package inity

import (
	"context"
	"errors"
	"os"
	"sync"

	"golang.org/x/sync/errgroup"
)

var (
	// ErrTaskManagerStopped indicates that the operation is now illegal because of
	// the task being stopped.
	ErrTaskManagerStopped = errors.New("task manager is stopping or stopped")
	// ErrTaskManagerClosed indicates that the task manager has been closed.
	ErrTaskManagerClosed = errors.New("task manager is closed")
)

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
	ctx, cancel := context.WithCancel(ctx)
	group, ctx := errgroup.WithContext(ctx)

	manager := &Manager{
		name:   name,
		group:  group,
		ctx:    ctx,
		cancel: cancel,
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
	cancel  context.CancelFunc
	signals <-chan os.Signal
	logger  Logger

	tasks []task

	force     bool
	mu        sync.Mutex
	closeOnce sync.Once
}

func (m *Manager) watchForShutdown(closed chan<- bool) {
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
	m.tasks = append(m.tasks, task)
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
	for _, task := range m.tasks {
		if _, ok := task.(*Manager); ok {
			m.group.Go(m.wrapStart(task.Start))
		} else {
			m.group.Go(task.Start)
		}
	}

	closed := make(chan bool, 10)
	m.watchForShutdown(closed)

	m.logger.Log(m.ctx, LevelInfo, "Waiting for exit conditions", "manager", m.name)

	// Can be unlocked
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
			m.tasks[i].Close()
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
			task := m.tasks[i]
			if t, ok := task.(quit); ok {
				t.Quit()
			} else {
				// do not wait for task to close
				go task.Close()
			}
		}
		m.logger.Log(m.ctx, LevelDebug, "Task manager closed", "manager", m.name)
	})
}
