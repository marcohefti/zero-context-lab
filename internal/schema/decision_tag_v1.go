package schema

import (
	"sort"
	"strings"
)

const (
	DecisionTagSuccess            = "success"
	DecisionTagBlocked            = "blocked"
	DecisionTagTimeout            = "timeout"
	DecisionTagContaminatedPrompt = "contaminated_prompt"
	DecisionTagFunnelBypass       = "funnel_bypass"
	DecisionTagMissingEvidence    = "missing_evidence"
)

func IsValidDecisionTagV1(s string) bool {
	switch strings.TrimSpace(s) {
	case "":
		return false
	case DecisionTagSuccess,
		DecisionTagBlocked,
		DecisionTagTimeout,
		DecisionTagContaminatedPrompt,
		DecisionTagFunnelBypass,
		DecisionTagMissingEvidence:
		return true
	default:
		return false
	}
}

func NormalizeDecisionTagsV1(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(strings.ToLower(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
