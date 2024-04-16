package inity

import "sync"

type task interface {
	Start() error
	Close()
}

type quit interface {
	Quit()
}

type taskAdapter struct {
	task      task
	closeOnce sync.Once
}

func (t *taskAdapter) close() {
	t.closeOnce.Do(t.task.Close)
}

func (t *taskAdapter) quit() {
	if q, ok := t.task.(quit); ok {
		t.closeOnce.Do(q.Quit)
		return
	}

	// do not wait for task to close
	go t.close()
}
