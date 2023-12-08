package inityzerolog

import (
	"context"
	"fmt"

	"github.com/inquisico/go-inity"
	"github.com/rs/zerolog"
)

func InityZerolog(l zerolog.Logger) inity.Logger {
	l = l.With().Logger()

	return inity.LoggerFunc(func(ctx context.Context, level inity.Level, msg string, fields ...any) {
		switch level {
		case inity.LevelDebug:
			l.Debug().Fields(fields).Msg(msg)
		case inity.LevelInfo:
			l.Info().Fields(fields).Msg(msg)
		case inity.LevelWarn:
			l.Warn().Fields(fields).Msg(msg)
		case inity.LevelError:
			l.Error().Fields(fields).Msg(msg)
		default:
			panic(fmt.Sprintf("unknown level %v", level))
		}
	})
}
