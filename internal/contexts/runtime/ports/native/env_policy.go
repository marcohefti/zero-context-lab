package native

import (
	"sort"
	"strings"
)

type EnvPolicy struct {
	AllowedExact    map[string]bool
	AllowedPrefixes []string
	BlockedExact    map[string]bool
	BlockedPrefixes []string
	RedactNameHints []string
}

func DefaultEnvPolicy() EnvPolicy {
	allowed := []string{
		"HOME",
		"LANG",
		"LC_ALL",
		"LC_CTYPE",
		"LC_MESSAGES",
		"LOGNAME",
		"PATH",
		"PWD",
		"SHELL",
		"TERM",
		"TMP",
		"TMPDIR",
		"TEMP",
		"USER",
	}
	blocked := []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AZURE_OPENAI_API_KEY",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"GOOGLE_API_KEY",
		"GEMINI_API_KEY",
		"GITHUB_TOKEN",
		"SLACK_BOT_TOKEN",
		"SSH_AUTH_SOCK",
		"SSH_AGENT_PID",
	}
	policy := EnvPolicy{
		AllowedExact:    map[string]bool{},
		AllowedPrefixes: []string{"ZCL_", "CODEX_"},
		BlockedExact:    map[string]bool{},
		BlockedPrefixes: []string{"SECRET_", "TOKEN_", "PASSWORD_", "CREDENTIAL_"},
		RedactNameHints: []string{"SECRET", "TOKEN", "KEY", "PASSWORD", "CREDENTIAL", "AUTH"},
	}
	for _, key := range allowed {
		policy.AllowedExact[key] = true
	}
	for _, key := range blocked {
		policy.BlockedExact[key] = true
	}
	return policy
}

func (p EnvPolicy) Filter(in map[string]string) (allowed map[string]string, blocked []string) {
	if len(in) == 0 {
		return nil, nil
	}
	allowed = map[string]string{}
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		norm := strings.ToUpper(key)
		if p.isBlocked(norm) {
			blocked = append(blocked, norm)
			continue
		}
		if p.isAllowed(norm) {
			allowed[norm] = v
			continue
		}
		blocked = append(blocked, norm)
	}
	if len(allowed) == 0 {
		allowed = nil
	}
	if len(blocked) == 0 {
		return allowed, nil
	}
	sort.Strings(blocked)
	blocked = dedupeStrings(blocked)
	return allowed, blocked
}

func (p EnvPolicy) RedactForLog(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if p.shouldRedactName(k) {
			out[k] = "[REDACTED]"
			continue
		}
		out[k] = v
	}
	return out
}

func (p EnvPolicy) isAllowed(key string) bool {
	if p.AllowedExact[key] {
		return true
	}
	for _, pref := range p.AllowedPrefixes {
		if strings.HasPrefix(key, strings.ToUpper(strings.TrimSpace(pref))) {
			return true
		}
	}
	return false
}

func (p EnvPolicy) isBlocked(key string) bool {
	if p.BlockedExact[key] {
		return true
	}
	for _, pref := range p.BlockedPrefixes {
		if strings.HasPrefix(key, strings.ToUpper(strings.TrimSpace(pref))) {
			return true
		}
	}
	return false
}

func (p EnvPolicy) shouldRedactName(key string) bool {
	norm := strings.ToUpper(strings.TrimSpace(key))
	if norm == "" {
		return false
	}
	for _, hint := range p.RedactNameHints {
		h := strings.ToUpper(strings.TrimSpace(hint))
		if h != "" && strings.Contains(norm, h) {
			return true
		}
	}
	return false
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
