package campaign

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/suite"
)

// LoadMissionPack loads a directory of .md mission files as a canonical suite.
// Ordering is deterministic (lexicographic filename order).
func LoadMissionPack(path string, campaignID string) (suite.ParsedSuite, error) {
	dir := strings.TrimSpace(path)
	if dir == "" {
		return suite.ParsedSuite{}, fmt.Errorf("missionSource.path is required for mission-pack mode")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return suite.ParsedSuite{}, fmt.Errorf("missionSource.path: %w", err)
	}
	if !info.IsDir() {
		return suite.ParsedSuite{}, fmt.Errorf("missionSource.path is not a directory")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return suite.ParsedSuite{}, fmt.Errorf("missionSource.path: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSpace(e.Name())
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return suite.ParsedSuite{}, fmt.Errorf("missionSource.path has no .md missions")
	}

	missions := make([]suite.MissionV1, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		full := filepath.Join(dir, name)
		raw, err := os.ReadFile(full)
		if err != nil {
			return suite.ParsedSuite{}, fmt.Errorf("missionSource.path read %s: %w", name, err)
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		missionID := ids.SanitizeComponent(base)
		if missionID == "" {
			return suite.ParsedSuite{}, fmt.Errorf("missionSource.path file %q produced empty mission id", name)
		}
		if seen[missionID] {
			return suite.ParsedSuite{}, fmt.Errorf("missionSource.path duplicate mission id %q", missionID)
		}
		seen[missionID] = true
		prompt := strings.TrimSpace(string(raw))
		if prompt == "" {
			return suite.ParsedSuite{}, fmt.Errorf("missionSource.path mission %q is empty", name)
		}
		missions = append(missions, suite.MissionV1{
			MissionID: missionID,
			Prompt:    prompt,
		})
	}

	suiteID := ids.SanitizeComponent(strings.TrimSpace(campaignID) + "-mission-pack")
	if suiteID == "" {
		suiteID = "mission-pack"
	}
	sf := suite.SuiteFileV1{
		Version:  1,
		SuiteID:  suiteID,
		Missions: missions,
	}
	return suite.ParsedSuite{Suite: sf, CanonicalJSON: sf}, nil
}
