package inity

import (
	"context"
)

// Task is the unit of work managed by a Manager. Implementations must start
// when Start is called and tear down on Close. Close must always return so the
// manager can finish a graceful shutdown.
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

type taskWrapper struct {
	task Task
}

func (t *taskWrapper) close() {
	t.task.Close()
}

func (t *taskWrapper) quit() {
	if q, ok := t.task.(Quit); ok {
		q.Quit()
		return
	}
	// Fallback: the task didn't opt in to fast shutdown, so we still call Close.
	// This blocks until Close returns; tasks that need to abort quickly should
	// implement Quit.
	t.task.Close()
}
