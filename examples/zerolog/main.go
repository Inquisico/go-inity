package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/inquisico/go-inity"
	"github.com/inquisico/go-inity/examples/simple/httpserver"
	inityzerolog "github.com/inquisico/go-inity/examples/zerolog/logger"
	"github.com/rs/zerolog/log"
)

func main() {
	// Use global logger
	logger := log.Logger

	// Create a new inity logger
	inityLogger := inityzerolog.InityZerolog(logger)

	// Define shutting down signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Create a new inity instance
	taskManager := inity.New(context.Background(), "zero", inity.WithSignals(sigs), inity.WithLogger(inityLogger))
	defer taskManager.Close()

	// Init HTTP Server (we use this as a task for simplicity)
	httpServer := httpserver.New("8080")

	// Register HTTP server with task manager
	taskManager.Register(httpServer)

	// Wait for all tasks to exit
	if err := taskManager.Start(); err != nil {
		logger.Warn().Err(err).Msg("Task has returned an error or closed")
	}

	logger.Info().Msg("Exiting")
}
