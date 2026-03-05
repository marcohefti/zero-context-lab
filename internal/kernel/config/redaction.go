package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/kernel/ids"
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
	if err := loadGlobalRedactionRules(merged); err != nil {
		return nil, err
	}
	if err := loadProjectRedactionRules(merged); err != nil {
		return nil, err
	}
	out := redactionRulesFromMap(merged)
	if err := ValidateRedactionRules(out); err != nil {
		return nil, err
	}
	return out, nil
}

func loadGlobalRedactionRules(merged map[string]RedactionRuleV1) error {
	p, err := DefaultGlobalConfigPath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var g GlobalConfigV1
	if err := json.Unmarshal(raw, &g); err != nil {
		return fmt.Errorf("invalid global config json: %w", err)
	}
	if g.SchemaVersion != 1 {
		return fmt.Errorf("global config unsupported schemaVersion=%d", g.SchemaVersion)
	}
	if g.Redaction != nil {
		mergeRedactionRules(merged, g.Redaction.ExtraRules)
	}
	return nil
}

func loadProjectRedactionRules(merged map[string]RedactionRuleV1) error {
	if raw, err := os.ReadFile(DefaultProjectConfigPath); err == nil {
		var p ProjectConfigV1
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("invalid project config json: %w", err)
		}
		if p.SchemaVersion != ProjectConfigSchemaV1 {
			return fmt.Errorf("project config unsupported schemaVersion=%d", p.SchemaVersion)
		}
		if p.Redaction != nil {
			mergeRedactionRules(merged, p.Redaction.ExtraRules)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func redactionRulesFromMap(merged map[string]RedactionRuleV1) []RedactionRuleV1 {
	var out []RedactionRuleV1
	for _, r := range merged {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
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
		if err := validateRedactionRule(r, seen); err != nil {
			return err
		}
	}
	return nil
}

func mergeRedactionRules(merged map[string]RedactionRuleV1, rules []RedactionRuleV1) {
	for _, r := range rules {
		merged[strings.TrimSpace(r.ID)] = r
	}
}

func validateRedactionRule(rule RedactionRuleV1, seen map[string]bool) error {
	id, err := validateRedactionRuleID(rule.ID, seen)
	if err != nil {
		return err
	}
	if err := validateRedactionRuleRegex(id, rule.Regex); err != nil {
		return err
	}
	if err := validateRedactionRuleReplacement(id, rule.Replacement); err != nil {
		return err
	}
	return nil
}

func validateRedactionRuleID(rawID string, seen map[string]bool) (string, error) {
	id := strings.TrimSpace(rawID)
	if id == "" {
		return "", fmt.Errorf("redaction rule id is missing")
	}
	if ids.SanitizeComponent(id) != id {
		return "", fmt.Errorf("redaction rule id %q is not canonical (use lowercase kebab-case)", id)
	}
	if seen[id] {
		return "", fmt.Errorf("duplicate redaction rule id %q", id)
	}
	seen[id] = true
	return id, nil
}

func validateRedactionRuleRegex(id string, rawRegex string) error {
	re := strings.TrimSpace(rawRegex)
	if re == "" {
		return fmt.Errorf("redaction rule %q regex is missing", id)
	}
	if len(re) > 4096 {
		return fmt.Errorf("redaction rule %q regex too long", id)
	}
	if _, err := regexp.Compile(re); err != nil {
		return fmt.Errorf("redaction rule %q regex invalid: %v", id, err)
	}
	return nil
}

func validateRedactionRuleReplacement(id string, replacement string) error {
	if replacement != "" && len(replacement) > 256 {
		return fmt.Errorf("redaction rule %q replacement too long", id)
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
