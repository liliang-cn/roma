package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/liliang/roma/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	daemon, err := app.NewDaemon()
	if err != nil {
		log.Fatalf("create daemon: %v", err)
	}

	if err := daemon.Run(ctx); err != nil {
		log.Fatalf("run daemon: %v", err)
	}
}
