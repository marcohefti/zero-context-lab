package redact

import "testing"

func TestText_RedactsKnownSecrets(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantSubstr string
		applied    string
	}{
		{name: "github_classic", in: "token=ghp_1234567890abcdef", wantSubstr: "[REDACTED:GITHUB_TOKEN]", applied: "github_token"},
		{name: "github_fine", in: "token=github_pat_1234567890_abcdefghijklmnopqrstuvwxyz", wantSubstr: "[REDACTED:GITHUB_TOKEN]", applied: "github_token"},
		{name: "github_oauth", in: "token=gho_1234567890abcdef", wantSubstr: "[REDACTED:GITHUB_TOKEN]", applied: "github_token"},
		{name: "openai", in: "k=sk-1234567890ABCDEF", wantSubstr: "[REDACTED:OPENAI_KEY]", applied: "openai_key"},
		{name: "slack", in: "x=xoxb-1234567890-abcdefghijklmnopqrstuvwxyz", wantSubstr: "[REDACTED:SLACK_TOKEN]", applied: "slack_token"},
		{name: "aws_access_key_id", in: "AKIAAAAAAAAAAAAAAAAA", wantSubstr: "[REDACTED:AWS_ACCESS_KEY_ID]", applied: "aws_access_key_id"},
		{name: "jwt", in: "jwt=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", wantSubstr: "[REDACTED:JWT]", applied: "jwt"},
		{name: "bearer_header", in: "Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456", wantSubstr: "Authorization: Bearer [REDACTED:BEARER_TOKEN]", applied: "bearer_token"},
		{name: "private_key_block", in: "-----BEGIN PRIVATE KEY-----\nABC\n-----END PRIVATE KEY-----", wantSubstr: "[REDACTED:PRIVATE_KEY]", applied: "private_key"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, a := Text(tc.in)
			if out == tc.in {
				t.Fatalf("expected redaction, got unchanged output")
			}
			if !contains(out, tc.wantSubstr) {
				t.Fatalf("expected output to contain %q, got: %q", tc.wantSubstr, out)
			}
			found := false
			for _, n := range a.Names {
				if n == tc.applied {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected applied to include %q, got: %+v", tc.applied, a.Names)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && index(s, sub) >= 0)
}

func index(s, sub string) int {
	// tiny local helper to avoid importing strings in this file
	n := len(sub)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}
