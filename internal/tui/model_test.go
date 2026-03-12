package tui

import "testing"

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
