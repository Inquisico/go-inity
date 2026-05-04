package inity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type blockingTask struct {
	started chan struct{}
	closeCh chan struct{}
	closeFn func() error
	onClose func()
	onQuit  func()
	once    sync.Once
}

func newBlockingTask() *blockingTask {
	return &blockingTask{
		started: make(chan struct{}),
		closeCh: make(chan struct{}),
	}
}

func (t *blockingTask) Start(_ context.Context) error {
	close(t.started)
	<-t.closeCh

	if t.closeFn != nil {
		return t.closeFn()
	}

	return nil
}

func (t *blockingTask) Close() {
	t.once.Do(func() {
		if t.onClose != nil {
			t.onClose()
		}
		close(t.closeCh)
	})
}

func (t *blockingTask) Quit() {
	t.once.Do(func() {
		if t.onQuit != nil {
			t.onQuit()
		}
		close(t.closeCh)
	})
}

type errorTask struct {
	err error
}

func (t errorTask) Start(_ context.Context) error {
	return t.err
}

func (errorTask) Close() {}

func TestManagerCloseOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	manager := New("test")
	task := newBlockingTask()

	closed := make(chan struct{}, 1)
	task.onClose = func() {
		closed <- struct{}{}
	}

	manager.Register(task)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(ctx)
	}()

	select {
	case <-task.started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}

	cancel()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not close task after context cancellation")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("manager start returned error after graceful shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manager start did not return after graceful shutdown")
	}
}

func TestManagerQuitOnTaskError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("boom")
	manager := New("test")

	blocking := newBlockingTask()
	quitCalled := make(chan struct{}, 1)
	closeCalled := make(chan struct{}, 1)
	blocking.onQuit = func() {
		quitCalled <- struct{}{}
	}
	blocking.onClose = func() {
		closeCalled <- struct{}{}
	}

	manager.Register(blocking)
	manager.Register(errorTask{err: expectedErr})

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(context.Background())
	}()

	select {
	case <-blocking.started:
	case <-time.After(2 * time.Second):
		t.Fatal("blocking task did not start")
	}

	err := <-errCh
	if err == nil {
		t.Fatal("manager start returned nil error after task failure")
	}

	if !errors.Is(err, ErrTaskManagerClosed) {
		t.Fatalf("manager start error %v does not wrap ErrTaskManagerClosed", err)
	}

	if !errors.Is(err, expectedErr) {
		t.Fatalf("manager start error %v does not wrap original task error", err)
	}

	select {
	case <-quitCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not quit remaining tasks after task failure")
	}

	select {
	case <-closeCalled:
		t.Fatal("manager closed remaining task instead of quitting it")
	default:
	}
}

func TestManagerStartReturnsErrTaskManagerStoppedWhenContextAlreadyCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	manager := New("test")
	err := manager.Start(ctx)
	if !errors.Is(err, ErrTaskManagerStopped) {
		t.Fatalf("expected ErrTaskManagerStopped, got %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected wrapped context.Canceled, got %v", err)
	}
}

func TestManagerStartReturnsErrTaskManagerStartedWhenStartedTwice(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	manager := New("test")
	task := newBlockingTask()
	manager.Register(task)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(ctx)
	}()

	select {
	case <-task.started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}

	if err := manager.Start(ctx); !errors.Is(err, ErrTaskManagerStarted) {
		t.Fatalf("expected ErrTaskManagerStarted on second Start, got %v", err)
	}

	cancel()

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("manager start did not return after graceful shutdown")
	}
}

func TestManagerRegisterAfterStartPanics(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	manager := New("test")
	task := newBlockingTask()
	manager.Register(task)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(ctx)
	}()

	select {
	case <-task.started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from Register after Start")
		}
		cancel()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
			t.Fatal("manager start did not return after graceful shutdown")
		}
	}()

	manager.Register(newBlockingTask())
}

