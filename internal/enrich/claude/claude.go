package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
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

type SessionEvent struct {
	Model   string         `json:"model"`
	Usage   map[string]any `json:"usage"`
	Message *SessionEvent  `json:"message"`
	Result  *SessionEvent  `json:"result"`
}

type SessionParseError struct {
	Path            string
	Reason          string
	ScannedLines    int
	ParsedLines     int
	ParsedWithModel int
	ParsedWithUsage int
	IgnoredLines    int
}

func (e *SessionParseError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf(
		"cannot parse Claude session %s: %s (scanned=%d parsed=%d parsed_with_model=%d parsed_with_usage=%d ignored=%d)",
		e.Path,
		e.Reason,
		e.ScannedLines,
		e.ParsedLines,
		e.ParsedWithModel,
		e.ParsedWithUsage,
		e.IgnoredLines,
	)
}

func ParseSessionJSONL(path string) (Metrics, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metrics{}, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	stats := SessionParseError{Path: path}
	var (
		m    Metrics
		best TokenUsage
	)

	for sc.Scan() {
		stats.ScannedLines++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var event SessionEvent
		if err := json.Unmarshal(line, &event); err != nil {
			stats.IgnoredLines++
			continue
		}
		stats.ParsedLines++

		if model, ok := findModel(event); ok {
			stats.ParsedWithModel++
			if m.Model == "" {
				m.Model = model
			}
		}
		if usage, ok := findUsage(event); ok {
			stats.ParsedWithUsage++
			best = maxTokenUsage(best, usage)
		}
	}
	if err := sc.Err(); err != nil {
		return Metrics{}, err
	}

	hasModel := m.Model != ""
	hasUsage := best.hasAnyTokenUsage()

	if !hasModel || !hasUsage {
		missing := []string{}
		if !hasModel {
			missing = append(missing, "model")
		}
		if !hasUsage {
			missing = append(missing, "token usage")
		}
		reason := "no usable " + strings.Join(missing, " or ") + " found"
		if stats.ParsedLines == 0 {
			reason = "no parsable JSONL lines with model/usage"
		}
		return Metrics{}, &SessionParseError{
			Path:            path,
			Reason:          reason,
			ScannedLines:    stats.ScannedLines,
			ParsedLines:     stats.ParsedLines,
			ParsedWithModel: stats.ParsedWithModel,
			ParsedWithUsage: stats.ParsedWithUsage,
			IgnoredLines:    stats.IgnoredLines,
		}
	}

	m.Usage = best
	return m, nil
}

func maxTokenUsage(best, candidate TokenUsage) TokenUsage {
	bestScore := scoreUsage(best)
	candScore := scoreUsage(candidate)

	if candScore == 0 {
		return best
	}
	if bestScore == 0 || candScore > bestScore {
		return candidate
	}
	return best
}

func scoreUsage(u TokenUsage) int64 {
	if u.TotalTokens != nil {
		return *u.TotalTokens
	}
	return tokenOrZero(u.InputTokens) + tokenOrZero(u.OutputTokens) + tokenOrZero(u.CachedInputTokens) + tokenOrZero(u.ReasoningOutputTokens)
}

func tokenOrZero(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func findUsage(event SessionEvent) (TokenUsage, bool) {
	usageSources := []map[string]any{}
	if event.Usage != nil {
		usageSources = append(usageSources, event.Usage)
	}
	if event.Message != nil && event.Message.Usage != nil {
		usageSources = append(usageSources, event.Message.Usage)
	}
	if event.Result != nil && event.Result.Usage != nil {
		usageSources = append(usageSources, event.Result.Usage)
	}

	for _, usage := range usageSources {
		parsed := TokenUsage{
			TotalTokens:           numPtr(usage["total_tokens"], usage["totalTokens"]),
			InputTokens:           numPtr(usage["input_tokens"], usage["inputTokens"], usage["input_token_count"], usage["prompt_tokens"]),
			OutputTokens:          numPtr(usage["output_tokens"], usage["outputTokens"], usage["output_token_count"], usage["completion_tokens"]),
			CachedInputTokens:     numPtr(usage["cached_tokens"], usage["cache_read_input_tokens"], usage["cache_creation_input_tokens"], usage["cache_creation"], usage["cachedInputTokens"]),
			ReasoningOutputTokens: numPtr(usage["reasoning_tokens"], usage["reasoningOutputTokens"]),
		}
		if parsed.hasAnyTokenUsage() {
			return parsed, true
		}
	}
	return TokenUsage{}, false
}

func findModel(event SessionEvent) (string, bool) {
	if event.Model != "" {
		return event.Model, true
	}
	if event.Message != nil {
		if model, ok := findModel(*event.Message); ok {
			return model, true
		}
	}
	if event.Result != nil {
		if model, ok := findModel(*event.Result); ok {
			return model, true
		}
	}
	return "", false
}

func (u TokenUsage) hasAnyTokenUsage() bool {
	return u.TotalTokens != nil || u.InputTokens != nil || u.OutputTokens != nil || u.CachedInputTokens != nil || u.ReasoningOutputTokens != nil
}

func numPtr(vals ...any) *int64 {
	for _, v := range vals {
		if v == nil {
			continue
		}
		switch n := v.(type) {
		case float64:
			if n < 0 || math.Trunc(n) != n || n > float64(maxInt64) {
				continue
			}
			x := int64(n)
			return &x
		case int64:
			if n < 0 {
				continue
			}
			return &n
		case int:
			if n < 0 {
				continue
			}
			x := int64(n)
			return &x
		case json.Number:
			x, err := n.Int64()
			if err != nil || x < 0 {
				continue
			}
			return &x
		case string:
			s := strings.TrimSpace(n)
			if s == "" {
				continue
			}
			x, err := strconv.ParseInt(s, 10, 64)
			if err != nil || x < 0 {
				continue
			}
			return &x
		default:
		}
	}
	return nil
}

const maxInt64 = int64(^uint64(0) >> 1)
