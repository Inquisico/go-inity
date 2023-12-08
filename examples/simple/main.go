package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/inquisico/go-inity"
	"github.com/inquisico/go-inity/examples/simple/httpserver"
)

func main() {
	// Define shutting down signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Create a task manager
	taskManager := inity.New(context.Background(), "main", inity.WithSignals(sigs))
	defer taskManager.Close()

	// Init HTTP Server  (we use this as a task for simplicity)
	httpServer := httpserver.New("8080")

	// Register HTTP server with task manager
	taskManager.Register(httpServer)

	// Wait for all tasks to exit
	if err := taskManager.Start(); err != nil {
		log.Println("WARN: Task has returned an error or closed", err)
	}

	log.Println("Exiting")
}
