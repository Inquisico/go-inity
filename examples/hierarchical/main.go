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
	ctx := context.Background()

	// Create a depdendent manager
	leafAManager := inity.New(ctx, "leaf")
	defer leafAManager.Close()
	{ // This block is just for easy reading
		// Init HTTP Server A
		httpServerA := httpserver.New("8080")

		// Register HTTP server A with leaf manager
		leafAManager.Register(httpServerA)
	}

	// Create a depdendent manager
	leafBManager := inity.New(ctx, "leaf")
	defer leafBManager.Close()
	{ // This block is just for easy reading
		// Init HTTP Server A
		httpServerB := httpserver.New("8081")

		// Register HTTP server A with leaf manager
		leafBManager.Register(httpServerB)
	}

	// Define shutting down signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Create a task manager
	rootManager := inity.New(context.Background(), "root", inity.WithSignals(sigs))
	defer rootManager.Close()
	{ // This block is just for easy reading
		// Register leaf managers with root manager
		rootManager.Register(leafAManager)
		rootManager.Register(leafBManager)

		// Init HTTP Server C
		httpServerC := httpserver.New("8088")

		// Register HTTP server C with root manager
		rootManager.Register(httpServerC)
	}

	// Wait for all tasks to exit
	if err := rootManager.Start(); err != nil {
		log.Println("WARN: Task has returned an error or closed", err)
	}

	log.Println("Exiting")
}
