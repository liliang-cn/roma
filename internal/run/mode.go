package run

import "strings"

const (
	RunModeRelay  = "relay"
	RunModeFanout = "fanout"
	RunModeCaesar = "caesar"
	RunModeSenate = "senate"
)

func normalizedRunMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", RunModeRelay, RunModeFanout:
		return RunModeFanout
	case RunModeCaesar:
		return RunModeCaesar
	case RunModeSenate:
		return RunModeSenate
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func NormalizeMode(mode string) string {
	return normalizedRunMode(mode)
}
