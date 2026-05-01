package inity

import (
	"context"
	"errors"
	"sync"
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
