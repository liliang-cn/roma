package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/liliang-cn/roma/internal/app"
)

func main() {
	acpPort := flag.Int("acp-port", 0, "TCP port for the ACP HTTP listener (0 disables ACP)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	daemon, err := app.NewDaemonWithOptions(app.DaemonOptions{ACPPort: *acpPort})
	if err != nil {
		log.Fatalf("create daemon: %v", err)
	}

	if err := daemon.Run(ctx); err != nil {
		log.Fatalf("run daemon: %v", err)
	}
}
