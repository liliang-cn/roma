package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/liliang-cn/roma/internal/tui"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("get working directory: %v", err)
	}
	if err := tui.Run(ctx, tui.Options{WorkingDir: wd}); err != nil {
		log.Fatal(err)
	}
}
