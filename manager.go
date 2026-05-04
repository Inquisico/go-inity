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
	// ErrTaskManagerStopped is returned when Start is called with an
	// already-cancelled context.
	ErrTaskManagerStopped = errors.New("task manager is stopping or stopped")
	// ErrTaskManagerClosed wraps the underlying failure when a task returns
	// a non-context error. Graceful ctx-cancel shutdowns are not wrapped.
	ErrTaskManagerClosed = errors.New("task manager is closed")
	// ErrTaskManagerStarted is returned when Start is called twice.
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
// A nil channel disables signal-driven shutdown (the manager then relies
// solely on ctx cancellation and task failures).
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
	// Constant after New; safe to read without synchronization.
	name    string
	signals <-chan os.Signal
	logger  Logger

	// Guarded by mu. tasks is also read without mu after started=true,
	// because Register panics once started=true so the slice is stable.
	mu      sync.Mutex
	tasks   []*taskWrapper
	started bool

	// Independently synchronized: force flips on the first non-ctx task
	// failure; closeOnce gates the single Close-or-Quit teardown pass.
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

// Start launches all registered tasks under ctx and blocks until shutdown
// is complete. ctx cancellation or a configured signal triggers a graceful
// Close; a task returning a non-context error triggers a forceful Quit.
// Graceful shutdowns return nil — including when ctx-aware tasks return
// ctx.Err(). Only a genuine task failure surfaces as an
// ErrTaskManagerClosed-wrapped error.
func (m *Manager) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrTaskManagerStopped, err)
	}

	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return fmt.Errorf("manager %q: %w", m.name, ErrTaskManagerStarted)
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
// single execution: whichever runs first wins, subsequent calls are no-ops.
// Safe to call in a defer alongside Start. Blocks until each task's start
// goroutine has been entered and every Close has returned.
func (m *Manager) Close() {
	m.closeWithContext(context.Background())
}

// Quit stops the tasks immediately in reverse order, calling Quit on tasks
// that implement it and Close on those that do not. See Close for the
// shared-execution and blocking contract.
func (m *Manager) Quit() {
	m.quitWithContext(context.Background())
}

func (m *Manager) closeWithContext(ctx context.Context) {
	m.shutdown(ctx, "Closing task manager", (*taskWrapper).close)
}

func (m *Manager) quitWithContext(ctx context.Context) {
	m.shutdown(ctx, "Quitting task manager", (*taskWrapper).quit)
}

// shutdown drives the single teardown pass. The hasStarted guard keeps a
// pre-Start Close/Quit from burning closeOnce.
func (m *Manager) shutdown(ctx context.Context, enterMsg string, teardown func(*taskWrapper)) {
	if !m.hasStarted() {
		return
	}
	m.closeOnce.Do(func() {
		m.logger.Log(ctx, LevelInfo, enterMsg, "manager", m.name)
		for i := len(m.tasks) - 1; i >= 0; i-- {
			teardown(m.tasks[i])
		}
		m.logger.Log(ctx, LevelInfo, "Task manager closed", "manager", m.name)
	})
}

// hasStarted reports whether Start has been called. Once it returns true,
// reading m.tasks without mu is safe because Register panics on started=true.
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
		err := t.task.Start(ctx)
		if err == nil {
			return nil
		}

		wrapped := fmt.Errorf("manager %q: %w", m.name, err)
		// A context-cancellation error means the task is shutting down in
		// response to ctx.Done() and is not a real failure, so do not flip
		// m.force.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return wrapped
		}
		m.force.Store(true)
		m.logger.Log(context.WithoutCancel(ctx), LevelError, "Task failed", "manager", m.name, "error", err)
		return wrapped
	}
}
