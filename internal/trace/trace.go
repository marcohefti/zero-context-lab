package trace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
}

func EnvFromProcess() (Env, error) {
	e := Env{
		RunID:     os.Getenv("ZCL_RUN_ID"),
		SuiteID:   os.Getenv("ZCL_SUITE_ID"),
		MissionID: os.Getenv("ZCL_MISSION_ID"),
		AttemptID: os.Getenv("ZCL_ATTEMPT_ID"),
		AgentID:   os.Getenv("ZCL_AGENT_ID"),
		OutDirAbs: os.Getenv("ZCL_OUT_DIR"),
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
	input, err := json.Marshal(ToolCallInput{Argv: argv})
	if err != nil {
		return err
	}

	var exitCodePtr *int
	if res.SpawnError == "" {
		exitCode := res.ExitCode
		exitCodePtr = &exitCode
	}

	ev := schema.TraceEventV1{
		V:         schema.TraceSchemaV1,
		TS:        now.UTC().Format(time.RFC3339Nano),
		RunID:     env.RunID,
		SuiteID:   env.SuiteID,
		MissionID: env.MissionID,
		AttemptID: env.AttemptID,
		AgentID:   env.AgentID,
		Tool:      argv[0],
		Op:        "exec",
		Input:     input,
		Result: schema.TraceResultV1{
			OK:         res.SpawnError == "" && res.ExitCode == 0,
			ExitCode:   exitCodePtr,
			DurationMs: res.DurationMs,
			Code:       res.SpawnError,
		},
		IO: schema.TraceIOV1{
			OutBytes:   res.OutBytes,
			ErrBytes:   res.ErrBytes,
			OutPreview: res.OutPreview,
			ErrPreview: res.ErrPreview,
		},
		RedactionsApplied: []string{},
		Integrity: &schema.TraceIntegrityV1{
			Truncated: res.OutTruncated || res.ErrTruncated,
		},
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
}
