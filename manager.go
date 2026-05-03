package inity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

var (
	// ErrTaskManagerStopped indicates that the operation is now illegal because
	// the parent context was already cancelled when Start was called.
	ErrTaskManagerStopped = errors.New("task manager is stopping or stopped")
	// ErrTaskManagerClosed indicates that one or more tasks returned a
	// non-context error. Context-cancellation errors observed during graceful
	// shutdown are not wrapped in this sentinel.
	ErrTaskManagerClosed = errors.New("task manager is closed")
	// ErrTaskManagerStarted indicates that Start was called on a manager that
	// has already been started.
	ErrTaskManagerStarted = errors.New("task manager already started")
)

// Logger records lifecycle events emitted by the manager.
type Logger interface {
	Log(ctx context.Context, level Level, msg string, fields ...any)
}

// Option configures a Manager during construction.
type Option func(*Manager)

// WithLogger configures the logger used by the manager. A nil logger is
// ignored so the default logger remains in place.
func WithLogger(logger Logger) Option {
	return func(m *Manager) {
		if logger == nil {
			return
		}
		m.logger = logger
	}
}

// WithSignals configures the signal channel that triggers graceful shutdown.
func WithSignals(signals <-chan os.Signal) Option {
	return func(m *Manager) {
		m.signals = signals
	}
}

// New constructs a Manager. The returned manager is inert until Start is
// called.
func New(name string, opts ...Option) *Manager {
	manager := &Manager{
		name:   name,
		logger: DefaultLogger(),
	}

	for _, opt := range opts {
		opt(manager)
	}

	return manager
}

// Manager starts registered tasks and shuts them down in reverse order.
// Tasks are started concurrently — registration order does not impose a
// start-order dependency, only a stop-order one. A Manager is single-use:
// once Start has been called the manager cannot be reset or re-run.
type Manager struct {
	name    string
	signals <-chan os.Signal
	logger  Logger

	mu      sync.Mutex
	tasks   []*taskWrapper
	started bool

	force     atomic.Bool
	closeOnce sync.Once
}

// Register adds a task to the manager start list. Register is safe to call
// from multiple goroutines but panics if called after Start.
func (m *Manager) Register(t Task) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		panic(fmt.Sprintf("inity: Register called on manager %q after Start", m.name))
	}
	m.tasks = append(m.tasks, newTaskWrapper(t))
}

// Start launches all registered tasks under ctx and blocks until shutdown is
// complete. ctx cancellation or a configured signal triggers a graceful Close;
// a task returning a non-context error triggers a forceful Quit. Start returns
// nil for any graceful shutdown (including when ctx-aware tasks return
// ctx.Err()) and an ErrTaskManagerClosed-wrapped error only when a task
// genuinely failed. Start may only be called once per Manager.
func (m *Manager) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrTaskManagerStopped, err)
	}

	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrTaskManagerStarted, m.name)
	}
	m.started = true
	m.mu.Unlock()

	group, gCtx := errgroup.WithContext(ctx)

	m.logger.Log(ctx, LevelInfo, "Starting task manager", "manager", m.name)
	for _, t := range m.tasks {
		group.Go(m.wrapStart(gCtx, t))
	}
	closed := m.watchForShutdown(ctx, gCtx)
	m.logger.Log(ctx, LevelInfo, "Waiting for exit conditions", "manager", m.name)

	err := group.Wait()

	logCtx := context.WithoutCancel(ctx)
	m.logger.Log(logCtx, LevelDebug, "Waiting for close to finish", "manager", m.name)
	<-closed
	m.logger.Log(logCtx, LevelDebug, "Close finished", "manager", m.name)

	// Only surface errors if a task genuinely failed. Context-cancellation
	// errors returned by well-behaved tasks during graceful shutdown are not
	// failures, so they do not flip m.force and do not propagate here.
	if err != nil && m.force.Load() {
		return errors.Join(ErrTaskManagerClosed, err)
	}

	return nil
}

