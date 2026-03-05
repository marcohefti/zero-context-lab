package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/redact"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
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

type NativeRuntimeEvent struct {
	RuntimeID string
	SessionID string
	ThreadID  string
	TurnID    string
	CallID    string
	EventName string
	Payload   json.RawMessage
	Code      string
	Partial   bool
}

func AppendCLIRunEvent(now time.Time, env Env, argv []string, res ResultForTrace) error {
	redArgv, argvApplied := redactStrings(argv)
	input, inputTruncated, inputWarn, err := boundedToolInputJSON(ToolCallInput{Argv: redArgv}, schema.ToolInputMaxBytesV1)
	if err != nil {
		return err
	}

	result := cliTraceResult(res)
	outPrev, errPrev, outCapped, errCapped, previewRedactions := redactedPreviews(res)
	redactions := unionStrings(argvApplied, previewRedactions)
	warnings := append([]schema.TraceWarningV1(nil), inputWarn...)

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
		Result:    result,
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

	if enrichment, enrichmentCapped := cliCaptureEnrichment(res); len(enrichment) > 0 {
		ev.Enrichment = enrichment
	} else if enrichmentCapped {
		ev.Warnings = append(ev.Warnings, schema.TraceWarningV1{Code: "ZCL_W_ENRICHMENT_TRUNCATED", Message: "trace enrichment omitted to fit bounds"})
		if ev.Integrity == nil {
			ev.Integrity = &schema.TraceIntegrityV1{}
		}
		ev.Integrity.Truncated = true
	}

	path := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")
	return store.AppendJSONL(path, ev)
}

func cliTraceResult(res ResultForTrace) schema.TraceResultV1 {
	var exitCodePtr *int
	if res.SpawnError == "" {
		exitCode := res.ExitCode
		exitCodePtr = &exitCode
	}
	code := res.SpawnError
	if code == "" && res.ExitCode != 0 {
		code = "ZCL_E_TOOL_FAILED"
	}
	return schema.TraceResultV1{
		OK:         res.SpawnError == "" && res.ExitCode == 0,
		ExitCode:   exitCodePtr,
		DurationMs: res.DurationMs,
		Code:       code,
	}
}

func redactedPreviews(res ResultForTrace) (string, string, bool, bool, []string) {
	outPrev, outApplied := redact.Text(res.OutPreview)
	errPrev, errApplied := redact.Text(res.ErrPreview)

	outPrev, outCapped := capStringBytes(outPrev, schema.PreviewMaxBytesV1)
	errPrev, errCapped := capStringBytes(errPrev, schema.PreviewMaxBytesV1)
	return outPrev, errPrev, outCapped, errCapped, unionStrings(outApplied.Names, errApplied.Names)
}

