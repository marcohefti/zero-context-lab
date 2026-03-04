package runnerid

import "strings"

type ID string

const (
	Codex  ID = "codex"
	Claude ID = "claude"
)

var registered = []ID{
	Codex,
	Claude,
}

func IsSupported(s string) bool {
	for _, r := range registered {
		if string(r) == s {
			return true
		}
	}
	return false
}

func Names() []ID {
	out := make([]ID, len(registered))
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
