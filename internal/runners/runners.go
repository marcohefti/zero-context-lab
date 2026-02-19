package runners

import "strings"

type Runner string

const (
	CodexRunner  Runner = "codex"
	ClaudeRunner Runner = "claude"
)

var registered = []Runner{
	CodexRunner,
	ClaudeRunner,
}

func IsSupported(s string) bool {
	for _, r := range registered {
		if string(r) == s {
			return true
		}
	}
	return false
}

func Names() []Runner {
	out := make([]Runner, len(registered))
	copy(out, registered)
	return out
}

func CLIUsageValues() string {
	names := make([]string, 0, len(registered))
	for _, r := range registered {
		names = append(names, string(r))
	}
	return strings.Join(names, "|")
}
