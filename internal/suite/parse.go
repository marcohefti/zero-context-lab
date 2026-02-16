package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/ids"
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
		}
	}

	// Canonical representation is the parsed/normalized struct.
	return ParsedSuite{Suite: s, CanonicalJSON: s}, nil
}

func FindMission(s SuiteFileV1, missionID string) *MissionV1 {
	for i := range s.Missions {
		if s.Missions[i].MissionID == missionID {
			return &s.Missions[i]
		}
	}
	return nil
}
