package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAddAndList(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	todoPath := filepath.Join(root, "todos.json")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if code := run([]string{"--file", todoPath, "add", "buy milk"}, stdout, stderr); code != 0 {
		t.Fatalf("run(add) code = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "added 1") {
		t.Fatalf("run(add) stdout = %q, want added output", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--file", todoPath, "list"}, stdout, stderr); code != 0 {
		t.Fatalf("run(list) code = %d, stderr = %q", code, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "[ ] 1 buy milk") {
		t.Fatalf("run(list) stdout = %q, want todo line", got)
	}
}

func TestRunDoneAndRemove(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	todoPath := filepath.Join(root, "todos.json")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if code := run([]string{"--file", todoPath, "add", "write tests"}, stdout, stderr); code != 0 {
		t.Fatalf("run(add) code = %d, stderr = %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--file", todoPath, "done", "1"}, stdout, stderr); code != 0 {
		t.Fatalf("run(done) code = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "completed 1") {
		t.Fatalf("run(done) stdout = %q, want completed output", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--file", todoPath, "list"}, stdout, stderr); code != 0 {
		t.Fatalf("run(list) code = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "[x] 1 write tests") {
		t.Fatalf("run(list) stdout = %q, want completed todo line", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--file", todoPath, "remove", "1"}, stdout, stderr); code != 0 {
		t.Fatalf("run(remove) code = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "removed 1") {
		t.Fatalf("run(remove) stdout = %q, want removed output", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--file", todoPath, "list"}, stdout, stderr); code != 0 {
		t.Fatalf("run(list-empty) code = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "no todos") {
		t.Fatalf("run(list-empty) stdout = %q, want no todos", got)
	}
}

func TestRunUsesTODOFileEnv(t *testing.T) {
	root := t.TempDir()
	todoPath := filepath.Join(root, "env-todos.json")
	t.Setenv("TODO_FILE", todoPath)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if code := run([]string{"add", "from env"}, stdout, stderr); code != 0 {
		t.Fatalf("run(add env) code = %d, stderr = %q", code, stderr.String())
	}
	if _, err := os.Stat(todoPath); err != nil {
		t.Fatalf("Stat(%q) error = %v, want created file", todoPath, err)
	}
}

func TestRunRejectsInvalidUsage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing command", args: nil, want: "usage:"},
		{name: "unknown command", args: []string{"bogus"}, want: "unknown command"},
		{name: "missing add text", args: []string{"add"}, want: "add requires todo text"},
		{name: "missing id", args: []string{"done"}, want: "done requires a numeric id"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			if code := run(tc.args, stdout, stderr); code == 0 {
				t.Fatalf("run(%v) code = 0, want non-zero", tc.args)
			}
			if got := stdout.String() + stderr.String(); !strings.Contains(got, tc.want) {
				t.Fatalf("run(%v) output = %q, want substring %q", tc.args, got, tc.want)
			}
		})
	}
}
