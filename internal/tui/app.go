package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/liliang-cn/roma/internal/agents"
	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/app"
	"github.com/liliang-cn/roma/internal/romapath"
)

func Run(ctx context.Context, opts Options) error {
	workingDir := strings.TrimSpace(opts.WorkingDir)
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
	}

	client := api.NewClientForControlDir(workingDir, romapath.HomeDir())

	registry, err := agents.DefaultRegistry()
	if err != nil {
		return fmt.Errorf("load agent registry: %w", err)
	}
	registry.SetUserConfigPath(agents.DefaultUserConfigPath())
	if err := registry.LoadUserConfig(registry.UserConfigPath()); err != nil {
		return fmt.Errorf("load user agent config: %w", err)
	}
	profiles := registry.WithResolvedAvailability(ctx)

	var daemonCancel context.CancelFunc
	var daemonErrCh <-chan error
	bootMessage := "Connected to existing romad."
	if !client.Available() {
		daemon, err := app.NewDaemonForWorkingDir(workingDir)
		if err != nil {
			return fmt.Errorf("create embedded daemon: %w", err)
		}
		daemonCtx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		logWriter := log.Writer()
		log.SetOutput(io.Discard)
		go func() {
			errCh <- daemon.Run(daemonCtx)
		}()
		daemonCancel = cancel
		daemonErrCh = errCh
		bootMessage = "Starting embedded romad..."
		defer log.SetOutput(logWriter)
	}

	input := textinput.New()
	input.Placeholder = "/help"
	input.Prompt = "> "
	input.Focus()
	input.CharLimit = 0
	input.Width = 80

	commandList := list.New(nil, newCommandListDelegate(lightPalette), 0, 0)
	commandList.Title = "Command"
	commandList.SetShowStatusBar(false)
	commandList.SetShowHelp(false)
	commandList.SetFilteringEnabled(false)
	commandList.SetShowPagination(false)
	commandList.DisableQuitKeybindings()

	m := model{
		workingDir:     workingDir,
		client:         client,
		registry:       registry,
		input:          input,
		commandList:    commandList,
		detailViewport: viewport.New(0, 0),
		help:           help.New(),
		daemonCancel:   daemonCancel,
		daemonErrCh:    daemonErrCh,
		helpText: []string{
			"/help",
			"/status",
			"/theme <light|dark>",
			"/agent",
			"/agent list",
			"/agent add <id> <path>",
			"/agent <id>",
			"/with <a,b>",
			"/run <prompt>",
			"/submit <prompt>",
			"/open <job_id>",
			"/cancel [job_id]",
			"/result [session_id]",
			"/refresh",
			"/quit",
		},
		boot:      bootMessage,
		themeName: "light",
	}
	m.refreshTheme()

	// Adjust viewport settings based on terminal environment
	isVTE := runningInVTE()
	m.detailViewport.MouseWheelEnabled = !isVTE // Disable mouse wheel in VTE for stability
	m.detailViewport.MouseWheelDelta = 3
	if len(profiles) > 0 {
		m.selectedAgent = profiles[0].ID
	} else {
		m.appendSystem("No agents configured. Use /agent add <id> <path> to get started.")
	}

	p := tea.NewProgram(m, tuiProgramOptions()...)
	_, runErr := p.Run()
	if daemonCancel != nil {
		daemonCancel()
	}
	select {
	case err, ok := <-daemonErrCh:
		if ok && err != nil && !errors.Is(err, context.Canceled) && runErr == nil {
			runErr = err
		}
	case <-time.After(2 * time.Second):
	}
	return runErr
}

func tuiProgramOptions() []tea.ProgramOption {
	return nil
}

func runningInVTE() bool {
	if strings.TrimSpace(os.Getenv("VTE_VERSION")) != "" {
		return true
	}
	termProgram := strings.ToLower(strings.TrimSpace(os.Getenv("TERM_PROGRAM")))
	switch termProgram {
	case "gnome-terminal", "ptyxis", "kgx":
		return true
	default:
		return false
	}
}
