package inity

type task interface {
	Start() error
	Close()
}

type quit interface {
	Quit()
}
