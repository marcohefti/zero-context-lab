package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/blind"
	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"gopkg.in/yaml.v3"
)

type ParsedSuite struct {
	Suite SuiteFileV1
	// CanonicalJSON is the normalized JSON form we snapshot to suite.json for diffability.
	CanonicalJSON any
}

func ParseFile(path string) (ParsedSuite, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ParsedSuite{}, err
	}

	var s SuiteFileV1
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &s); err != nil {
			return ParsedSuite{}, fmt.Errorf("invalid suite yaml: %w", err)
		}
	default:
		if err := json.Unmarshal(raw, &s); err != nil {
			return ParsedSuite{}, fmt.Errorf("invalid suite json: %w", err)
		}
	}

	if s.Version == 0 {
		// Allow omission as v1 for early ergonomics.
		s.Version = 1
	}
	if s.Version != 1 {
		return ParsedSuite{}, fmt.Errorf("unsupported suite version (expected 1)")
	}

	s.SuiteID = ids.SanitizeComponent(strings.TrimSpace(s.SuiteID))
	if s.SuiteID == "" {
		return ParsedSuite{}, fmt.Errorf("missing/invalid suiteId")
	}

	if s.Defaults.Mode != "" && s.Defaults.Mode != "discovery" && s.Defaults.Mode != "ci" {
		return ParsedSuite{}, fmt.Errorf("invalid defaults.mode (expected discovery|ci)")
	}
	if strings.TrimSpace(s.Defaults.FeedbackPolicy) != "" {
		fp := strings.ToLower(strings.TrimSpace(s.Defaults.FeedbackPolicy))
		if !schema.IsValidFeedbackPolicyV1(fp) {
			return ParsedSuite{}, fmt.Errorf("invalid defaults.feedbackPolicy (expected strict|auto_fail)")
		}
		s.Defaults.FeedbackPolicy = fp
	}
	if !schema.IsValidTimeoutStartV1(s.Defaults.TimeoutStart) {
		return ParsedSuite{}, fmt.Errorf("invalid defaults.timeoutStart (expected attempt_start|first_tool_call)")
	}
	if len(s.Defaults.BlindTerms) > 0 {
		s.Defaults.BlindTerms = blind.NormalizeTerms(s.Defaults.BlindTerms)
	}

	if len(s.Missions) == 0 {
		return ParsedSuite{}, fmt.Errorf("suite has no missions")
	}

	seen := map[string]bool{}
	for i := range s.Missions {
		m := &s.Missions[i]
		m.MissionID = ids.SanitizeComponent(strings.TrimSpace(m.MissionID))
		if m.MissionID == "" {
			return ParsedSuite{}, fmt.Errorf("mission missing/invalid missionId")
		}
		if seen[m.MissionID] {
			return ParsedSuite{}, fmt.Errorf("duplicate missionId %q", m.MissionID)
		}
		seen[m.MissionID] = true

		if m.Expects != nil && m.Expects.Result != nil {
			rt := strings.TrimSpace(m.Expects.Result.Type)
			if rt != "string" && rt != "json" {
				return ParsedSuite{}, fmt.Errorf("mission %q: expects.result.type must be string|json", m.MissionID)
			}
			m.Expects.Result.Type = rt
			if len(m.Expects.Result.RequiredJSONPointers) > 0 {
				if rt != "json" {
					return ParsedSuite{}, fmt.Errorf("mission %q: expects.result.requiredJsonPointers requires expects.result.type=json", m.MissionID)
				}
				seenPointers := map[string]bool{}
				normalized := make([]string, 0, len(m.Expects.Result.RequiredJSONPointers))
				for _, ptr := range m.Expects.Result.RequiredJSONPointers {
					ptr = strings.TrimSpace(ptr)
					if ptr == "" {
						return ParsedSuite{}, fmt.Errorf("mission %q: expects.result.requiredJsonPointers cannot contain empty pointers", m.MissionID)
					}
					if !IsValidJSONPointer(ptr) {
						return ParsedSuite{}, fmt.Errorf("mission %q: invalid expects.result.requiredJsonPointers entry %q", m.MissionID, ptr)
					}
					if seenPointers[ptr] {
						continue
					}
					seenPointers[ptr] = true
					normalized = append(normalized, ptr)
				}
				m.Expects.Result.RequiredJSONPointers = normalized
			}
		}
		if m.Expects != nil && m.Expects.Trace != nil {
			tr := m.Expects.Trace
			if tr.MaxToolCallsTotal < 0 || tr.MaxFailuresTotal < 0 || tr.MaxTimeoutsTotal < 0 || tr.MaxRepeatStreak < 0 {
				return ParsedSuite{}, fmt.Errorf("mission %q: expects.trace numeric fields must be >= 0", m.MissionID)
			}
			// Normalize prefixes: trim and drop empties, keep stable order as provided.
			if len(tr.RequireCommandPrefix) > 0 {
				out := make([]string, 0, len(tr.RequireCommandPrefix))
				for _, p := range tr.RequireCommandPrefix {
					p = strings.TrimSpace(p)
					if p == "" {
						continue
					}
					out = append(out, p)
				}
				tr.RequireCommandPrefix = out
			}
		}
		if m.Expects != nil && m.Expects.Semantic != nil {
			sem := m.Expects.Semantic
			req, err := normalizeJSONPointers(sem.RequiredJSONPointers)
			if err != nil {
				return ParsedSuite{}, fmt.Errorf("mission %q: %w", m.MissionID, err)
			}
			sem.RequiredJSONPointers = req
			nonEmpty, err := normalizeJSONPointers(sem.NonEmptyJSONPointers)
			if err != nil {
				return ParsedSuite{}, fmt.Errorf("mission %q: %w", m.MissionID, err)
			}
			sem.NonEmptyJSONPointers = nonEmpty
			sem.PlaceholderValues = normalizeStringList(sem.PlaceholderValues, true)
			sem.RequireToolOps = normalizeStringList(sem.RequireToolOps, false)
			sem.RequireCommandPrefix = normalizeStringList(sem.RequireCommandPrefix, false)
			sem.RequireMCPTool = normalizeStringList(sem.RequireMCPTool, false)
			sem.BoilerplateMCPTools = normalizeStringList(sem.BoilerplateMCPTools, false)
			sem.BoilerplateCommandPrefixes = normalizeStringList(sem.BoilerplateCommandPrefixes, false)
			sem.HookCommand = normalizeCommand(sem.HookCommand)
			if sem.MinMeaningfulFields < 0 {
				return ParsedSuite{}, fmt.Errorf("mission %q: expects.semantic.minMeaningfulFields must be >= 0", m.MissionID)
			}
			if sem.MaxMeaningfulFieldsForBoilerplate < 0 {
				return ParsedSuite{}, fmt.Errorf("mission %q: expects.semantic.maxMeaningfulFieldsForBoilerplate must be >= 0", m.MissionID)
			}
			if sem.HookTimeoutMs < 0 {
				return ParsedSuite{}, fmt.Errorf("mission %q: expects.semantic.hookTimeoutMs must be >= 0", m.MissionID)
			}
		}
	}

	// Canonical representation is the parsed/normalized struct.
	return ParsedSuite{Suite: s, CanonicalJSON: s}, nil
}

func normalizeJSONPointers(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, ptr := range in {
		ptr = strings.TrimSpace(ptr)
		if ptr == "" {
			return nil, fmt.Errorf("expects.semantic pointers cannot contain empty entries")
		}
		if !IsValidJSONPointer(ptr) {
			return nil, fmt.Errorf("invalid expects.semantic pointer %q", ptr)
		}
		if seen[ptr] {
			continue
		}
		seen[ptr] = true
		out = append(out, ptr)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeStringList(in []string, lower bool) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if lower {
			v = strings.ToLower(v)
		}
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeCommand(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, part := range in {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func FindMission(s SuiteFileV1, missionID string) *MissionV1 {
	for i := range s.Missions {
		if s.Missions[i].MissionID == missionID {
			return &s.Missions[i]
		}
	}
	return nil
}
