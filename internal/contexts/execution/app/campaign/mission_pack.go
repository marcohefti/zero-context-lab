package campaign

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/contexts/spec/ports/suite"
	"github.com/marcohefti/zero-context-lab/internal/kernel/ids"
)

type SplitMissionPackResult struct {
	Parsed            suite.ParsedSuite
	OracleByMissionID map[string]string
}

// LoadMissionPack loads a directory of .md mission files as a canonical suite.
// Ordering is deterministic (lexicographic filename order).
func LoadMissionPack(path string, campaignID string) (suite.ParsedSuite, error) {
	dir, err := requireDirPath(path, "missionSource.path", "missionSource.path is required for mission-pack mode")
	if err != nil {
		return suite.ParsedSuite{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return suite.ParsedSuite{}, fmt.Errorf("missionSource.path: %w", err)
	}
	names := selectMarkdownFiles(entries)
	if len(names) == 0 {
		return suite.ParsedSuite{}, fmt.Errorf("missionSource.path has no .md missions")
	}
	missions, _, err := buildPromptMissions(dir, names, "missionSource.path")
	if err != nil {
		return suite.ParsedSuite{}, err
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
	dir, err := requireDirPath(promptPath, "missionSource.promptSource.path", "missionSource.promptSource.path is required for exam mode")
	if err != nil {
		return SplitMissionPackResult{}, err
	}
	promptFiles, err := listMissionFiles(dir, true)
	if err != nil {
		return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path: %w", err)
	}
	if len(promptFiles) == 0 {
		return SplitMissionPackResult{}, fmt.Errorf("missionSource.promptSource.path has no .md missions")
	}
	missions, seenPromptIDs, err := buildPromptMissions(dir, promptFiles, "missionSource.promptSource.path")
	if err != nil {
		return SplitMissionPackResult{}, err
	}

	oracleByMissionID := map[string]string{}
	oracleDir := strings.TrimSpace(oraclePath)
	if oracleDir != "" {
		oracleByMissionID, err = loadOracleMissionMap(oracleDir)
		if err != nil {
			return SplitMissionPackResult{}, err
		}
		if err := validateOraclePromptParity(missions, seenPromptIDs, oracleByMissionID); err != nil {
			return SplitMissionPackResult{}, err
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

func requireDirPath(path string, field string, missingErr string) (string, error) {
	dir := strings.TrimSpace(path)
	if dir == "" {
		return "", fmt.Errorf("%s", missingErr)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("%s: %w", field, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", field)
	}
	return dir, nil
}

func selectMarkdownFiles(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSpace(e.Name())
		if strings.ToLower(filepath.Ext(name)) != ".md" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func buildPromptMissions(dir string, files []string, field string) ([]suite.MissionV1, map[string]bool, error) {
	missions := make([]suite.MissionV1, 0, len(files))
	seen := map[string]bool{}
	for _, name := range files {
		prompt, missionID, err := loadPromptMission(dir, name, field)
		if err != nil {
			return nil, nil, err
		}
		if seen[missionID] {
			return nil, nil, fmt.Errorf("%s duplicate mission id %q", field, missionID)
		}
		seen[missionID] = true
		missions = append(missions, suite.MissionV1{
			MissionID: missionID,
			Prompt:    prompt,
		})
	}
	return missions, seen, nil
}

func loadPromptMission(dir string, name string, field string) (string, string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", "", fmt.Errorf("%s read %s: %w", field, name, err)
	}
	missionID := ids.SanitizeComponent(strings.TrimSuffix(name, filepath.Ext(name)))
	if missionID == "" {
		return "", "", fmt.Errorf("%s file %q produced empty mission id", field, name)
	}
	prompt := strings.TrimSpace(string(raw))
	if prompt == "" {
		return "", "", fmt.Errorf("%s mission %q is empty", field, name)
	}
	return prompt, missionID, nil
}

func loadOracleMissionMap(oracleDir string) (map[string]string, error) {
	if _, err := requireDirPath(oracleDir, "missionSource.oracleSource.path", "missionSource.oracleSource.path is required"); err != nil {
		return nil, err
	}
	oracleFiles, err := listMissionFiles(oracleDir, false)
	if err != nil {
		return nil, fmt.Errorf("missionSource.oracleSource.path: %w", err)
	}
	if len(oracleFiles) == 0 {
		return nil, fmt.Errorf("missionSource.oracleSource.path has no oracle mission files")
	}
	oracleByMissionID := make(map[string]string, len(oracleFiles))
	for _, name := range oracleFiles {
		missionID := ids.SanitizeComponent(strings.TrimSuffix(name, filepath.Ext(name)))
		if missionID == "" {
			return nil, fmt.Errorf("missionSource.oracleSource.path file %q produced empty mission id", name)
		}
		if _, exists := oracleByMissionID[missionID]; exists {
			return nil, fmt.Errorf("missionSource.oracleSource.path duplicate mission id %q", missionID)
		}
		oracleByMissionID[missionID] = filepath.Join(oracleDir, name)
	}
	return oracleByMissionID, nil
}

func validateOraclePromptParity(missions []suite.MissionV1, seenPromptIDs map[string]bool, oracleByMissionID map[string]string) error {
	for _, m := range missions {
		if _, ok := oracleByMissionID[m.MissionID]; !ok {
			return fmt.Errorf("missionSource.oracleSource.path missing oracle for mission %q", m.MissionID)
		}
	}
	for missionID := range oracleByMissionID {
		if !seenPromptIDs[missionID] {
			return fmt.Errorf("missionSource.oracleSource.path mission %q has no matching prompt", missionID)
		}
	}
	return nil
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
