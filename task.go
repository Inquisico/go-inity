package inity

import (
	"context"
)

// Task is the unit of work managed by a Manager. Implementations must start
// when Start is called and tear down on Close. Close must always return so the
// manager can finish a graceful shutdown.
//
// The manager guarantees that Close (and Quit, if implemented) is not invoked
// until the goroutine that will run Start has been scheduled and entered, so
// implementations never see Close fire on an entirely unscheduled task. After
// that point the call into Start and a concurrent Close race: Start typically
// blocks for the lifetime of the task, so concurrent teardown is unavoidable.
// Implementations must therefore be safe to Close while Start is still
// executing, including during whatever setup Start does on its first lines.
type Task interface {
	Start(ctx context.Context) error
	Close()
}

// Quit is an optional Task extension. When the manager performs a forceful
// shutdown (a peer task failed) it calls Quit instead of Close so the task can
// abort fast rather than completing graceful teardown.
type Quit interface {
	Quit()
}

// taskWrapper carries the per-task state the manager needs around a Task: the
// task itself and a started channel that wrapStart closes immediately before
// invoking task.Start. close/quit block on that channel so the manager never
// teardown-calls a task whose Start has not yet been entered.
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
	// Fallback: the task didn't opt in to fast shutdown, so we still call Close.
	// This blocks until Close returns; tasks that need to abort quickly should
	// implement Quit.
	t.task.Close()
}
