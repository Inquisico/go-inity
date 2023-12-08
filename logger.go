package inity

import (
	"context"
	"fmt"
	"log"
)

type Level int

const (
	LevelDebug Level = -4
	LevelInfo  Level = 0
	LevelWarn  Level = 4
	LevelError Level = 8
)

type Logger interface {
	Log(ctx context.Context, level Level, msg string, fields ...any)
}

// LoggerFunc is a function that also implements Logger interface.
type LoggerFunc func(ctx context.Context, level Level, msg string, fields ...any)

func (f LoggerFunc) Log(ctx context.Context, level Level, msg string, fields ...any) {
	f(ctx, level, msg, fields...)
}

func DefaultLogger() Logger {
	l := log.Default()

	return LoggerFunc(func(ctx context.Context, level Level, msg string, fields ...any) {
		switch level {
		case LevelDebug:
			l.Println("DEBUG:", msg, fields)
		case LevelInfo:
			l.Println("INFO:", msg, fields)
		case LevelWarn:
			l.Println("WARN:", msg, fields)
		case LevelError:
			l.Println("ERROR:", msg, fields)
		default:
			panic(fmt.Sprintf("unknown level %v", level))
		}
	})
}
