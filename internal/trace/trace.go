package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
)

type Env struct {
	RunID     string
	SuiteID   string
	MissionID string
	AttemptID string
	AgentID   string
	OutDirAbs string
	TmpDirAbs string
}

func EnvFromProcess() (Env, error) {
	e := Env{
		RunID:     os.Getenv("ZCL_RUN_ID"),
		SuiteID:   os.Getenv("ZCL_SUITE_ID"),
		MissionID: os.Getenv("ZCL_MISSION_ID"),
		AttemptID: os.Getenv("ZCL_ATTEMPT_ID"),
		AgentID:   os.Getenv("ZCL_AGENT_ID"),
		OutDirAbs: os.Getenv("ZCL_OUT_DIR"),
		TmpDirAbs: os.Getenv("ZCL_TMP_DIR"),
	}
	if e.OutDirAbs == "" {
		return Env{}, fmt.Errorf("missing ZCL_OUT_DIR")
	}
	if e.RunID == "" || e.SuiteID == "" || e.MissionID == "" || e.AttemptID == "" {
		return Env{}, fmt.Errorf("missing required ZCL_* ids (need ZCL_RUN_ID, ZCL_SUITE_ID, ZCL_MISSION_ID, ZCL_ATTEMPT_ID)")
	}
	return e, nil
}

type ToolCallInput struct {
	Argv []string `json:"argv"`
}

func AppendCLIRunEvent(now time.Time, env Env, argv []string, res ResultForTrace) error {
	redArgv, argvApplied := redactStrings(argv)
	input, inputTruncated, inputWarn, err := boundedToolInputJSON(ToolCallInput{Argv: redArgv}, schema.ToolInputMaxBytesV1)
	if err != nil {
		return err
	}

	var exitCodePtr *int
	if res.SpawnError == "" {
		exitCode := res.ExitCode
		exitCodePtr = &exitCode
	}
	code := res.SpawnError
	if code == "" && res.ExitCode != 0 {
		code = "ZCL_E_TOOL_FAILED"
	}

	outPrev, outApplied := redact.Text(res.OutPreview)
	errPrev, errApplied := redact.Text(res.ErrPreview)

	outPrev, outCapped := capStringBytes(outPrev, schema.PreviewMaxBytesV1)
	errPrev, errCapped := capStringBytes(errPrev, schema.PreviewMaxBytesV1)

	redactions := unionStrings(argvApplied, outApplied.Names, errApplied.Names)
	warnings := append([]schema.TraceWarningV1(nil), inputWarn...)
	enrichmentCapped := false

	ev := schema.TraceEventV1{
		V:         schema.TraceSchemaV1,
		TS:        now.UTC().Format(time.RFC3339Nano),
		RunID:     env.RunID,
		SuiteID:   env.SuiteID,
		MissionID: env.MissionID,
		AttemptID: env.AttemptID,
		AgentID:   env.AgentID,
		Tool:      "cli",
		Op:        "exec",
		Input:     input,
		Result: schema.TraceResultV1{
			OK:         res.SpawnError == "" && res.ExitCode == 0,
			ExitCode:   exitCodePtr,
			DurationMs: res.DurationMs,
			Code:       code,
		},
		IO: schema.TraceIOV1{
			OutBytes:   res.OutBytes,
			ErrBytes:   res.ErrBytes,
			OutPreview: outPrev,
			ErrPreview: errPrev,
		},
		RedactionsApplied: redactions,
		Warnings:          warnings,
		Integrity: &schema.TraceIntegrityV1{
			Truncated: inputTruncated || outCapped || errCapped || res.OutTruncated || res.ErrTruncated,
		},
	}

	if strings.TrimSpace(res.CapturedStdoutPath) != "" || strings.TrimSpace(res.CapturedStderrPath) != "" {
		en := map[string]any{
			"capture": map[string]any{
				"stdoutPath":      strings.TrimSpace(res.CapturedStdoutPath),
				"stderrPath":      strings.TrimSpace(res.CapturedStderrPath),
				"stdoutBytes":     res.CapturedStdoutBytes,
				"stderrBytes":     res.CapturedStderrBytes,
				"stdoutSha256":    strings.TrimSpace(res.CapturedStdoutSHA256),
				"stderrSha256":    strings.TrimSpace(res.CapturedStderrSHA256),
				"stdoutTruncated": res.CapturedStdoutTruncated,
				"stderrTruncated": res.CapturedStderrTruncated,
				"maxBytes":        res.CaptureMaxBytes,
			},
		}
		if b, err := store.CanonicalJSON(en); err == nil {
			if len(b) <= schema.EnrichmentMaxBytesV1 {
				ev.Enrichment = b
			} else {
				enrichmentCapped = true
			}
		}
	}
	if enrichmentCapped {
		ev.Warnings = append(ev.Warnings, schema.TraceWarningV1{Code: "ZCL_W_ENRICHMENT_TRUNCATED", Message: "trace enrichment omitted to fit bounds"})
		if ev.Integrity == nil {
			ev.Integrity = &schema.TraceIntegrityV1{}
		}
		ev.Integrity.Truncated = true
	}

	path := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")
	return store.AppendJSONL(path, ev)
}

