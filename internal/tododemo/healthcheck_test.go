package tododemo

import "testing"

func TestHealthMessage(t *testing.T) {
	t.Parallel()

	const want = "todo-webapp-ok"

	if got := HealthMessage(); got != want {
		t.Fatalf("HealthMessage() = %q, want %q", got, want)
	}
}
