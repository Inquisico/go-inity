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
	// ErrTaskManagerClosed indicates that one or more tasks returned an error
	// during shutdown.
	ErrTaskManagerClosed = errors.New("task manager is closed")
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
// The Manager itself satisfies the Task interface so managers can be nested.
type Manager struct {
	name    string
	signals <-chan os.Signal
	logger  Logger

	mu    sync.Mutex
	tasks []*taskWrapper

	force     atomic.Bool
	closeOnce sync.Once
}

// Register adds a task to the manager start list. Register is safe to call
// from multiple goroutines but must not be called after Start.
func (m *Manager) Register(t Task) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks = append(m.tasks, &taskWrapper{task: t})
}

// Start launches all registered tasks under ctx and blocks until shutdown is
// complete. ctx cancellation triggers a graceful Close; a task returning an
// error triggers a forceful Quit.
func (m *Manager) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrTaskManagerStopped, err)
	}

	group, gCtx := errgroup.WithContext(ctx)

	m.mu.Lock()
	m.logger.Log(gCtx, LevelInfo, "Starting task manager", "manager", m.name)
	for _, t := range m.tasks {
		group.Go(m.wrapStart(gCtx, t))
	}
	closed := m.watchForShutdown(gCtx)
	m.logger.Log(gCtx, LevelInfo, "Waiting for exit conditions", "manager", m.name)
	m.mu.Unlock()

	err := group.Wait()

	m.logger.Log(gCtx, LevelDebug, "Waiting for close to finish", "manager", m.name)
	<-closed
	m.logger.Log(gCtx, LevelDebug, "Close finished")

	if err != nil {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeOnce.Do(func() {
		m.logger.Log(ctx, LevelDebug, "Closing task manager", "manager", m.name)
		for i := len(m.tasks) - 1; i >= 0; i-- {
			m.tasks[i].close()
		}
		m.logger.Log(ctx, LevelDebug, "Task manager closed", "manager", m.name)
	})
}

func (m *Manager) quitWithContext(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeOnce.Do(func() {
		m.logger.Log(ctx, LevelDebug, "Quitting task manager", "manager", m.name)
		for i := len(m.tasks) - 1; i >= 0; i-- {
			m.tasks[i].quit()
		}
		m.logger.Log(ctx, LevelDebug, "Task manager closed", "manager", m.name)
	})
}

func (m *Manager) watchForShutdown(ctx context.Context) <-chan struct{} {
	closed := make(chan struct{})

	go func() {
		defer close(closed)
		select {
		case s := <-m.signals:
			m.logger.Log(ctx, LevelDebug, "Signal received, closing task manager", "signal", s.String(), "manager", m.name)
			m.closeWithContext(ctx)
		case <-ctx.Done():
			if m.force.Load() {
				m.quitWithContext(ctx)
			} else {
				m.closeWithContext(ctx)
			}
		}
	}()

	return closed
}

func (m *Manager) wrapStart(ctx context.Context, t *taskWrapper) func() error {
	return func() error {
		if err := t.task.Start(ctx); err != nil {
			m.force.Store(true)
			return fmt.Errorf("task %q: %w", m.name, err)
		}

		return nil
	}
}
