// Package inityzerolog adapts a zerolog.Logger to the inity.Logger interface.
package inityzerolog

import (
	"context"

	"github.com/inquisico/go-inity"
	"github.com/rs/zerolog"
)

// New returns an inity.Logger backed by the given zerolog.Logger.
func New(l zerolog.Logger) inity.Logger {
	return inity.LoggerFunc(func(_ context.Context, level inity.Level, msg string, fields ...any) {
		switch level {
		case inity.LevelDebug:
			l.Debug().Fields(fields).Msg(msg)
		case inity.LevelWarn:
			l.Warn().Fields(fields).Msg(msg)
		case inity.LevelError:
			l.Error().Fields(fields).Msg(msg)
		case inity.LevelInfo:
			fallthrough
		default:
			l.Info().Fields(fields).Msg(msg)
		}
	})
}
