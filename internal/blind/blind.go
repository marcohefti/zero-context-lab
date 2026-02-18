package blind

import (
	"sort"
	"strings"
)

var defaultHarnessTermsV1 = []string{
	"zcl",
	"zcl feedback",
	"feedback.json",
	"tool.calls.jsonl",
	"funnel",
	"attempt start",
	"attempt finish",
	"suite run",
	"trace",
}

func DefaultHarnessTermsV1() []string {
	out := make([]string, 0, len(defaultHarnessTermsV1))
	out = append(out, defaultHarnessTermsV1...)
	return out
}

func NormalizeTerms(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(strings.ToLower(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func ParseTermsCSV(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	return NormalizeTerms(parts)
}

func FindContaminationTerms(prompt string, terms []string) []string {
	p := strings.ToLower(prompt)
	if strings.TrimSpace(p) == "" {
		return nil
	}
	norm := NormalizeTerms(terms)
	if len(norm) == 0 {
		norm = DefaultHarnessTermsV1()
	}
	found := make([]string, 0, len(norm))
	for _, t := range norm {
		if strings.Contains(p, t) {
			found = append(found, t)
		}
	}
	return found
}
