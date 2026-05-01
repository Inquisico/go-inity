// Package inity provides a small task manager for starting and stopping
// application services in dependency order.
package inity

import (
	"context"
	"fmt"
	"log"
)

// Level represents the severity of a log entry.
type Level int

const (
	// LevelDebug records verbose diagnostic messages.
	LevelDebug Level = -4
	// LevelInfo records routine lifecycle messages.
	LevelInfo Level = 0
	// LevelWarn records recoverable problems.
	LevelWarn Level = 4
	// LevelError records failures that require attention.
	LevelError Level = 8
)

// LoggerFunc is a function that also implements Logger interface.
type LoggerFunc func(ctx context.Context, level Level, msg string, fields ...any)

// Log calls the wrapped function.
func (f LoggerFunc) Log(ctx context.Context, level Level, msg string, fields ...any) {
	f(ctx, level, msg, fields...)
}

// DefaultLogger returns the package's standard-library-backed logger.
func DefaultLogger() LoggerFunc {
	l := log.Default()

	return LoggerFunc(func(_ context.Context, level Level, msg string, fields ...any) {
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
