# Inity

[![Go Lint](https://github.com/Inquisico/go-inity/actions/workflows/golangci-lint-push.yaml/badge.svg)](https://github.com/Inquisico/go-inity/actions/workflows/golangci-lint-push.yaml) [![Go Test](https://github.com/Inquisico/go-inity/actions/workflows/go-test-push.yaml/badge.svg)](https://github.com/Inquisico/go-inity/actions/workflows/go-test-push.yaml) [![Release Drafter](https://github.com/Inquisico/go-inity/actions/workflows/release-drafter.yaml/badge.svg)](https://github.com/Inquisico/go-inity/actions/workflows/release-drafter.yaml)

Inity is a small task manager for bootstrapping Go services and shutting them down in reverse dependency order.

## What It Does

- Starts registered tasks concurrently.
- Cancels the manager context when any task returns an error.
- Shuts tasks down in reverse registration order.
- Supports an optional `Quit()` fast path for forced shutdown.

Any type with `Start() error` and `Close()` methods can be registered. If the type also implements `Quit()`, Inity uses it when another task fails and the remaining tasks should stop immediately.

## Install

```bash
go get github.com/inquisico/go-inity
```

## Quick Start

```go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/inquisico/go-inity"
)

type server struct{}

func (server) Start() error {
	return nil
}

func (server) Close() {}

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)

	manager := inity.New(context.Background(), "app", inity.WithSignals(sigs))
	manager.Register(server{})

	if err := manager.Start(); err != nil {
		log.Fatal(err)
	}
}
```

## Shutdown Semantics

- `Start()` blocks until every task exits.
- If the parent context is canceled or a configured signal arrives, Inity calls `Close()` on registered tasks in reverse order.
- If a task returns an error, Inity marks shutdown as forced and calls `Quit()` on remaining tasks when available.
- Tasks should treat their own normal shutdown path as success and return `nil` from `Start()` after `Close()` or `Quit()` unblocks them.

## Examples

Example programs live under `examples/`:

- `examples/simple`
- `examples/hierarchical`
- `examples/zerolog`

Run the simple example from the repository root:

```bash
make run
```

## Development

```bash
make update
make test
make lint
```