// Close stops the tasks gracefully in reverse order. Close and Quit share a
// single execution: whichever runs first wins, and subsequent calls to either
// are no-ops. It is safe to call Close in a defer alongside Start.
func (m *Manager) Close() {
	m.closeWithContext(context.Background())
}

// Quit stops the tasks immediately in reverse order, calling Quit on tasks
// that implement it and Close on those that do not. See Close for the
// shared-execution contract.
func (m *Manager) Quit() {
	m.quitWithContext(context.Background())
}

func (m *Manager) closeWithContext(ctx context.Context) {
	if !m.hasStarted() {
		return
	}
	m.closeOnce.Do(func() {
		m.logger.Log(ctx, LevelInfo, "Closing task manager", "manager", m.name)
		for i := len(m.tasks) - 1; i >= 0; i-- {
			m.tasks[i].close()
		}
		m.logger.Log(ctx, LevelInfo, "Task manager closed", "manager", m.name)
	})
}

func (m *Manager) quitWithContext(ctx context.Context) {
	if !m.hasStarted() {
		return
	}
	m.closeOnce.Do(func() {
		m.logger.Log(ctx, LevelInfo, "Quitting task manager", "manager", m.name)
		for i := len(m.tasks) - 1; i >= 0; i-- {
			m.tasks[i].quit()
		}
		m.logger.Log(ctx, LevelInfo, "Task manager closed", "manager", m.name)
	})
}

// hasStarted reports whether Start has been called. The shutdown helpers
// use it so a pre-Start Close/Quit is a no-op rather than burning
// closeOnce and disabling the real shutdown that follows Start. Reading
// m.tasks after this returns true is safe because Register panics once
// started=true, so the slice is stable.
func (m *Manager) hasStarted() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

// watchForShutdown blocks until a shutdown trigger is observed, then drives
// the appropriate teardown. parentCtx is the ctx passed to Start; gCtx is the
// errgroup-derived child. Distinguishing them lets us tell parent cancellation
// (graceful close) from a task error (forceful quit) when both eventually
// cancel gCtx. Shutdown logging uses a detached context so the messages are
// not dropped by ctx-aware loggers when parentCtx has already cancelled.
func (m *Manager) watchForShutdown(parentCtx, gCtx context.Context) <-chan struct{} {
	closed := make(chan struct{})
	logCtx := context.WithoutCancel(parentCtx)

	go func() {
		defer close(closed)
		select {
		case s, ok := <-m.signals:
			sig := "<channel closed>"
			if ok && s != nil {
				sig = s.String()
			}
			m.logger.Log(logCtx, LevelDebug, "Signal received, closing task manager", "signal", sig, "manager", m.name)
			m.closeWithContext(logCtx)
		case <-gCtx.Done():
			// gCtx cancels when parentCtx cancels, when a task errors, or when
			// errgroup.Wait returns. m.force is only set on real (non-ctx)
			// failures (see wrapStart), so it cleanly distinguishes graceful
			// shutdown from a peer-task crash.
			if m.force.Load() {
				m.quitWithContext(logCtx)
			} else {
				m.closeWithContext(logCtx)
			}
		}
	}()

	return closed
}

func (m *Manager) wrapStart(ctx context.Context, t *taskWrapper) func() error {
	return func() error {
		// Signal that this task's Start is about to be invoked. close/quit
		// block on this channel so a fast shutdown cannot reach user Close
		// before user Start has been entered.
		close(t.started)
		if err := t.task.Start(ctx); err != nil {
			// A context-cancellation error means the task is shutting down in
			// response to ctx.Done() and is not a real failure, so do not flip
			// m.force. Anything else is treated as a peer-visible crash.
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				m.force.Store(true)
			}
			return fmt.Errorf("manager %q: %w", m.name, err)
		}

		return nil
	}
}
