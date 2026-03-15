package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/liliang-cn/roma/internal/api"
	"github.com/liliang-cn/roma/internal/events"
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

func TestEnterAppendsUserPrompt(t *testing.T) {
	t.Parallel()

	input := textinput.New()
	input.Focus()
	commandList := list.New(nil, newCommandListDelegate(lightPalette), 0, 0)
	m := model{
		input:          input,
		commandList:    commandList,
		detailViewport: viewport.New(0, 0),
		help:           help.New(),
		themeName:      "light",
		selectedAgent:  "codex",
	}
	m.input.SetValue("build a feature")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(model)
	if len(got.transcript) == 0 {
		t.Fatal("transcript should contain user prompt")
	}
	if got.transcript[0].kind != transcriptUser || got.transcript[0].text != "build a feature" {
		t.Fatalf("transcript[0] = %#v, want user prompt", got.transcript[0])
	}
	if !got.input.Focused() {
		t.Fatal("input should stay focused after enter")
	}
	if got.input.Value() != "" {
		t.Fatalf("input value = %q, want empty", got.input.Value())
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

func TestCommandMenuHidesAfterCommandArgumentsStart(t *testing.T) {
	t.Parallel()

	input := textinput.New()
	input.Focus()
	commandList := list.New(nil, newCommandListDelegate(lightPalette), 0, 0)
	m := model{
		input:          input,
		commandList:    commandList,
		detailViewport: viewport.New(0, 0),
		help:           help.New(),
		themeName:      "light",
		width:          100,
		height:         30,
	}

	m.input.SetValue("/run ")
	m.syncCommandList()
	if m.commandMenuVisible() {
		t.Fatal("command menu should hide once command arguments start")
	}
}

func TestConsumeInspectAppendsNewOutputOnce(t *testing.T) {
	t.Parallel()

	m := model{}
	resp := &api.QueueInspectResponse{
		Job: queue.Request{ID: "job_1", Status: queue.StatusRunning},
		Events: []events.Record{
			{
				ID:   "evt_1",
				Type: events.TypeRuntimeStdoutCaptured,
				Payload: map[string]any{
					"agent":  "codex",
					"stdout": "planning\n",
				},
			},
		},
	}

	m.consumeInspect(resp)
	m.consumeInspect(resp)
	if len(m.transcript) != 1 {
		t.Fatalf("transcript len = %d, want 1", len(m.transcript))
	}
	if got := m.transcript[0]; got.kind != transcriptOutput || got.label != "codex" || got.text != "planning" {
		t.Fatalf("transcript[0] = %#v, want codex output", got)
	}
}

func TestSyncViewportsPreservesAndClampsScrollOffset(t *testing.T) {
	t.Parallel()

	m := model{
		width:         120,
		height:        12,
		themeName:     "light",
		status:        api.StatusResponse{QueueItems: 1, Sessions: 1, Artifacts: 2, Events: 18},
		queue:         []queue.Request{{ID: "job_1", Status: queue.StatusRunning, StarterAgent: "my-codex"}},
		selectedJobID: "job_1",
		inspect: &api.QueueInspectResponse{
			Job: queue.Request{ID: "job_1", Status: queue.StatusRunning},
		},
		input:          textinput.New(),
		commandList:    list.New(nil, newCommandListDelegate(lightPalette), 0, 0),
		detailViewport: viewport.New(0, 0),
		help:           help.New(),
		transcript: []transcriptEntry{
			{kind: transcriptOutput, label: "codex", text: "line 1"},
			{kind: transcriptOutput, label: "codex", text: "line 2"},
			{kind: transcriptOutput, label: "codex", text: "line 3"},
			{kind: transcriptOutput, label: "codex", text: "line 4"},
			{kind: transcriptOutput, label: "codex", text: "line 5"},
			{kind: transcriptOutput, label: "codex", text: "line 6"},
			{kind: transcriptOutput, label: "codex", text: "line 7"},
			{kind: transcriptOutput, label: "codex", text: "line 8"},
			{kind: transcriptOutput, label: "codex", text: "line 9"},
			{kind: transcriptOutput, label: "codex", text: "line 10"},
		},
	}
	m.refreshTheme()
	m.syncViewports()
	m.detailViewport.SetYOffset(2)
	m.syncViewports()
	if m.detailViewport.YOffset != 2 {
		t.Fatalf("detail viewport offset = %d, want 2", m.detailViewport.YOffset)
	}

	m.transcript = nil
	m.inspect = nil
	m.syncViewports()
	if m.detailViewport.YOffset < 0 {
		t.Fatalf("detail viewport offset = %d, want >= 0", m.detailViewport.YOffset)
	}
}
