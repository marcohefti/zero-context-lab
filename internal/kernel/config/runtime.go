package config

import "strings"

type RuntimeConfigV1 struct {
	StrategyChain []string `json:"strategyChain,omitempty"`
}

func DefaultRuntimeStrategyChain() []string {
	return []string{"codex_app_server"}
}

func NormalizeRuntimeStrategyChain(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		raw = strings.ToLower(strings.TrimSpace(raw))
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ParseRuntimeStrategyCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	return NormalizeRuntimeStrategyChain(parts)
}
