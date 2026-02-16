package cli

import (
	"sort"
	"strings"
)

func formatEnv(env map[string]string, format string) (string, bool) {
	switch strings.TrimSpace(format) {
	case "", "sh":
		return formatEnvSh(env), true
	case "dotenv":
		return formatEnvDotenv(env), true
	default:
		return "", false
	}
}

func formatEnvSh(env map[string]string) string {
	keys := sortedKeys(env)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("export ")
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(shQuote(env[k]))
		b.WriteByte('\n')
	}
	return b.String()
}

func formatEnvDotenv(env map[string]string) string {
	keys := sortedKeys(env)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(dotenvQuote(env[k]))
		b.WriteByte('\n')
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func shQuote(s string) string {
	// Safe single-quote string for POSIX shells:
	// ' -> '\'' (close quote, escape single quote, reopen)
	if s == "" {
		return "''"
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func dotenvQuote(s string) string {
	// Keep unquoted if it's simple. Otherwise, quote with JSON-ish escapes.
	if s == "" {
		return `""`
	}
	simple := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == '/' || c == ':' {
			continue
		}
		simple = false
		break
	}
	if simple {
		return s
	}
	esc := strings.NewReplacer(
		`\\`, `\\\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	).Replace(s)
	return `"` + esc + `"`
}
