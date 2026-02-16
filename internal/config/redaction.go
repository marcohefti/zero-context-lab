package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/ids"
)

type RedactionRuleV1 struct {
	ID          string `json:"id"`
	Regex       string `json:"regex"`
	Replacement string `json:"replacement,omitempty"`
}

type RedactionConfigV1 struct {
	ExtraRules []RedactionRuleV1 `json:"extraRules,omitempty"`
}

// LoadRedactionMerged loads configured extra redaction rules from:
// - global config (~/.zcl/config.json) when present
// - project config (zcl.config.json) when present
//
// Project rules override global rules when IDs collide.
func LoadRedactionMerged() ([]RedactionRuleV1, error) {
	merged := map[string]RedactionRuleV1{}

	// Global
	if p, err := DefaultGlobalConfigPath(); err == nil {
		if raw, err := os.ReadFile(p); err == nil {
			var g GlobalConfigV1
			if err := json.Unmarshal(raw, &g); err != nil {
				return nil, fmt.Errorf("invalid global config json: %w", err)
			}
			if g.SchemaVersion != 1 {
				return nil, fmt.Errorf("global config unsupported schemaVersion=%d", g.SchemaVersion)
			}
			if g.Redaction != nil {
				for _, r := range g.Redaction.ExtraRules {
					merged[strings.TrimSpace(r.ID)] = r
				}
			}
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	// Project
	if raw, err := os.ReadFile(DefaultProjectConfigPath); err == nil {
		var p ProjectConfigV1
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("invalid project config json: %w", err)
		}
		if p.SchemaVersion != ProjectConfigSchemaV1 {
			return nil, fmt.Errorf("project config unsupported schemaVersion=%d", p.SchemaVersion)
		}
		if p.Redaction != nil {
			for _, r := range p.Redaction.ExtraRules {
				merged[strings.TrimSpace(r.ID)] = r
			}
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	var out []RedactionRuleV1
	for _, r := range merged {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	if err := ValidateRedactionRules(out); err != nil {
		return nil, err
	}
	return out, nil
}

func ValidateRedactionRules(rules []RedactionRuleV1) error {
	if len(rules) == 0 {
		return nil
	}
	if len(rules) > 128 {
		return fmt.Errorf("too many redaction rules (max 128)")
	}
	seen := map[string]bool{}
	for _, r := range rules {
		id := strings.TrimSpace(r.ID)
		if id == "" {
			return fmt.Errorf("redaction rule id is missing")
		}
		if ids.SanitizeComponent(id) != id {
			return fmt.Errorf("redaction rule id %q is not canonical (use lowercase kebab-case)", id)
		}
		if seen[id] {
			return fmt.Errorf("duplicate redaction rule id %q", id)
		}
		seen[id] = true

		re := strings.TrimSpace(r.Regex)
		if re == "" {
			return fmt.Errorf("redaction rule %q regex is missing", id)
		}
		if len(re) > 4096 {
			return fmt.Errorf("redaction rule %q regex too long", id)
		}
		if _, err := regexp.Compile(re); err != nil {
			return fmt.Errorf("redaction rule %q regex invalid: %v", id, err)
		}
		if r.Replacement != "" && len(r.Replacement) > 256 {
			return fmt.Errorf("redaction rule %q replacement too long", id)
		}
	}
	return nil
}

// DefaultGlobalDir returns ~/.zcl (or error).
func DefaultGlobalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zcl"), nil
}
