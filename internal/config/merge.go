package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Merged struct {
	OutRoot string

	// Source is informational for operator UX/debugging.
	Source string
}

func DefaultGlobalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zcl", "config.json"), nil
}

type GlobalConfigV1 struct {
	SchemaVersion int                `json:"schemaVersion"`
	OutRoot       string             `json:"outRoot,omitempty"`
	Redaction     *RedactionConfigV1 `json:"redaction,omitempty"`
}

func LoadMerged(flagOutRoot string) (Merged, error) {
	// Precedence:
	// 1) CLI flags
	// 2) env vars
	// 3) project config (zcl.config.json)
	// 4) global config (~/.zcl/config.json)
	// 5) defaults
	if strings.TrimSpace(flagOutRoot) != "" {
		return Merged{OutRoot: flagOutRoot, Source: "flag"}, nil
	}
	if v := strings.TrimSpace(os.Getenv("ZCL_OUT_ROOT")); v != "" {
		return Merged{OutRoot: v, Source: "env:ZCL_OUT_ROOT"}, nil
	}
	if cfg, ok, err := loadProject(DefaultProjectConfigPath); err != nil {
		return Merged{}, err
	} else if ok {
		return Merged{OutRoot: cfg.OutRoot, Source: DefaultProjectConfigPath}, nil
	}
	globalPath, err := DefaultGlobalConfigPath()
	if err != nil {
		return Merged{}, err
	}
	if g, ok, err := loadGlobal(globalPath); err != nil {
		return Merged{}, err
	} else if ok && strings.TrimSpace(g.OutRoot) != "" {
		return Merged{OutRoot: g.OutRoot, Source: globalPath}, nil
	}
	return Merged{OutRoot: ".zcl", Source: "default"}, nil
}

func loadProject(path string) (ProjectConfigV1, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectConfigV1{}, false, nil
		}
		return ProjectConfigV1{}, false, err
	}
	var cfg ProjectConfigV1
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ProjectConfigV1{}, false, err
	}
	if cfg.SchemaVersion != ProjectConfigSchemaV1 {
		return ProjectConfigV1{}, false, fmt.Errorf("project config unsupported schemaVersion=%d", cfg.SchemaVersion)
	}
	if strings.TrimSpace(cfg.OutRoot) == "" {
		return ProjectConfigV1{}, false, fmt.Errorf("project config outRoot is empty")
	}
	return cfg, true, nil
}

func loadGlobal(path string) (GlobalConfigV1, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GlobalConfigV1{}, false, nil
		}
		return GlobalConfigV1{}, false, err
	}
	var cfg GlobalConfigV1
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return GlobalConfigV1{}, false, err
	}
	if cfg.SchemaVersion != 1 {
		return GlobalConfigV1{}, false, fmt.Errorf("global config unsupported schemaVersion=%d", cfg.SchemaVersion)
	}
	return cfg, true, nil
}
