package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/kernel/blind"
	"github.com/marcohefti/zero-context-lab/internal/kernel/ids"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
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
	s, err := decodeSuiteFile(path, raw)
	if err != nil {
		return ParsedSuite{}, err
	}
	if err := normalizeSuiteFile(&s); err != nil {
		return ParsedSuite{}, err
	}
	return ParsedSuite{Suite: s, CanonicalJSON: s}, nil
}

func decodeSuiteFile(path string, raw []byte) (SuiteFileV1, error) {
	var s SuiteFileV1
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &s); err != nil {
			return SuiteFileV1{}, fmt.Errorf("invalid suite yaml: %w", err)
		}
	default:
		if err := json.Unmarshal(raw, &s); err != nil {
			return SuiteFileV1{}, fmt.Errorf("invalid suite json: %w", err)
		}
	}
	return s, nil
}

func normalizeSuiteFile(s *SuiteFileV1) error {
	if err := normalizeSuiteHeader(s); err != nil {
		return err
	}
	return normalizeSuiteMissions(s)
}

func normalizeSuiteHeader(s *SuiteFileV1) error {
	if s.Version == 0 {
		// Allow omission as v1 for early ergonomics.
		s.Version = 1
	}
	if s.Version != 1 {
		return fmt.Errorf("unsupported suite version (expected 1)")
	}

	s.SuiteID = ids.SanitizeComponent(strings.TrimSpace(s.SuiteID))
	if s.SuiteID == "" {
		return fmt.Errorf("missing/invalid suiteId")
	}

	if s.Defaults.Mode != "" && s.Defaults.Mode != "discovery" && s.Defaults.Mode != "ci" {
		return fmt.Errorf("invalid defaults.mode (expected discovery|ci)")
	}
	if strings.TrimSpace(s.Defaults.FeedbackPolicy) != "" {
		fp := strings.ToLower(strings.TrimSpace(s.Defaults.FeedbackPolicy))
		if !schema.IsValidFeedbackPolicyV1(fp) {
			return fmt.Errorf("invalid defaults.feedbackPolicy (expected strict|auto_fail)")
		}
		s.Defaults.FeedbackPolicy = fp
	}
	if !schema.IsValidTimeoutStartV1(s.Defaults.TimeoutStart) {
		return fmt.Errorf("invalid defaults.timeoutStart (expected attempt_start|first_tool_call)")
	}
	if len(s.Defaults.BlindTerms) > 0 {
		s.Defaults.BlindTerms = blind.NormalizeTerms(s.Defaults.BlindTerms)
	}
	return nil
}

func normalizeSuiteMissions(s *SuiteFileV1) error {
	if len(s.Missions) == 0 {
		return fmt.Errorf("suite has no missions")
	}
	seen := map[string]bool{}
	for i := range s.Missions {
		m := &s.Missions[i]
		if err := normalizeMissionID(m, seen); err != nil {
			return err
		}
		if err := normalizeMissionExpects(m); err != nil {
			return err
		}
	}
	return nil
}

func normalizeMissionID(m *MissionV1, seen map[string]bool) error {
	m.MissionID = ids.SanitizeComponent(strings.TrimSpace(m.MissionID))
	if m.MissionID == "" {
		return fmt.Errorf("mission missing/invalid missionId")
	}
	if seen[m.MissionID] {
		return fmt.Errorf("duplicate missionId %q", m.MissionID)
	}
	seen[m.MissionID] = true
	return nil
}

func normalizeMissionExpects(m *MissionV1) error {
	if m.Expects == nil {
		return nil
	}
	if err := normalizeMissionResultExpects(m); err != nil {
		return err
	}
	if err := normalizeMissionTraceExpects(m); err != nil {
		return err
	}
	return normalizeMissionSemanticExpects(m)
}

func normalizeMissionResultExpects(m *MissionV1) error {
	if m.Expects.Result == nil {
		return nil
	}
	rt := strings.TrimSpace(m.Expects.Result.Type)
	if rt != "string" && rt != "json" {
		return fmt.Errorf("mission %q: expects.result.type must be string|json", m.MissionID)
	}
	m.Expects.Result.Type = rt
	if len(m.Expects.Result.RequiredJSONPointers) == 0 {
		return nil
	}
	if rt != "json" {
		return fmt.Errorf("mission %q: expects.result.requiredJsonPointers requires expects.result.type=json", m.MissionID)
	}
	normalized, err := normalizeMissionRequiredPointers(m.MissionID, m.Expects.Result.RequiredJSONPointers)
	if err != nil {
		return err
	}
	m.Expects.Result.RequiredJSONPointers = normalized
	return nil
}

func normalizeMissionRequiredPointers(missionID string, pointers []string) ([]string, error) {
	seenPointers := map[string]bool{}
	normalized := make([]string, 0, len(pointers))
	for _, ptr := range pointers {
		ptr = strings.TrimSpace(ptr)
		if ptr == "" {
			return nil, fmt.Errorf("mission %q: expects.result.requiredJsonPointers cannot contain empty pointers", missionID)
		}
		if !IsValidJSONPointer(ptr) {
			return nil, fmt.Errorf("mission %q: invalid expects.result.requiredJsonPointers entry %q", missionID, ptr)
		}
		if seenPointers[ptr] {
			continue
		}
		seenPointers[ptr] = true
		normalized = append(normalized, ptr)
	}
	return normalized, nil
}

func normalizeMissionTraceExpects(m *MissionV1) error {
	if m.Expects.Trace == nil {
		return nil
	}
	tr := m.Expects.Trace
	if tr.MaxToolCallsTotal < 0 || tr.MaxFailuresTotal < 0 || tr.MaxTimeoutsTotal < 0 || tr.MaxRepeatStreak < 0 {
		return fmt.Errorf("mission %q: expects.trace numeric fields must be >= 0", m.MissionID)
	}
	tr.RequireCommandPrefix = normalizeCommandPrefixList(tr.RequireCommandPrefix)
	return nil
}

func normalizeCommandPrefixList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func normalizeMissionSemanticExpects(m *MissionV1) error {
	if m.Expects.Semantic == nil {
		return nil
	}
	sem := m.Expects.Semantic
	req, err := normalizeJSONPointers(sem.RequiredJSONPointers)
	if err != nil {
		return fmt.Errorf("mission %q: %w", m.MissionID, err)
	}
	sem.RequiredJSONPointers = req
	nonEmpty, err := normalizeJSONPointers(sem.NonEmptyJSONPointers)
	if err != nil {
		return fmt.Errorf("mission %q: %w", m.MissionID, err)
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
		return fmt.Errorf("mission %q: expects.semantic.minMeaningfulFields must be >= 0", m.MissionID)
	}
	if sem.MaxMeaningfulFieldsForBoilerplate < 0 {
		return fmt.Errorf("mission %q: expects.semantic.maxMeaningfulFieldsForBoilerplate must be >= 0", m.MissionID)
	}
	if sem.HookTimeoutMs < 0 {
		return fmt.Errorf("mission %q: expects.semantic.hookTimeoutMs must be >= 0", m.MissionID)
	}
	return nil
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
