package redact

import "regexp"

type Applied struct {
	Names []string
}

var (
	// Keep this minimal but real: redaction must be bounded + default-safe.
	reGitHubToken = regexp.MustCompile(`\bghp_[A-Za-z0-9]{10,}\b`)
	reOpenAIKey   = regexp.MustCompile(`\bsk-[A-Za-z0-9]{10,}\b`)
)

func Text(s string) (string, Applied) {
	applied := Applied{}
	out := s

	if reGitHubToken.MatchString(out) {
		out = reGitHubToken.ReplaceAllString(out, "[REDACTED:GITHUB_TOKEN]")
		applied.Names = append(applied.Names, "github_token")
	}
	if reOpenAIKey.MatchString(out) {
		out = reOpenAIKey.ReplaceAllString(out, "[REDACTED:OPENAI_KEY]")
		applied.Names = append(applied.Names, "openai_key")
	}

	return out, applied
}
