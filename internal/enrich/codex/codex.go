package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
)

type TokenUsage struct {
	TotalTokens           *int64
	InputTokens           *int64
	OutputTokens          *int64
	CachedInputTokens     *int64
	ReasoningOutputTokens *int64
}

type Metrics struct {
	Model string
	Usage TokenUsage
}

func ParseRolloutJSONL(path string) (Metrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metrics{}, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		m    Metrics
		best TokenUsage
	)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}
		if v, ok := findString(obj, "model"); ok && m.Model == "" {
			m.Model = v
		}
		if usage, ok := findTokenUsage(obj); ok {
			// Prefer the largest totalTokens (these events tend to be cumulative).
			if usage.TotalTokens != nil && (best.TotalTokens == nil || *usage.TotalTokens > *best.TotalTokens) {
				best = usage
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Metrics{}, err
	}
	m.Usage = best
	return m, nil
}

func WriteAttemptArtifacts(attemptDir string, attempt schema.AttemptJSONV1, rolloutPath string, metrics Metrics) error {
	ref := schema.RunnerRefJSONV1{
		SchemaVersion: schema.ArtifactSchemaV1,
		Runner:        "codex",
		RunID:         attempt.RunID,
		SuiteID:       attempt.SuiteID,
		MissionID:     attempt.MissionID,
		AttemptID:     attempt.AttemptID,
		AgentID:       attempt.AgentID,
		RolloutPath:   rolloutPath,
	}

	met := schema.RunnerMetricsJSONV1{
		SchemaVersion:         schema.ArtifactSchemaV1,
		Runner:                "codex",
		Model:                 metrics.Model,
		TotalTokens:           metrics.Usage.TotalTokens,
		InputTokens:           metrics.Usage.InputTokens,
		OutputTokens:          metrics.Usage.OutputTokens,
		CachedInputTokens:     metrics.Usage.CachedInputTokens,
		ReasoningOutputTokens: metrics.Usage.ReasoningOutputTokens,
	}

	if err := store.WriteJSONAtomic(filepath.Join(attemptDir, "runner.ref.json"), ref); err != nil {
		return err
	}
	if err := store.WriteJSONAtomic(filepath.Join(attemptDir, "runner.metrics.json"), met); err != nil {
		return err
	}
	return nil
}

func findString(obj map[string]any, key string) (string, bool) {
	if v, ok := obj[key]; ok {
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	// common nested shapes: meta.model, result.model, params.model
	for _, k := range []string{"meta", "result", "params"} {
		if m, ok := obj[k].(map[string]any); ok {
			if s, ok := m[key].(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func findTokenUsage(obj map[string]any) (TokenUsage, bool) {
	// Look for msg.type == "token_count" and then msg.info.total_token_usage fields.
	var msg map[string]any
	if m, ok := obj["msg"].(map[string]any); ok {
		msg = m
	} else if m, ok := obj["event"].(map[string]any); ok {
		// some shapes embed event under "event"
		if mm, ok := m["msg"].(map[string]any); ok {
			msg = mm
		} else {
			msg = m
		}
	}
	if msg == nil {
		return TokenUsage{}, false
	}
	if t, ok := msg["type"].(string); !ok || t != "token_count" {
		return TokenUsage{}, false
	}

	info, _ := msg["info"].(map[string]any)
	if info == nil {
		return TokenUsage{}, true // token_count without info is valid but unhelpful
	}
	total, _ := info["total_token_usage"].(map[string]any)
	if total == nil {
		return TokenUsage{}, true
	}

	usage := TokenUsage{
		TotalTokens:           numPtr(total["totalTokens"]),
		InputTokens:           numPtr(total["inputTokens"]),
		OutputTokens:          numPtr(total["outputTokens"]),
		CachedInputTokens:     numPtr(total["cachedInputTokens"]),
		ReasoningOutputTokens: numPtr(total["reasoningOutputTokens"]),
	}
	return usage, true
}

func numPtr(v any) *int64 {
	switch n := v.(type) {
	case float64:
		x := int64(n)
		return &x
	case int64:
		x := n
		return &x
	case int:
		x := int64(n)
		return &x
	default:
		return nil
	}
}
