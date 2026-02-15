package note

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

const MaxMessageBytesV1 = 16 * 1024
const MaxDataBytesV1 = 64 * 1024

type AppendOpts struct {
	Kind     string
	Message  string
	DataJSON string
	Tags     []string
}

func Append(now time.Time, env trace.Env, opts AppendOpts) error {
	kind := strings.TrimSpace(opts.Kind)
	if kind == "" {
		kind = "agent"
	}
	if kind != "agent" && kind != "operator" && kind != "system" {
		return fmt.Errorf("invalid --kind (expected agent|operator|system)")
	}

	msg := strings.TrimSpace(opts.Message)
	if msg == "" && strings.TrimSpace(opts.DataJSON) == "" {
		return fmt.Errorf("missing --message or --data-json")
	}
	if msg != "" && strings.TrimSpace(opts.DataJSON) != "" {
		return fmt.Errorf("provide only one of --message or --data-json")
	}

	var (
		applied []string
		dataRaw json.RawMessage
	)

	if msg != "" {
		red, a := redact.Text(msg)
		msg = red
		applied = a.Names
		if len([]byte(msg)) > MaxMessageBytesV1 {
			return fmt.Errorf("message exceeds max bytes (%d)", MaxMessageBytesV1)
		}
	} else {
		var v any
		if err := json.Unmarshal([]byte(opts.DataJSON), &v); err != nil {
			return fmt.Errorf("invalid --data-json: %w", err)
		}
		b, err := store.CanonicalJSON(v)
		if err != nil {
			return err
		}
		if len(b) > MaxDataBytesV1 {
			return fmt.Errorf("data exceeds max bytes (%d)", MaxDataBytesV1)
		}
		dataRaw = b
	}

	ev := schema.NoteEventV1{
		V:                 schema.TraceSchemaV1,
		TS:                now.UTC().Format(time.RFC3339Nano),
		RunID:             env.RunID,
		SuiteID:           env.SuiteID,
		MissionID:         env.MissionID,
		AttemptID:         env.AttemptID,
		AgentID:           env.AgentID,
		Kind:              kind,
		Message:           msg,
		Data:              dataRaw,
		Tags:              opts.Tags,
		RedactionsApplied: applied,
	}

	path := filepath.Join(env.OutDirAbs, "notes.jsonl")
	return store.AppendJSONL(path, ev)
}
