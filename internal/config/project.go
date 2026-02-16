package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/store"
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
}

type InitResult struct {
	OK           bool   `json:"ok"`
	ConfigPath   string `json:"configPath"`
	OutRoot      string `json:"outRoot"`
	Created      bool   `json:"created"`
	OutRootReady bool   `json:"outRootReady"`
}

func InitProject(configPath string, outRoot string) (*InitResult, error) {
	if strings.TrimSpace(configPath) == "" {
		configPath = DefaultProjectConfigPath
	}
	if strings.TrimSpace(outRoot) == "" {
		outRoot = ".zcl"
	}

	if err := os.MkdirAll(filepath.Join(outRoot, "runs"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(outRoot, "tmp"), 0o755); err != nil {
		return nil, err
	}

	created := false
	if _, err := os.Stat(configPath); err == nil {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
		var existing ProjectConfigV1
		if err := json.Unmarshal(raw, &existing); err != nil {
			return nil, err
		}
		if existing.SchemaVersion != ProjectConfigSchemaV1 {
			return nil, fmt.Errorf("existing config has unsupported schemaVersion=%d", existing.SchemaVersion)
		}
		if strings.TrimSpace(existing.OutRoot) == "" {
			return nil, fmt.Errorf("existing config outRoot is empty")
		}
		if existing.OutRoot != outRoot {
			return nil, fmt.Errorf("existing config outRoot=%q does not match requested outRoot=%q", existing.OutRoot, outRoot)
		}
	} else if os.IsNotExist(err) {
		cfg := ProjectConfigV1{
			SchemaVersion: ProjectConfigSchemaV1,
			OutRoot:       outRoot,
		}
		if err := store.WriteJSONAtomic(configPath, cfg); err != nil {
			return nil, err
		}
		created = true
	} else if err != nil {
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
