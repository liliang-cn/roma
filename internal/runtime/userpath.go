package runtime

// Agent processes need the human's PATH, not the daemon's.
//
// tagitd is normally started by launchd (brew services) or systemd, and those
// hand a process a minimal PATH — no Homebrew, and none of the per-user version
// managers. The agent CLI itself is found anyway, because agents.json records
// its absolute path. What breaks is everything the CLI shells out to: a Claude
// Code SessionEnd hook that runs `node` fails with
//
//	SessionEnd hook [node ".../session-lifecycle-hook.mjs" SessionEnd] failed:
//	/bin/sh: node: command not found
//
// on every single round, because node lives under fnm in the user's home and
// launchd has never heard of it. The agent is supposed to act as the user; it
// should get the toolchain the user has.
//
// So: ask the login shell what PATH it would give, once per daemon lifetime,
// and append whatever the daemon did not already have.

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// loginShellTimeout bounds the one login-shell invocation. A profile that hangs
// must cost a slow first run, never a stuck daemon.
const loginShellTimeout = 5 * time.Second

var (
	userPathMu  sync.RWMutex
	userPathVal string
	// userPathResolver is swapped in tests; production reads the login shell.
	userPathResolver = loginShellPath
)

// ResolveUserPathAtStartup asks the login shell for its PATH and makes it
// available to every agent launched afterwards. It returns what it found, or ""
// when the shell could not be asked.
//
// Deliberately explicit, and deliberately not lazy. Spawning a login shell
// costs ~150ms, and the one thing it must not do is land inside a launch: the
// first job of the day would pay it, and a concurrency test measuring a batch
// would silently attribute it to scheduling. A daemon can spend 150ms at
// startup; nothing else in the process should be able to trigger a shell.
//
// Call it once, from the daemon's startup path. Everything that never calls it
// — tests, one-shot CLI commands — keeps the environment it already had.
func ResolveUserPathAtStartup() string {
	resolved := userPathResolver()
	userPathMu.Lock()
	userPathVal = resolved
	userPathMu.Unlock()
	return resolved
}

// userPath returns the PATH resolved at startup, or "" when there was none.
func userPath() string {
	userPathMu.RLock()
	defer userPathMu.RUnlock()
	return userPathVal
}

// loginShellPath runs the user's shell as a login shell and reads its PATH.
// Any failure yields "", which leaves the daemon's own PATH untouched.
func loginShellPath() string {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), loginShellTimeout)
	defer cancel()
	// -l so profile files run; printf rather than echo so nothing is added.
	out, err := exec.CommandContext(ctx, shell, "-lc", `printf %s "$PATH"`).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// withUserPath returns env with the login shell's PATH entries appended to
// whatever PATH it already carries. An empty env means "inherit", so it is
// materialised from os.Environ() first — the child cannot inherit and be
// modified at the same time.
func withUserPath(env []string) []string {
	extra := userPath()
	if strings.TrimSpace(extra) == "" {
		return env
	}
	if len(env) == 0 {
		env = os.Environ()
	}
	return mergePath(env, extra)
}

// mergePath appends the entries of extra to env's PATH, keeping the existing
// order and dropping duplicates. The daemon's own entries stay first: an
// operator who put something explicit on the service's PATH meant it.
func mergePath(env []string, extra string) []string {
	const key = "PATH="
	idx := -1
	for i, entry := range env {
		if strings.HasPrefix(entry, key) {
			idx = i
		}
	}

	current := ""
	if idx >= 0 {
		current = strings.TrimPrefix(env[idx], key)
	}

	seen := make(map[string]struct{})
	merged := make([]string, 0, 16)
	for _, dir := range strings.Split(current, string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		merged = append(merged, dir)
	}
	for _, dir := range strings.Split(extra, string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		merged = append(merged, dir)
	}

	value := key + strings.Join(merged, string(os.PathListSeparator))
	out := make([]string, len(env), len(env)+1)
	copy(out, env)
	if idx >= 0 {
		out[idx] = value
		return out
	}
	return append(out, value)
}
