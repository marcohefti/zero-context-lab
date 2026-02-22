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

type SplitMissionPackResult struct {
	Parsed            suite.ParsedSuite
	OracleByMissionID map[string]string
}

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

// LoadMissionPackSplit loads prompt missions from promptPath and optional oracle files from oraclePath.
// Prompt ordering is deterministic (lexicographic filename order) and mission IDs are derived from filenames.
func LoadMissionPackSplit(promptPath string, oraclePath string, campaignID string) (SplitMissionPackResult, error) {
	dir := strings.TrimSpace(promptPath)
	if dir == "" {
		return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path is required for exam mode")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path: %w", err)
	}
	if !info.IsDir() {
		return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path is not a directory")
	}
	promptFiles, err := listMissionFiles(dir, true)
	if err != nil {
		return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path: %w", err)
	}
	if len(promptFiles) == 0 {
		return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path has no .md missions")
	}

	missions := make([]suite.MissionV1, 0, len(promptFiles))
	seenPromptIDs := map[string]bool{}
	for _, name := range promptFiles {
		full := filepath.Join(dir, name)
		raw, err := os.ReadFile(full)
		if err != nil {
			return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path read %s: %w", name, err)
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		missionID := ids.SanitizeComponent(base)
		if missionID == "" {
			return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path file %q produced empty mission id", name)
		}
		if seenPromptIDs[missionID] {
			return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path duplicate mission id %q", missionID)
		}
		seenPromptIDs[missionID] = true
		prompt := strings.TrimSpace(string(raw))
		if prompt == "" {
			return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path mission %q is empty", name)
		}
		missions = append(missions, suite.MissionV1{
			MissionID: missionID,
			Prompt:    prompt,
		})
	}

	oracleByMissionID := map[string]string{}
	oracleDir := strings.TrimSpace(oraclePath)
	if oracleDir != "" {
		info, err := os.Stat(oracleDir)
		if err != nil {
			return SplitMissionPackResult{}, fmt.Errorf("missionSource.oracleSource.path: %w", err)
		}
		if !info.IsDir() {
			return SplitMissionPackResult{}, fmt.Errorf("missionSource.oracleSource.path is not a directory")
		}
		oracleFiles, err := listMissionFiles(oracleDir, false)
		if err != nil {
			return SplitMissionPackResult{}, fmt.Errorf("missionSource.oracleSource.path: %w", err)
		}
		if len(oracleFiles) == 0 {
			return SplitMissionPackResult{}, fmt.Errorf("missionSource.oracleSource.path has no oracle mission files")
		}
		for _, name := range oracleFiles {
			full := filepath.Join(oracleDir, name)
			base := strings.TrimSuffix(name, filepath.Ext(name))
			missionID := ids.SanitizeComponent(base)
			if missionID == "" {
				return SplitMissionPackResult{}, fmt.Errorf("missionSource.oracleSource.path file %q produced empty mission id", name)
			}
			if _, exists := oracleByMissionID[missionID]; exists {
				return SplitMissionPackResult{}, fmt.Errorf("missionSource.oracleSource.path duplicate mission id %q", missionID)
			}
			oracleByMissionID[missionID] = full
		}
		for _, m := range missions {
			if _, ok := oracleByMissionID[m.MissionID]; !ok {
				return SplitMissionPackResult{}, fmt.Errorf("missionSource.oracleSource.path missing oracle for mission %q", m.MissionID)
			}
		}
		for missionID := range oracleByMissionID {
			if !seenPromptIDs[missionID] {
				return SplitMissionPackResult{}, fmt.Errorf("missionSource.oracleSource.path mission %q has no matching prompt", missionID)
			}
		}
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
	return SplitMissionPackResult{
		Parsed:            suite.ParsedSuite{Suite: sf, CanonicalJSON: sf},
		OracleByMissionID: oracleByMissionID,
	}, nil
}

func listMissionFiles(dir string, markdownOnly bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSpace(e.Name())
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, ".") {
			continue
		}
		if markdownOnly {
			ext := strings.ToLower(filepath.Ext(name))
			if ext != ".md" {
				continue
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