func TestManagerCloseOnParentCancelDoesNotForceQuit(t *testing.T) {
	// Verifies the parentCtx-vs-gCtx distinction in watchForShutdown: a task
	// that returns ctx.Err() after the parent ctx cancels should not flip the
	// manager into a forced quit.
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	manager := New("test")

	ctxAwareTask := &ctxRespectingTask{started: make(chan struct{})}
	blocking := newBlockingTask()
	closeCalled := make(chan struct{}, 1)
	quitCalled := make(chan struct{}, 1)
	blocking.onClose = func() {
		closeCalled <- struct{}{}
	}
	blocking.onQuit = func() {
		quitCalled <- struct{}{}
	}

	manager.Register(blocking)
	manager.Register(ctxAwareTask)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(ctx)
	}()

	select {
	case <-ctxAwareTask.started:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx-aware task did not start")
	}

	cancel()

	select {
	case <-closeCalled:
	case <-quitCalled:
		t.Fatal("manager force-quit on parent cancellation when graceful close was expected")
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not close after parent cancellation")
	}

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("manager start did not return")
	}
}

func TestManagerShutdownBeforeStartIsNoOp(t *testing.T) {
	// A pre-Start Close or Quit must be a no-op. Otherwise it burns closeOnce
	// and the real shutdown after Start silently skips every task's Close.
	t.Parallel()

	cases := []struct {
		name    string
		trigger func(*Manager)
	}{
		{"Close", func(m *Manager) { m.Close() }},
		{"Quit", func(m *Manager) { m.Quit() }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			manager := New("test")
			task := newBlockingTask()
			closed := make(chan struct{}, 1)
			task.onClose = func() {
				closed <- struct{}{}
			}
			manager.Register(task)

			tc.trigger(manager) // before Start: must not consume closeOnce.

			errCh := make(chan error, 1)
			go func() {
				errCh <- manager.Start(ctx)
			}()

			select {
			case <-task.started:
			case <-time.After(2 * time.Second):
				t.Fatal("task did not start")
			}

			cancel()

			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Fatalf("manager did not close task after context cancellation; pre-Start %s burned closeOnce", tc.name)
			}

			select {
			case <-errCh:
			case <-time.After(2 * time.Second):
				t.Fatal("manager did not return after graceful shutdown")
			}
		})
	}
}

func TestManagerStartReturnsNilOnGracefulCtxCancel(t *testing.T) {
	// A ctx-respecting task returns ctx.Err() on shutdown. That must not
	// surface as an error from Start: parent ctx cancellation is graceful.
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	manager := New("test")
	task := &ctxRespectingTask{started: make(chan struct{})}
	manager.Register(task)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(ctx)
	}()

	select {
	case <-task.started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error from graceful ctx-cancel shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not return")
	}
}

func TestManagerStartReturnsNilOnGracefulCtxDeadline(t *testing.T) {
	// Same as above but for DeadlineExceeded — both must be treated as
	// graceful, not failure.
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	t.Cleanup(cancel)

	manager := New("test")
	task := &ctxRespectingTask{started: make(chan struct{})}
	manager.Register(task)

	if err := manager.Start(ctx); err != nil {
		t.Fatalf("expected nil error from graceful deadline shutdown, got %v", err)
	}
}

func TestManagerStartReturnsNilWhenTaskWrapsCtxErr(t *testing.T) {
	// Tasks that wrap ctx.Err() in their own error type should still be
	// recognised as graceful via errors.Is.
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	manager := New("test")
	task := &wrappedCtxErrTask{started: make(chan struct{})}
	manager.Register(task)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(ctx)
	}()

	select {
	case <-task.started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error when task wraps ctx.Err(), got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not return")
	}
}

func TestManagerHandlesClosedSignalChannel(t *testing.T) {
	// A closed signal channel produces a zero-value receive on every read.
	// The watchdog must not panic on s.String() and must drive a graceful
	// close.
	t.Parallel()

	sigCh := make(chan os.Signal)
	close(sigCh)

	manager := New("test", WithSignals(sigCh))
	task := newBlockingTask()
	closed := make(chan struct{}, 1)
	task.onClose = func() {
		closed <- struct{}{}
	}
	manager.Register(task)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(ctx)
	}()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not close after signal channel closure")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error after closed-signal shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not return")
	}
}

func TestManagerSignalDrivenShutdownReturnsNil(t *testing.T) {
	t.Parallel()

	sigCh := make(chan os.Signal, 1)
	manager := New("test", WithSignals(sigCh))
	task := newBlockingTask()
	manager.Register(task)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(ctx)
	}()

	select {
	case <-task.started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}

	sigCh <- syscall.SIGTERM

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error from signal-driven shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not return")
	}
}

