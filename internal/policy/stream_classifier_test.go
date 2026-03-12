package policy

import (
	"testing"

	"github.com/liliang-cn/roma/internal/domain"
)

func TestAnalyzeOutputChunkBuildsTransportPatternAndSemanticLayers(t *testing.T) {
	t.Parallel()

	chunk := "$ rm -rf /\n" +
		"approval required before applying patch\n" +
		"json parse error in report\n" +
		"{\"kind\":\"report\",\"schema_version\":\"v1\"}\n" +
		"diff --git a/README.md b/README.md\n" +
		"mcp: playwright ready\n"

	classification := AnalyzeOutputChunk(chunk)
	if len(classification.Transport.Lines) != 6 {
		t.Fatalf("transport lines = %d, want 6", len(classification.Transport.Lines))
	}
	if classification.Transport.Timestamp.IsZero() {
		t.Fatal("transport timestamp is zero")
	}

	assertHasPattern(t, classification.Patterns, PatternDangerousCommand, "dangerous_shell_rm_root", domain.ConfidenceHigh)
	assertHasPattern(t, classification.Patterns, PatternApprovalPhrase, "runtime_approval_requested", domain.ConfidenceHigh)
	assertHasPattern(t, classification.Patterns, PatternParseWarning, "runtime_parse_warning", domain.ConfidenceMedium)
	assertHasPattern(t, classification.Patterns, PatternJSONEnvelope, "json_envelope_detected", domain.ConfidenceMedium)
	assertHasPattern(t, classification.Patterns, PatternDiffHeader, "diff_header_detected", domain.ConfidenceMedium)
	assertHasPattern(t, classification.Patterns, PatternToolCall, "tool_call_detected", domain.ConfidenceMedium)

	assertHasSignal(t, classification.Signals, SignalDangerousCommandDetected, "dangerous_shell_rm_root", domain.ConfidenceHigh)
	assertHasSignal(t, classification.Signals, SignalApprovalRequested, "runtime_approval_requested", domain.ConfidenceHigh)
	assertHasSignal(t, classification.Signals, SignalParseWarning, "runtime_parse_warning", domain.ConfidenceMedium)
}

func TestAnalyzeOutputChunkDedupesSemanticSignalsPerLine(t *testing.T) {
	t.Parallel()

	classification := AnalyzeOutputChunk("approval required\napproval required\n")
	count := 0
	for _, signal := range classification.Signals {
		if signal.Kind == SignalApprovalRequested {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("approval signal count = %d, want 1", count)
	}
}

func assertHasPattern(t *testing.T, patterns []PatternMatch, kind PatternKind, reason string, confidence domain.Confidence) {
	t.Helper()
	for _, pattern := range patterns {
		if pattern.Kind == kind && pattern.Reason == reason && pattern.Confidence == confidence {
			return
		}
	}
	t.Fatalf("patterns = %#v, want %s/%s/%s", patterns, kind, reason, confidence)
}

func assertHasSignal(t *testing.T, signals []StreamSignal, kind StreamSignalKind, reason string, confidence domain.Confidence) {
	t.Helper()
	for _, signal := range signals {
		if signal.Kind == kind && signal.Reason == reason && signal.Confidence == confidence {
			return
		}
	}
	t.Fatalf("signals = %#v, want %s/%s/%s", signals, kind, reason, confidence)
}
