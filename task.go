package inity

import (
	"context"
)

// Task is the unit of work managed by a Manager. Close must always return
// so the manager can finish a graceful shutdown.
//
// The manager guarantees Close (and Quit) are not called until the
// goroutine that runs Start has been entered, so an unscheduled task never
// sees Close. Once entered, Start and a concurrent Close race;
// implementations must therefore tolerate Close firing while Start is
// still executing — including during Start's own setup.
type Task interface {
	Start(ctx context.Context) error
	Close()
}

// Quit is an optional Task extension. The manager calls Quit instead of
// Close on a forceful shutdown (peer task failed) so the task can abort
// fast rather than completing graceful teardown.
type Quit interface {
	Quit()
}

// taskWrapper pairs a Task with a started channel that wrapStart closes
// just before invoking task.Start, so close/quit can block on it and never
// teardown-call a task whose Start has not yet been entered.
type taskWrapper struct {
	task    Task
	started chan struct{}
}

func newTaskWrapper(t Task) *taskWrapper {
	return &taskWrapper{
		task:    t,
		started: make(chan struct{}),
	}
}

func (t *taskWrapper) close() {
	<-t.started
	t.task.Close()
}

func (t *taskWrapper) quit() {
	<-t.started
	if q, ok := t.task.(Quit); ok {
		q.Quit()
		return
	}
	// Fallback: task didn't opt in to fast shutdown, so we call Close and
	// block until it returns.
	t.task.Close()
}