func TestManagerCloseDoesNotHoldMutexDuringTaskClose(t *testing.T) {
	// While a task's Close handler is running, the manager's mutex must be
	// free so other manager methods do not block on slow user code. We probe
	// this by stalling inside Close and observing that a concurrent Start
	// returns ErrTaskManagerStarted promptly instead of blocking on m.mu.
	t.Parallel()

	manager := New("test")

	closeRunning := make(chan struct{})
	closeCanFinish := make(chan struct{})
	task := newBlockingTask()
	task.onClose = func() {
		close(closeRunning)
		<-closeCanFinish
	}
	manager.Register(task)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	startErr := make(chan error, 1)
	go func() {
		startErr <- manager.Start(ctx)
	}()

	select {
	case <-task.started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}

	cancel()

	select {
	case <-closeRunning:
	case <-time.After(2 * time.Second):
		t.Fatal("close handler did not run")
	}

	// The close handler is now blocked. The manager's mutex must be free
	// so a second Start observes started=true and returns immediately.
	secondStart := make(chan error, 1)
	go func() {
		secondStart <- manager.Start(context.Background())
	}()

	select {
	case err := <-secondStart:
		if !errors.Is(err, ErrTaskManagerStarted) {
			t.Fatalf("expected ErrTaskManagerStarted, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked while a Close handler was running — mutex was held across user code")
	}

	close(closeCanFinish)

	select {
	case <-startErr:
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not return")
	}
}

func TestManagerStartedGateInvokesEveryTask(t *testing.T) {
	// With the started gate, the manager schedules and dispatches every
	// task's Start before invoking Close, so when shutdown fires while
	// goroutines are still being scheduled, every task still has both Start
	// and Close called. (Strict in-task ordering of Start vs Close is not
	// asserted: once the goroutine is dispatched, the Start call and a
	// concurrent Close race; the gate only narrows the window.)
	t.Parallel()

	const taskCount = 64

	tasks := make([]*orderRecordingTask, taskCount)
	manager := New("test")
	for i := range tasks {
		tasks[i] = &orderRecordingTask{}
		manager.Register(tasks[i])
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Start(ctx)
	}()

	// Wait until manager.Start is past its ctx.Err() check and has launched
	// goroutines — otherwise an immediate cancel short-circuits Start and no
	// task ever runs. Any task having entered Start proves we're past that.
	deadline := time.Now().Add(2 * time.Second)
	for {
		entered := false
		for _, task := range tasks {
			if task.startCalled.Load() {
				entered = true
				break
			}
		}
		if entered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manager.Start did not enter within 2s")
		}
		runtime.Gosched()
	}

	cancel()

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not return")
	}

	for i, task := range tasks {
		if !task.startCalled.Load() {
			t.Fatalf("task %d: Start was not called", i)
		}
		if !task.closeCalled.Load() {
			t.Fatalf("task %d: Close was not called", i)
		}
	}
}

type ctxRespectingTask struct {
	started chan struct{}
}

func (t *ctxRespectingTask) Start(ctx context.Context) error {
	close(t.started)
	<-ctx.Done()
	// Returning the bare ctx.Err() is the idiomatic shutdown pattern this
	// type is intended to model — the manager must recognise it as graceful.
	return ctx.Err() //nolint:wrapcheck
}

func (*ctxRespectingTask) Close() {}

type wrappedCtxErrTask struct {
	started chan struct{}
}

func (t *wrappedCtxErrTask) Start(ctx context.Context) error {
	close(t.started)
	<-ctx.Done()
	return fmt.Errorf("wrappedCtxErrTask shutting down: %w", ctx.Err())
}

func (*wrappedCtxErrTask) Close() {}

// orderRecordingTask records that its Start and Close were each invoked, so
// a test can assert the manager dispatched every registered task.
type orderRecordingTask struct {
	startCalled atomic.Bool
	closeCalled atomic.Bool
}

func (t *orderRecordingTask) Start(ctx context.Context) error {
	t.startCalled.Store(true)
	<-ctx.Done()
	return nil
}

func (t *orderRecordingTask) Close() {
	t.closeCalled.Store(true)
}
