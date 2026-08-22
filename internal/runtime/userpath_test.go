package runtime

import (
	"os"
	"strings"
	"testing"
)

func pathOf(t *testing.T, env []string) string {
	t.Helper()
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			return strings.TrimPrefix(entry, "PATH=")
		}
	}
	t.Fatalf("no PATH in %v", env)
	return ""
}

func sep() string { return string(os.PathListSeparator) }

// withResolver installs a fake login shell for one test and restores the real
// one afterwards, including the resolved value — nothing in the suite should
// ever spawn an actual shell.
func withResolver(t *testing.T, fake func() string) {
	t.Helper()
	restoreResolver := userPathResolver
	userPathMu.RLock()
	restoreValue := userPathVal
	userPathMu.RUnlock()

	userPathResolver = fake
	ResolveUserPathAtStartup()

	t.Cleanup(func() {
		userPathResolver = restoreResolver
		userPathMu.Lock()
		userPathVal = restoreValue
		userPathMu.Unlock()
	})
}

// A process that never resolves a user PATH — every test, and every one-shot
// CLI command — must launch agents with exactly the environment it had.
func TestNoUserPathIsResolvedUnlessAskedFor(t *testing.T) {
	env := []string{"PATH=/usr/bin"}
	if got := withUserPath(env); len(got) != 1 || got[0] != "PATH=/usr/bin" {
		t.Errorf("withUserPath() = %v, want the input unchanged", got)
	}
}

// The whole point: node lives on the login shell's PATH and not on the
// daemon's, and the agent's hooks need it.
func TestMergePathAppendsTheEntriesTheDaemonLacks(t *testing.T) {
	env := mergePath(
		[]string{"HOME=/Users/x", "PATH=" + strings.Join([]string{"/usr/bin", "/bin"}, sep())},
		strings.Join([]string{"/opt/homebrew/bin", "/Users/x/.fnm/current/bin", "/usr/bin"}, sep()),
	)

	want := strings.Join([]string{"/usr/bin", "/bin", "/opt/homebrew/bin", "/Users/x/.fnm/current/bin"}, sep())
	if got := pathOf(t, env); got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
	// Everything else survives untouched.
	if len(env) != 2 || env[0] != "HOME=/Users/x" {
		t.Errorf("other entries disturbed: %v", env)
	}
}

// A duplicate must not shift position: an operator who put a directory first on
// the service's PATH meant it to win.
func TestMergePathKeepsTheDaemonsOrderAndDropsDuplicates(t *testing.T) {
	env := mergePath(
		[]string{"PATH=" + strings.Join([]string{"/first", "/second"}, sep())},
		strings.Join([]string{"/second", "/first", "/third"}, sep()),
	)
	want := strings.Join([]string{"/first", "/second", "/third"}, sep())
	if got := pathOf(t, env); got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
}

func TestMergePathAddsAPathWhenThereWasNone(t *testing.T) {
	env := mergePath([]string{"HOME=/Users/x"}, "/opt/homebrew/bin")
	if got := pathOf(t, env); got != "/opt/homebrew/bin" {
		t.Errorf("PATH = %q, want /opt/homebrew/bin", got)
	}
}

// Empty segments in either side are noise, not entries.
func TestMergePathIgnoresEmptySegments(t *testing.T) {
	env := mergePath([]string{"PATH=" + sep() + "/usr/bin" + sep()}, sep()+"/opt/bin"+sep())
	want := strings.Join([]string{"/usr/bin", "/opt/bin"}, sep())
	if got := pathOf(t, env); got != want {
		t.Errorf("PATH = %q, want %q", got, want)
	}
}

// A shell that cannot be asked (no $SHELL, a profile that errors, a timeout)
// must leave the environment exactly as it was rather than blanking PATH.
func TestWithUserPathLeavesEnvAloneWhenTheShellSaysNothing(t *testing.T) {
	withResolver(t, func() string { return "  " })

	env := []string{"PATH=/usr/bin"}
	got := withUserPath(env)
	if len(got) != 1 || got[0] != "PATH=/usr/bin" {
		t.Errorf("withUserPath() = %v, want the input unchanged", got)
	}
}

// An empty env means "inherit the parent's" — it has to be materialised before
// it can be extended, or setting Env would strip the child bare.
func TestWithUserPathMaterialisesAnInheritedEnvironment(t *testing.T) {
	withResolver(t, func() string { return "/from/login/shell" })

	got := withUserPath(nil)
	if len(got) < 2 {
		t.Fatalf("want the inherited environment, got %v", got)
	}
	if !strings.Contains(pathOf(t, got), "/from/login/shell") {
		t.Errorf("login shell PATH not merged in: %q", pathOf(t, got))
	}
	if !hasEnvKey(got, "HOME") {
		t.Error("the rest of the environment was dropped")
	}
}
