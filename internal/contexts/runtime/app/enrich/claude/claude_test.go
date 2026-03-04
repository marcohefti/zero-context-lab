package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSessionJSONL_Works(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte(strings.Join([]string{
		`{"message":{"model":"claude-4.1","usage":{"input_tokens":3,"output_tokens":7}}}`,
		`{"type":"assistant","model":"claude-4.1","usage":{"input_tokens":10,"output_tokens":20,"cache_creation_input_tokens":5,"total_tokens":35}}`,
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	metrics, err := ParseSessionJSONL(sessionPath)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if metrics.Model != "claude-4.1" {
		t.Fatalf("expected model claude-4.1, got %q", metrics.Model)
	}
	if metrics.Usage.InputTokens == nil || *metrics.Usage.InputTokens != 10 {
		t.Fatalf("expected input_tokens 10, got %#v", metrics.Usage.InputTokens)
	}
	if metrics.Usage.OutputTokens == nil || *metrics.Usage.OutputTokens != 20 {
		t.Fatalf("expected output_tokens 20, got %#v", metrics.Usage.OutputTokens)
	}
	if metrics.Usage.CachedInputTokens == nil || *metrics.Usage.CachedInputTokens != 5 {
		t.Fatalf("expected cached_input_tokens 5, got %#v", metrics.Usage.CachedInputTokens)
	}
}

func TestParseSessionJSONL_WithMessageUsage(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"type":"assistant","model":"claude-4.1","message":{"usage":{"input_tokens":8,"output_tokens":9}}}`), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	metrics, err := ParseSessionJSONL(sessionPath)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if metrics.Usage.InputTokens == nil || *metrics.Usage.InputTokens != 8 {
		t.Fatalf("expected input_tokens 8, got %#v", metrics.Usage.InputTokens)
	}
}

func TestParseSessionJSONL_StrictShape(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	content := strings.Join([]string{
		`{"message":{"model":"claude-4.1","usage":{"input_tokens":1,"output_tokens":2}}}`,
		`{"result":{"usage":{"total_tokens":12,"input_tokens":3,"output_tokens":4}}}`,
	}, "\n")
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	metrics, err := ParseSessionJSONL(sessionPath)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if metrics.Model != "claude-4.1" {
		t.Fatalf("expected model claude-4.1, got %q", metrics.Model)
	}
	if metrics.Usage.TotalTokens == nil || *metrics.Usage.TotalTokens != 12 {
		t.Fatalf("expected total_tokens 12, got %#v", metrics.Usage.TotalTokens)
	}
}

func TestParseSessionJSONL_StringTokenCounts(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"model":"claude-4.1","usage":{"input_tokens":"11","output_tokens":"13","cache_creation_input_tokens":"17","total_tokens":"41"}}`), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	metrics, err := ParseSessionJSONL(sessionPath)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if metrics.Model != "claude-4.1" {
		t.Fatalf("expected model claude-4.1, got %q", metrics.Model)
	}
	if metrics.Usage.InputTokens == nil || *metrics.Usage.InputTokens != 11 {
		t.Fatalf("expected input_tokens 11, got %#v", metrics.Usage.InputTokens)
	}
	if metrics.Usage.OutputTokens == nil || *metrics.Usage.OutputTokens != 13 {
		t.Fatalf("expected output_tokens 13, got %#v", metrics.Usage.OutputTokens)
	}
	if metrics.Usage.CachedInputTokens == nil || *metrics.Usage.CachedInputTokens != 17 {
		t.Fatalf("expected cached_input_tokens 17, got %#v", metrics.Usage.CachedInputTokens)
	}
	if metrics.Usage.TotalTokens == nil || *metrics.Usage.TotalTokens != 41 {
		t.Fatalf("expected total_tokens 41, got %#v", metrics.Usage.TotalTokens)
	}
}

func TestParseSessionJSONL_RejectsInvalidNumbers(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"model":"claude-4.1","usage":{"input_tokens":-1,"output_tokens":1.5,"total_tokens":"abc"}}`), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	_, err := ParseSessionJSONL(sessionPath)
	if err == nil {
		t.Fatalf("expected parse error")
	}
	var parseErr *SessionParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected SessionParseError, got: %v", err)
	}
	if parseErr.Reason == "" {
		t.Fatalf("expected reason")
	}
}

func TestParseSessionJSONL_RejectsModelWithoutUsage(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"type":"assistant","model":"claude-4.1","content":"hi"}`), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	_, err := ParseSessionJSONL(sessionPath)
	if err == nil {
		t.Fatalf("expected parse error")
	}
	var parseErr *SessionParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected SessionParseError, got: %v", err)
	}
	if !strings.Contains(parseErr.Reason, "token usage") {
		t.Fatalf("expected token usage missing reason, got: %q", parseErr.Reason)
	}
	if parseErr.ParsedWithModel != 1 {
		t.Fatalf("expected one model-bearing event, got %d", parseErr.ParsedWithModel)
	}
}

func TestParseSessionJSONL_NoUsableData(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(sessionPath, []byte(`{"type":"assistant","content":"hi"}`), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	_, err := ParseSessionJSONL(sessionPath)
	if err == nil {
		t.Fatalf("expected parse error")
	}
	var parseErr *SessionParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("expected SessionParseError, got: %v", err)
	}
	if parseErr.ScannedLines != 1 {
		t.Fatalf("expected ScannedLines=1, got %d", parseErr.ScannedLines)
	}
}
