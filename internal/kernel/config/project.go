package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

const (
	ProjectConfigSchemaV1    = 1
	DefaultProjectConfigPath = "zcl.config.json"
)

// ProjectConfigV1 is the minimal per-repo config created by `zcl init`.
// It is intentionally tiny; richer config merge logic comes later.
type ProjectConfigV1 struct {
	SchemaVersion int                `json:"schemaVersion"`
	OutRoot       string             `json:"outRoot"`
	Redaction     *RedactionConfigV1 `json:"redaction,omitempty"`
	Runtime       RuntimeConfigV1    `json:"runtime,omitempty"`
}

type InitResult struct {
	OK           bool   `json:"ok"`
	ConfigPath   string `json:"configPath"`
	OutRoot      string `json:"outRoot"`
	Created      bool   `json:"created"`
	OutRootReady bool   `json:"outRootReady"`
}

func InitProject(configPath string, outRoot string) (*InitResult, error) {
	configPath, outRoot = normalizeInitProjectArgs(configPath, outRoot)
	if err := ensureProjectOutRootDirs(outRoot); err != nil {
		return nil, err
	}
	created, err := ensureProjectConfig(configPath, outRoot)
	if err != nil {
		return nil, err
	}

	return &InitResult{
		OK:           true,
		ConfigPath:   configPath,
		OutRoot:      outRoot,
		Created:      created,
		OutRootReady: true,
	}, nil
}

func normalizeInitProjectArgs(configPath string, outRoot string) (string, string) {
	if strings.TrimSpace(configPath) == "" {
		configPath = DefaultProjectConfigPath
	}
	if strings.TrimSpace(outRoot) == "" {
		outRoot = ".zcl"
	}
	return configPath, outRoot
}

func ensureProjectOutRootDirs(outRoot string) error {
	if err := os.MkdirAll(filepath.Join(outRoot, "runs"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(outRoot, "tmp"), 0o755); err != nil {
		return err
	}
	return nil
}

func ensureProjectConfig(configPath string, outRoot string) (bool, error) {
	if _, err := os.Stat(configPath); err == nil {
		return false, validateExistingProjectConfig(configPath, outRoot)
	} else if os.IsNotExist(err) {
		return true, createProjectConfig(configPath, outRoot)
	} else if err != nil {
		return false, err
	}
	return false, nil
}

func validateExistingProjectConfig(configPath string, outRoot string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var existing ProjectConfigV1
	if err := json.Unmarshal(raw, &existing); err != nil {
		return err
	}
	if existing.SchemaVersion != ProjectConfigSchemaV1 {
		return fmt.Errorf("existing config has unsupported schemaVersion=%d", existing.SchemaVersion)
	}
	if strings.TrimSpace(existing.OutRoot) == "" {
		return fmt.Errorf("existing config outRoot is empty")
	}
	if existing.OutRoot != outRoot {
		return fmt.Errorf("existing config outRoot=%q does not match requested outRoot=%q", existing.OutRoot, outRoot)
	}
	return nil
}

func createProjectConfig(configPath string, outRoot string) error {
	cfg := ProjectConfigV1{
		SchemaVersion: ProjectConfigSchemaV1,
		OutRoot:       outRoot,
	}
	return store.WriteJSONAtomic(configPath, cfg)
}
