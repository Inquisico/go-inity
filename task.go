package inity

type task interface {
	Start() error
	Close()
}

type quit interface {
	Quit()
}

type taskWrapper struct {
	task task
}

func (t *taskWrapper) close() {
	t.task.Close()
}

func (t *taskWrapper) quit() {
	if q, ok := t.task.(quit); ok {
		q.Quit()
		return
	}

	// do not wait for task to close
	go t.close()
}
