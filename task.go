package inity

type task interface {
	Start() error
	Close()
}

type quit interface {
	Quit()
}

type taskAdapter struct {
	task task
}

func (t *taskAdapter) close() {
	t.task.Close()
}

func (t *taskAdapter) quit() {
	if q, ok := t.task.(quit); ok {
		q.Quit()
		return
	}

	// do not wait for task to close
	go t.close()
}
