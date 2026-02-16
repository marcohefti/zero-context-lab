package redact

import (
	"regexp"
	"strings"
	"sync"

	"github.com/marcohefti/zero-context-lab/internal/config"
)

type Applied struct {
	Names []string
}

var (
	// Keep this minimal but real: redaction must be bounded + default-safe.
	reGitHubTokenClassic = regexp.MustCompile(`\bghp_[A-Za-z0-9]{10,}\b`)
	reGitHubTokenFine    = regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{10,}\b`)
	reGitHubTokenOAuth   = regexp.MustCompile(`\bgho_[A-Za-z0-9]{10,}\b`)
	reOpenAIKey          = regexp.MustCompile(`\bsk-[A-Za-z0-9]{10,}\b`)
	reSlackToken         = regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}\b`)
	reAWSAccessKeyID     = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	reJWT                = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
	reAuthBearerHeader   = regexp.MustCompile(`(?i)\bAuthorization:\s*Bearer\s+[A-Za-z0-9._-]{10,}\b`)
	rePrivateKeyBlock    = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`)
)

type compiledExtraRule struct {
	id          string
	re          *regexp.Regexp
	replacement string
}

var (
	loadOnce   sync.Once
	extraRules []compiledExtraRule
)

func loadExtraRulesOnce() {
	loadOnce.Do(func() {
		rules, err := config.LoadRedactionMerged()
		if err != nil || len(rules) == 0 {
			return
		}
		out := make([]compiledExtraRule, 0, len(rules))
		for _, r := range rules {
			re, err := regexp.Compile(strings.TrimSpace(r.Regex))
			if err != nil {
				continue
			}
			repl := strings.TrimSpace(r.Replacement)
			if repl == "" {
				repl = "[REDACTED:" + strings.ToUpper(r.ID) + "]"
			}
			out = append(out, compiledExtraRule{
				id:          strings.TrimSpace(r.ID),
				re:          re,
				replacement: repl,
			})
		}
		extraRules = out
	})
}

func Text(s string) (string, Applied) {
	loadExtraRulesOnce()

	applied := Applied{}
	out := s

	if reGitHubTokenClassic.MatchString(out) {
		out = reGitHubTokenClassic.ReplaceAllString(out, "[REDACTED:GITHUB_TOKEN]")
		applied.Names = append(applied.Names, "github_token")
	}
	if reGitHubTokenFine.MatchString(out) {
		out = reGitHubTokenFine.ReplaceAllString(out, "[REDACTED:GITHUB_TOKEN]")
		applied.Names = append(applied.Names, "github_token")
	}
	if reGitHubTokenOAuth.MatchString(out) {
		out = reGitHubTokenOAuth.ReplaceAllString(out, "[REDACTED:GITHUB_TOKEN]")
		applied.Names = append(applied.Names, "github_token")
	}
	if reOpenAIKey.MatchString(out) {
		out = reOpenAIKey.ReplaceAllString(out, "[REDACTED:OPENAI_KEY]")
		applied.Names = append(applied.Names, "openai_key")
	}
	if reSlackToken.MatchString(out) {
		out = reSlackToken.ReplaceAllString(out, "[REDACTED:SLACK_TOKEN]")
		applied.Names = append(applied.Names, "slack_token")
	}
	if reAWSAccessKeyID.MatchString(out) {
		out = reAWSAccessKeyID.ReplaceAllString(out, "[REDACTED:AWS_ACCESS_KEY_ID]")
		applied.Names = append(applied.Names, "aws_access_key_id")
	}
	if reJWT.MatchString(out) {
		out = reJWT.ReplaceAllString(out, "[REDACTED:JWT]")
		applied.Names = append(applied.Names, "jwt")
	}
	if reAuthBearerHeader.MatchString(out) {
		out = reAuthBearerHeader.ReplaceAllString(out, "Authorization: Bearer [REDACTED:BEARER_TOKEN]")
		applied.Names = append(applied.Names, "bearer_token")
	}
	if rePrivateKeyBlock.MatchString(out) {
		out = rePrivateKeyBlock.ReplaceAllString(out, "[REDACTED:PRIVATE_KEY]")
		applied.Names = append(applied.Names, "private_key")
	}

	for _, r := range extraRules {
		if r.re.MatchString(out) {
			out = r.re.ReplaceAllString(out, r.replacement)
			applied.Names = append(applied.Names, r.id)
		}
	}

	return out, applied
}
