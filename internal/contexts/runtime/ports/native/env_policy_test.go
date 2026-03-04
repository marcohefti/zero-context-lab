package native

import "testing"

func TestEnvPolicyFilter_DefaultPolicy(t *testing.T) {
	p := DefaultEnvPolicy()
	allowed, blocked := p.Filter(map[string]string{
		"PATH":              "/usr/bin",
		"OPENAI_API_KEY":    "secret",
		"ZCL_RUN_ID":        "run",
		"UNLISTED_VARIABLE": "value",
	})
	if allowed["PATH"] == "" {
		t.Fatalf("expected PATH allowed")
	}
	if allowed["ZCL_RUN_ID"] != "run" {
		t.Fatalf("expected ZCL_RUN_ID allowed")
	}
	for _, key := range blocked {
		if key == "OPENAI_API_KEY" {
			goto found
		}
	}
	t.Fatalf("expected OPENAI_API_KEY blocked, got %#v", blocked)
found:
	for _, key := range blocked {
		if key == "UNLISTED_VARIABLE" {
			return
		}
	}
	t.Fatalf("expected UNLISTED_VARIABLE blocked")
}

func TestEnvPolicyRedactForLog(t *testing.T) {
	p := DefaultEnvPolicy()
	red := p.RedactForLog(map[string]string{
		"OPENAI_API_KEY": "secret",
		"PATH":           "/usr/bin",
	})
	if red["OPENAI_API_KEY"] != "[REDACTED]" {
		t.Fatalf("expected redacted key, got %q", red["OPENAI_API_KEY"])
	}
	if red["PATH"] != "/usr/bin" {
		t.Fatalf("expected PATH unchanged")
	}
}