func cliCaptureEnrichment(res ResultForTrace) (json.RawMessage, bool) {
	if strings.TrimSpace(res.CapturedStdoutPath) == "" && strings.TrimSpace(res.CapturedStderrPath) == "" {
		return nil, false
	}
	enrichment := map[string]any{
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
	b, err := store.CanonicalJSON(enrichment)
	if err != nil {
		return nil, false
	}
	if len(b) > schema.EnrichmentMaxBytesV1 {
		return nil, true
	}
	return b, false
}

func AppendNativeRuntimeEvent(now time.Time, env Env, evIn NativeRuntimeEvent) error {
	eventName := strings.TrimSpace(evIn.EventName)
	if eventName == "" {
		eventName = "unknown"
	}
	op := strings.ToLower(strings.TrimSpace(eventName))
	op = strings.ReplaceAll(op, "codex/event/", "")
	op = strings.ReplaceAll(op, "/", "_")
	if op == "" {
		op = "unknown"
	}
	payload := map[string]any{
		"runtimeId": strings.TrimSpace(evIn.RuntimeID),
		"sessionId": strings.TrimSpace(evIn.SessionID),
		"threadId":  strings.TrimSpace(evIn.ThreadID),
		"turnId":    strings.TrimSpace(evIn.TurnID),
		"callId":    strings.TrimSpace(evIn.CallID),
		"eventName": eventName,
	}
	var redactions []string
	if len(evIn.Payload) > 0 {
		var decoded any
		if err := json.Unmarshal(evIn.Payload, &decoded); err == nil {
			redacted, applied := redactAny(decoded)
			payload["payload"] = redacted
			redactions = unionStrings(redactions, applied)
		} else {
			red, applied := redact.Text(strings.TrimSpace(string(evIn.Payload)))
			payload["payloadRaw"] = red
			redactions = unionStrings(redactions, applied.Names)
		}
	}
	input, inputTruncated, warnings, err := boundedToolInputJSON(payload, schema.ToolInputMaxBytesV1)
	if err != nil {
		return err
	}
	ok := !evIn.Partial
	code := strings.TrimSpace(evIn.Code)
	if code != "" {
		ok = false
	}
	if evIn.Partial && code == "" {
		code = "ZCL_E_RUNTIME_STREAM_DISCONNECT"
	}
	traceEvent := schema.TraceEventV1{
		V:         schema.TraceSchemaV1,
		TS:        now.UTC().Format(time.RFC3339Nano),
		RunID:     env.RunID,
		SuiteID:   env.SuiteID,
		MissionID: env.MissionID,
		AttemptID: env.AttemptID,
		AgentID:   env.AgentID,
		Tool:      "native",
		Op:        op,
		Input:     input,
		Result: schema.TraceResultV1{
			OK:         ok,
			Code:       code,
			DurationMs: 0,
		},
		IO: schema.TraceIOV1{
			OutBytes: 0,
			ErrBytes: 0,
		},
		Warnings:          warnings,
		RedactionsApplied: redactions,
		Integrity: &schema.TraceIntegrityV1{
			Truncated: inputTruncated || evIn.Partial,
		},
	}
	path := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")
	return store.AppendJSONL(path, traceEvent)
}

func redactAny(v any) (any, []string) {
	switch x := v.(type) {
	case string:
		red, applied := redact.Text(x)
		return red, applied.Names
	case []any:
		out := make([]any, len(x))
		var all []string
		for i, item := range x {
			red, names := redactAny(item)
			out[i] = red
			all = unionStrings(all, names)
		}
		return out, all
	case map[string]any:
		out := make(map[string]any, len(x))
		var all []string
		for k, val := range x {
			red, names := redactAny(val)
			out[k] = red
			all = unionStrings(all, names)
		}
		return out, all
	default:
		return v, nil
	}
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

	if tc, ok := v.(ToolCallInput); ok {
		return boundedToolCallInputJSON(tc, maxBytes)
	}

	return boundedUnknownToolInputJSON(maxBytes)
}

func boundedToolCallInputJSON(tc ToolCallInput, maxBytes int) (json.RawMessage, bool, []schema.TraceWarningV1, error) {
	argv := append([]string(nil), tc.Argv...)
	for len(argv) > 1 {
		argv = argv[:len(argv)-1]
		b, err := json.Marshal(ToolCallInput{Argv: argv})
		if err != nil {
			return nil, false, nil, err
		}
		if len(b) <= maxBytes {
			return b, true, traceInputTruncatedWarnings(false), nil
		}
	}

	lastResort, err := json.Marshal(ToolCallInput{Argv: []string{"[TRUNCATED]"}})
	if err != nil {
		return nil, false, nil, err
	}
	if len(lastResort) > maxBytes {
		return json.RawMessage(`{}`), true, traceInputTruncatedWarnings(false), nil
	}
	return lastResort, true, traceInputTruncatedWarnings(false), nil
}

func boundedUnknownToolInputJSON(maxBytes int) (json.RawMessage, bool, []schema.TraceWarningV1, error) {
	placeholder := map[string]any{
		"truncated": true,
		"reason":    "input_truncated_to_fit_bounds",
	}
	bp, err := json.Marshal(placeholder)
	if err != nil {
		return json.RawMessage(`{}`), true, traceInputTruncatedWarnings(true), nil
	}
	if len(bp) > maxBytes {
		return json.RawMessage(`{}`), true, traceInputTruncatedWarnings(true), nil
	}
	return bp, true, traceInputTruncatedWarnings(true), nil
}

func traceInputTruncatedWarnings(placeholder bool) []schema.TraceWarningV1 {
	if placeholder {
		return []schema.TraceWarningV1{{
			Code:    "ZCL_W_INPUT_TRUNCATED",
			Message: "tool input replaced with placeholder to fit bounds",
		}}
	}
	return []schema.TraceWarningV1{{
		Code:    "ZCL_W_INPUT_TRUNCATED",
		Message: "tool input truncated to fit bounds",
	}}
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
