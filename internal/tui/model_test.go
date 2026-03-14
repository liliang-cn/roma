package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/queue"
)

func TestParseCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		command command
		wantErr bool
	}{
		{
			name:  "plain text becomes run",
			input: "build a feature",
			command: command{
				name: "run",
				args: []string{"build a feature"},
				raw:  "build a feature",
			},
		},
		{
			name:  "slash command",
			input: "/with codex,gemini",
			command: command{
				name: "with",
				args: []string{"codex,gemini"},
				raw:  "/with codex,gemini",
			},
		},
		{
			name:    "empty",
			input:   "   ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCommand(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseCommand() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCommand() error = %v", err)
			}
			if got.name != tt.command.name || got.raw != tt.command.raw {
				t.Fatalf("parseCommand() = %#v, want %#v", got, tt.command)
			}
			if len(got.args) != len(tt.command.args) {
				t.Fatalf("arg len = %d, want %d", len(got.args), len(tt.command.args))
			}
			for i := range got.args {
				if got.args[i] != tt.command.args[i] {
					t.Fatalf("arg[%d] = %q, want %q", i, got.args[i], tt.command.args[i])
				}
			}
		})
	}
}

func TestSplitCSV(t *testing.T) {
	t.Parallel()

	got := splitCSV(" codex , gemini ,, copilot ")
	want := []string{"codex", "gemini", "copilot"}
	if len(got) != len(want) {
		t.Fatalf("split len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("split[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunningInVTE(t *testing.T) {
	t.Setenv("VTE_VERSION", "")
	t.Setenv("TERM_PROGRAM", "")
	if runningInVTE() {
		t.Fatal("runningInVTE() = true, want false")
	}

	t.Setenv("VTE_VERSION", "7600")
	if !runningInVTE() {
		t.Fatal("runningInVTE() = false with VTE_VERSION set, want true")
	}

	t.Setenv("VTE_VERSION", "")
	t.Setenv("TERM_PROGRAM", "gnome-terminal")
	if !runningInVTE() {
		t.Fatal("runningInVTE() = false with TERM_PROGRAM=gnome-terminal, want true")
	}
}

func TestFocusInputShortcuts(t *testing.T) {
	t.Parallel()

	input := textinput.New()
	jobList := list.New(nil, newJobListDelegate(lightPalette), 0, 0)
	commandList := list.New(nil, newCommandListDelegate(lightPalette), 0, 0)
	m := model{
		input:          input,
		jobList:        jobList,
		commandList:    commandList,
		detailViewport: viewport.New(0, 0),
		help:           help.New(),
		themeName:      "light",
	}
	if m.input.Focused() {
		t.Fatal("input should start blurred in zero-value model")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	got := next.(model)
	if !got.input.Focused() {
		t.Fatal("input not focused after pressing i")
	}

	got.blurInput()
	next, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	got = next.(model)
	if !got.input.Focused() {
		t.Fatal("input not focused after pressing /")
	}
	if got.input.Value() != "/" {
		t.Fatalf("input value = %q, want /", got.input.Value())
	}
}

func TestCommandPaletteFiltersSlashCommands(t *testing.T) {
	t.Parallel()

	items := filterCommandItems("the")
	if len(items) == 0 {
		t.Fatal("filterCommandItems returned no items for query")
	}

	found := false
	for _, item := range items {
		cmd := item.(commandItem)
		if cmd.insert == "/theme light" || cmd.insert == "/theme dark" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("theme command not found in filtered command items")
	}
}

func TestSyncViewportsPreservesAndClampsScrollOffset(t *testing.T) {
	t.Parallel()

	m := model{
		width:         120,
		height:        30,
		themeName:     "light",
		status:        api.StatusResponse{QueueItems: 1, Sessions: 1, Artifacts: 2, Events: 18},
		queue:         []queue.Request{{ID: "job_1", Status: queue.StatusRunning, StarterAgent: "my-codex"}},
		selectedJobID: "job_1",
		inspect: &api.QueueInspectResponse{
			Job: queue.Request{ID: "job_1", Status: queue.StatusRunning},
		},
		input:          textinput.New(),
		jobList:        list.New(nil, newJobListDelegate(lightPalette), 0, 0),
		commandList:    list.New(nil, newCommandListDelegate(lightPalette), 0, 0),
		detailViewport: viewport.New(0, 0),
		help:           help.New(),
		messages: []string{
			"line 1", "line 2", "line 3", "line 4", "line 5",
			"line 6", "line 7", "line 8", "line 9", "line 10",
		},
	}
	m.refreshTheme()
	m.syncViewports()
	m.detailViewport.SetYOffset(5)
	m.syncViewports()
	if m.detailViewport.YOffset != 5 {
		t.Fatalf("detail viewport offset = %d, want 5", m.detailViewport.YOffset)
	}

	m.messages = nil
	m.inspect = nil
	m.syncViewports()
	if m.detailViewport.YOffset < 0 {
		t.Fatalf("detail viewport offset = %d, want >= 0", m.detailViewport.YOffset)
	}
}
