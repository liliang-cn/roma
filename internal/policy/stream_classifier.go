package policy

import (
	"strings"
	"time"

	"github.com/liliang-cn/roma/internal/domain"
)

type TransportLine struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

type TransportChunk struct {
	Raw       string          `json:"raw"`
	Timestamp time.Time       `json:"timestamp"`
	Lines     []TransportLine `json:"lines"`
}

type PatternKind string

const (
	PatternDangerousCommand PatternKind = "dangerous_command"
	PatternApprovalPhrase   PatternKind = "approval_phrase"
	PatternParseWarning     PatternKind = "parse_warning"
	PatternJSONEnvelope     PatternKind = "json_envelope"
	PatternDiffHeader       PatternKind = "diff_header"
	PatternToolCall         PatternKind = "tool_call"
)

type PatternMatch struct {
	Kind       PatternKind       `json:"kind"`
	Reason     string            `json:"reason"`
	Confidence domain.Confidence `json:"confidence"`
	Line       TransportLine     `json:"line"`
}

type StreamClassification struct {
	Transport TransportChunk `json:"transport"`
	Patterns  []PatternMatch `json:"patterns"`
	Signals   []StreamSignal `json:"signals"`
}

func AnalyzeOutputChunk(chunk string) StreamClassification {
	transport := normalizeTransportChunk(chunk)
	patterns := matchChunkPatterns(transport)
	signals := elevateChunkPatterns(patterns)
	return StreamClassification{
		Transport: transport,
		Patterns:  patterns,
		Signals:   signals,
	}
}

func normalizeTransportChunk(chunk string) TransportChunk {
	rawLines := strings.Split(chunk, "\n")
	lines := make([]TransportLine, 0, len(rawLines))
	for i, raw := range rawLines {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		lines = append(lines, TransportLine{
			Index: i,
			Text:  text,
		})
	}
	return TransportChunk{
		Raw:       chunk,
		Timestamp: time.Now().UTC(),
		Lines:     lines,
	}
}

func matchChunkPatterns(chunk TransportChunk) []PatternMatch {
	out := make([]PatternMatch, 0, len(chunk.Lines))
	for _, line := range chunk.Lines {
		if signal, ok := classifyDangerousOutput(line.Text); ok {
			out = append(out, PatternMatch{
				Kind:       PatternDangerousCommand,
				Reason:     signal.Reason,
				Confidence: signal.Confidence,
				Line:       line,
			})
		}
		if signal, ok := classifyApprovalOutput(line.Text); ok {
			out = append(out, PatternMatch{
				Kind:       PatternApprovalPhrase,
				Reason:     signal.Reason,
				Confidence: signal.Confidence,
				Line:       line,
			})
		}
		if signal, ok := classifyParseWarning(line.Text); ok {
			out = append(out, PatternMatch{
				Kind:       PatternParseWarning,
				Reason:     signal.Reason,
				Confidence: signal.Confidence,
				Line:       line,
			})
		}
		if looksLikeJSONEnvelope(line.Text) {
			out = append(out, PatternMatch{
				Kind:       PatternJSONEnvelope,
				Reason:     "json_envelope_detected",
				Confidence: domain.ConfidenceMedium,
				Line:       line,
			})
		}
		if looksLikeDiffHeader(line.Text) {
			out = append(out, PatternMatch{
				Kind:       PatternDiffHeader,
				Reason:     "diff_header_detected",
				Confidence: domain.ConfidenceMedium,
				Line:       line,
			})
		}
		if looksLikeToolCall(line.Text) {
			out = append(out, PatternMatch{
				Kind:       PatternToolCall,
				Reason:     "tool_call_detected",
				Confidence: domain.ConfidenceMedium,
				Line:       line,
			})
		}
	}
	return out
}

func elevateChunkPatterns(patterns []PatternMatch) []StreamSignal {
	out := make([]StreamSignal, 0, len(patterns))
	seen := make(map[string]struct{}, len(patterns))
	for _, pattern := range patterns {
		var signal StreamSignal
		switch pattern.Kind {
		case PatternDangerousCommand:
			signal = StreamSignal{
				Kind:       SignalDangerousCommandDetected,
				Reason:     pattern.Reason,
				Confidence: pattern.Confidence,
				Text:       pattern.Line.Text,
			}
		case PatternApprovalPhrase:
			signal = StreamSignal{
				Kind:       SignalApprovalRequested,
				Reason:     pattern.Reason,
				Confidence: pattern.Confidence,
				Text:       pattern.Line.Text,
			}
		case PatternParseWarning:
			signal = StreamSignal{
				Kind:       SignalParseWarning,
				Reason:     pattern.Reason,
				Confidence: pattern.Confidence,
				Text:       pattern.Line.Text,
			}
		default:
			continue
		}
		key := string(signal.Kind) + "::" + signal.Reason + "::" + signal.Text
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, signal)
	}
	return out
}

func looksLikeJSONEnvelope(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "{") && strings.Contains(line, `"kind"`) && strings.Contains(line, `"schema_version"`)
}

func looksLikeDiffHeader(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "@@ ")
}

func looksLikeToolCall(line string) bool {
	lowered := strings.ToLower(strings.TrimSpace(line))
	return strings.Contains(lowered, "mcp:") ||
		strings.Contains(lowered, "tool call") ||
		strings.Contains(lowered, "using tool")
}
