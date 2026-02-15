package feedback

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

const MaxResultBytesV1 = 64 * 1024

type WriteOpts struct {
	OK         bool
	Result     string
	ResultJSON string
}

func Write(now time.Time, env trace.Env, opts WriteOpts) error {
	if opts.Result != "" && opts.ResultJSON != "" {
		return fmt.Errorf("provide only one of --result or --result-json")
	}
	if opts.Result == "" && opts.ResultJSON == "" {
		return fmt.Errorf("missing --result or --result-json")
	}

	var (
		resultText string
		resultRaw  json.RawMessage
		applied    []string
	)

	if opts.Result != "" {
		red, a := redact.Text(opts.Result)
		resultText = red
		applied = a.Names
		if len([]byte(resultText)) > MaxResultBytesV1 {
			return fmt.Errorf("result exceeds max bytes (%d)", MaxResultBytesV1)
		}
	} else {
		var v any
		if err := json.Unmarshal([]byte(opts.ResultJSON), &v); err != nil {
			return fmt.Errorf("invalid --result-json: %w", err)
		}
		b, err := store.CanonicalJSON(v)
		if err != nil {
			return err
		}
		if len(b) > MaxResultBytesV1 {
			return fmt.Errorf("resultJson exceeds max bytes (%d)", MaxResultBytesV1)
		}
		resultRaw = b
	}

	payload := schema.FeedbackJSONV1{
		SchemaVersion:     schema.ArtifactSchemaV1,
		RunID:             env.RunID,
		SuiteID:           env.SuiteID,
		MissionID:         env.MissionID,
		AttemptID:         env.AttemptID,
		OK:                opts.OK,
		Result:            resultText,
		ResultJSON:        resultRaw,
		CreatedAt:         now.UTC().Format(time.RFC3339Nano),
		RedactionsApplied: applied,
	}

	path := filepath.Join(env.OutDirAbs, "feedback.json")
	return store.WriteJSONAtomic(path, payload)
}