type ResultForTrace struct {
	SpawnError string

	ExitCode     int
	DurationMs   int64
	OutBytes     int64
	ErrBytes     int64
	OutPreview   string
	ErrPreview   string
	OutTruncated bool
	ErrTruncated bool

	CapturedStdoutPath      string
	CapturedStderrPath      string
	CapturedStdoutBytes     int64
	CapturedStderrBytes     int64
	CapturedStdoutSHA256    string
	CapturedStderrSHA256    string
	CapturedStdoutTruncated bool
	CapturedStderrTruncated bool
	CaptureMaxBytes         int64
}

func redactStrings(in []string) ([]string, []string) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(in))
	var applied []string
	for _, s := range in {
		red, a := redact.Text(s)
		out = append(out, red)
		applied = append(applied, a.Names...)
	}
	return out, uniqStrings(applied)
}

func boundedToolInputJSON(v any, maxBytes int) (json.RawMessage, bool, []schema.TraceWarningV1, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false, nil, err
	}
	if len(b) <= maxBytes {
		return b, false, nil, nil
	}

	// Special-case the only input we currently emit (argv) and trim args until it fits.
	if tc, ok := v.(ToolCallInput); ok {
		argv := append([]string(nil), tc.Argv...)
		for len(argv) > 1 {
			argv = argv[:len(argv)-1]
			b2, err := json.Marshal(ToolCallInput{Argv: argv})
			if err != nil {
				return nil, false, nil, err
			}
			if len(b2) <= maxBytes {
				return b2, true, []schema.TraceWarningV1{{Code: "ZCL_W_INPUT_TRUNCATED", Message: "tool input truncated to fit bounds"}}, nil
			}
		}
		// Last resort: drop argv content entirely (still a valid shape).
		b3, err := json.Marshal(ToolCallInput{Argv: []string{"[TRUNCATED]"}})
		if err != nil {
			return nil, false, nil, err
		}
		if len(b3) > maxBytes {
			// Should be impossible, but avoid violating the contract.
			return json.RawMessage(`{}`), true, []schema.TraceWarningV1{{Code: "ZCL_W_INPUT_TRUNCATED", Message: "tool input truncated to fit bounds"}}, nil
		}
		return b3, true, []schema.TraceWarningV1{{Code: "ZCL_W_INPUT_TRUNCATED", Message: "tool input truncated to fit bounds"}}, nil
	}

	// Unknown input type: omit it rather than violating bounds.
	return nil, true, []schema.TraceWarningV1{{Code: "ZCL_W_INPUT_TRUNCATED", Message: "tool input omitted to fit bounds"}}, nil
}

func capStringBytes(s string, maxBytes int) (string, bool) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(s) <= maxBytes {
		return s, false
	}
	return s[:maxBytes], true
}

func unionStrings(parts ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		for _, s := range p {
			if s == "" || seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func uniqStrings(in []string) []string {
	return unionStrings(in)
}
