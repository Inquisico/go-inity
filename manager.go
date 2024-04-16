package inity

import (
	"context"
	"errors"
	"os"
	"sync"

	"golang.org/x/sync/errgroup"
)

const (
	channelClosedSize = 10
)

var (
	// ErrTaskManagerStopped indicates that the operation is now illegal because of
	// the task being stopped.
	ErrTaskManagerStopped = errors.New("task manager is stopping or stopped")
	// ErrTaskManagerClosed indicates that the task manager has been closed.
	ErrTaskManagerClosed = errors.New("task manager is closed")
)

type Logger interface {
	Log(ctx context.Context, level Level, msg string, fields ...any)
}

type Option func(*Manager)

func WithLogger(logger Logger) Option {
	return func(m *Manager) {
		m.logger = logger
	}
}

func WithSignals(signals <-chan os.Signal) Option {
	return func(m *Manager) {
		m.signals = signals
	}
}

func New(ctx context.Context, name string, opts ...Option) *Manager {
	group, ctx := errgroup.WithContext(ctx)

	manager := &Manager{
		name:   name,
		group:  group,
		ctx:    ctx,
		logger: DefaultLogger(),
	}

	for _, opt := range opts {
		opt(manager)
	}

	return manager
}

type Manager struct {
	name    string
	group   *errgroup.Group
	ctx     context.Context
	signals <-chan os.Signal
	logger  Logger

	tasks []*taskWrapper

	force     bool
	mu        sync.Mutex
	closeOnce sync.Once
}

func (m *Manager) watchForShutdown() <-chan bool {
	closed := make(chan bool, channelClosedSize)
	// Terminate tasks when signaled or done
	go func() {
		select {
		case s := <-m.signals:
			m.logger.Log(m.ctx, LevelDebug, "Signal received, closing task manager", "signal", s.String(), "manager", m.name)
			m.Close()
			closed <- true
		case <-m.ctx.Done():
			if m.force {
				// we do not wait for quit to return
				closed <- true
				m.Quit()
			} else {
				// we wait everything to close gracefully before returning
				m.Close()
				closed <- true
			}
		}
	}()

	return closed
}

func (m *Manager) wrapStart(start func() error) func() error {
	return func() error {
		if err := start(); err != nil {
			m.force = true
			return err
		}

		return nil
	}
}

func (m *Manager) Register(task task) {
	m.tasks = append(m.tasks, &taskWrapper{task: task})
}

func (m *Manager) GetContext() context.Context {
	return m.ctx
}

func (m *Manager) Start() error {
	m.mu.Lock()

	if m.ctx.Err() != nil {
		m.mu.Unlock()
		return errors.Join(ErrTaskManagerStopped, m.ctx.Err())
	}

	m.logger.Log(m.ctx, LevelInfo, "Starting task manager", "manager", m.name)
	for _, t := range m.tasks {
		if leafManager, ok := t.task.(*Manager); ok {
			m.group.Go(m.wrapStart(leafManager.Start))
		} else {
			m.group.Go(t.task.Start)
		}
	}

	closed := m.watchForShutdown()

	m.logger.Log(m.ctx, LevelInfo, "Waiting for exit conditions", "manager", m.name)

	// Mutex can be unlocked after everything has started
	m.mu.Unlock()

	err := m.group.Wait()

	// Wait for close to finish before returning
	m.logger.Log(m.ctx, LevelDebug, "Waiting for close to finish", "manager", m.name)
	<-closed
	m.logger.Log(m.ctx, LevelDebug, "Close finished")

	if err != nil {
		return errors.Join(ErrTaskManagerClosed, err)
	}

	return nil
}

// Close stop the tasks gracefully in reverse order.
// It blocks until all tasks are closed.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeOnce.Do(func() {
		m.logger.Log(m.ctx, LevelDebug, "Closing task manager", "manager", m.name)
		for i := len(m.tasks) - 1; i >= 0; i-- {
			m.tasks[i].close()
		}
		m.logger.Log(m.ctx, LevelDebug, "Task manager closed", "manager", m.name)
	})
}

// Quit stop the tasks immediately in reverse order.
func (m *Manager) Quit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeOnce.Do(func() {
		m.logger.Log(m.ctx, LevelDebug, "Quiting task manager", "manager", m.name)
		for i := len(m.tasks) - 1; i >= 0; i-- {
			m.tasks[i].quit()
		}
		m.logger.Log(m.ctx, LevelDebug, "Task manager closed", "manager", m.name)
	})
}
