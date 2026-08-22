package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/liliang-cn/tagit/internal/app"
	"github.com/liliang-cn/tagit/internal/runtime"
	"github.com/liliang-cn/tagit/internal/tagitpath"
)

func main() {
	acpPort := flag.Int("acp-port", 0, "TCP port for the ACP HTTP listener (0 disables ACP)")
	flag.Parse()

	// Write a pid file so `tagit status` / `tagit stop` can find this daemon even
	// when it is launched directly (e.g. by launchd via `brew services`, not by
	// `tagit start`). Removed on a clean shutdown.
	pidPath := filepath.Join(tagitpath.HomeDir(), "tagitd.pid")
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err == nil {
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err == nil {
			defer os.Remove(pidPath)
		}
	}

	// Agents act as the user, so they need the user's PATH — launchd and systemd
	// hand a daemon a minimal one, which is why a Claude Code hook that shells
	// out to `node` fails with "command not found" on a machine where node is
	// plainly installed. Resolved once, here, because it costs a login shell.
	if userPath := runtime.ResolveUserPathAtStartup(); userPath != "" {
		log.Printf("agent PATH from login shell: %s", userPath)
	} else {
		log.Printf("no login-shell PATH resolved; agents inherit the daemon's own")
	}

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
