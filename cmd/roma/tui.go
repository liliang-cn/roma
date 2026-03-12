package main

import (
	"context"
	"fmt"
	"os"

	"github.com/liliang-cn/roma/internal/tui"
)

func runTUI(ctx context.Context, args []string) error {
	workingDir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--cwd":
			i++
			if i >= len(args) {
				return fmt.Errorf("--cwd requires a value")
			}
			workingDir = args[i]
		default:
			return fmt.Errorf("unknown tui argument %q", args[i])
		}
	}
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}
	return tui.Run(ctx, tui.Options{WorkingDir: workingDir})
}
