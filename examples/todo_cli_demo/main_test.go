package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAddAndList(t *testing.T) {
	root := t.TempDir()
	todoPath := filepath.Join(root, "todos.json")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	
	// Test adding a todo
	if code := run([]string{"--file", todoPath, "add", "buy milk"}, stdout, stderr); code != 0 {
		t.Fatalf("run(add) code = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "added 1") {
		t.Fatalf("run(add) stdout = %q, want added output", got)
	}

	stdout.Reset()
	stderr.Reset()
	// Test listing todos
	if code := run([]string{"--file", todoPath, "list"}, stdout, stderr); code != 0 {
		t.Fatalf("run(list) code = %d, stderr = %q", code, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "[ ] 1 buy milk") {
		t.Fatalf("run(list) stdout = %q, want todo line", got)
	}
}

func TestRunDoneAndRemove(t *testing.T) {
	root := t.TempDir()
	todoPath := filepath.Join(root, "todos.json")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	
	// Add a todo first
	run([]string{"--file", todoPath, "add", "write tests"}, stdout, stderr)
	stdout.Reset()
	stderr.Reset()

	// Test marking as done
	if code := run([]string{"--file", todoPath, "done", "1"}, stdout, stderr); code != 0 {
		t.Fatalf("run(done) code = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "completed 1") {
		t.Fatalf("run(done) stdout = %q, want completed output", got)
	}

	stdout.Reset()
	stderr.Reset()
	// Check if listed as done
	run([]string{"--file", todoPath, "list"}, stdout, stderr)
	if got := stdout.String(); !strings.Contains(got, "[x] 1 write tests") {
		t.Fatalf("run(list) stdout = %q, want completed todo line", got)
	}

	stdout.Reset()
	stderr.Reset()
	// Test removal
	if code := run([]string{"--file", todoPath, "remove", "1"}, stdout, stderr); code != 0 {
		t.Fatalf("run(remove) code = %d, stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "removed 1") {
		t.Fatalf("run(remove) stdout = %q, want removed output", got)
	}

	stdout.Reset()
	stderr.Reset()
	// Check if empty
	run([]string{"--file", todoPath, "list"}, stdout, stderr)
	if got := stdout.String(); !strings.Contains(got, "no todos") {
		t.Fatalf("run(list-empty) stdout = %q, want no todos", got)
	}
}

func TestRunInvalidUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "unknown command", args: []string{"bogus"}},
		{name: "add no text", args: []string{"add"}},
		{name: "done no id", args: []string{"done"}},
		{name: "remove no id", args: []string{"remove"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code := run(tc.args, io.Discard, io.Discard); code == 0 {
				t.Errorf("run(%v) code = 0, want non-zero", tc.args)
			}
		})
	}
}
