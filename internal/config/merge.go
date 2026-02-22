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

	RuntimeStrategyChain  []string
	RuntimeStrategySource string
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
	Runtime       RuntimeConfigV1    `json:"runtime,omitempty"`
}

func LoadMerged(flagOutRoot string) (Merged, error) {
	// Precedence:
	// 1) CLI flags
	// 2) env vars
	// 3) project config (zcl.config.json)
	// 4) global config (~/.zcl/config.json)
	// 5) defaults
	projectCfg, hasProjectCfg, err := loadProject(DefaultProjectConfigPath)
	if err != nil {
		return Merged{}, err
	}
	globalPath, err := DefaultGlobalConfigPath()
	if err != nil {
		return Merged{}, err
	}
	globalCfg, hasGlobalCfg, err := loadGlobal(globalPath)
	if err != nil {
		return Merged{}, err
	}

	res := Merged{
		OutRoot:               ".zcl",
		Source:                "default",
		RuntimeStrategyChain:  DefaultRuntimeStrategyChain(),
		RuntimeStrategySource: "default",
	}
	if strings.TrimSpace(flagOutRoot) != "" {
		res.OutRoot = flagOutRoot
		res.Source = "flag"
	} else if v := strings.TrimSpace(os.Getenv("ZCL_OUT_ROOT")); v != "" {
		res.OutRoot = v
		res.Source = "env:ZCL_OUT_ROOT"
	} else if hasProjectCfg {
		res.OutRoot = projectCfg.OutRoot
		res.Source = DefaultProjectConfigPath
	} else if hasGlobalCfg && strings.TrimSpace(globalCfg.OutRoot) != "" {
		res.OutRoot = globalCfg.OutRoot
		res.Source = globalPath
	}

	if v := ParseRuntimeStrategyCSV(os.Getenv("ZCL_RUNTIME_STRATEGIES")); len(v) > 0 {
		res.RuntimeStrategyChain = v
		res.RuntimeStrategySource = "env:ZCL_RUNTIME_STRATEGIES"
	} else if hasProjectCfg {
		if chain := NormalizeRuntimeStrategyChain(projectCfg.Runtime.StrategyChain); len(chain) > 0 {
			res.RuntimeStrategyChain = chain
			res.RuntimeStrategySource = DefaultProjectConfigPath
		}
	} else if hasGlobalCfg {
		if chain := NormalizeRuntimeStrategyChain(globalCfg.Runtime.StrategyChain); len(chain) > 0 {
			res.RuntimeStrategyChain = chain
			res.RuntimeStrategySource = globalPath
		}
	}
	return res, nil
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
