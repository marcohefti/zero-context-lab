package feedback

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

type WriteOpts struct {
	OK             bool
	Result         string
	ResultJSON     string
	Classification string
}

func Write(now time.Time, env trace.Env, opts WriteOpts) error {
	if opts.Result != "" && opts.ResultJSON != "" {
		return fmt.Errorf("provide only one of --result or --result-json")
	}
	if opts.Result == "" && opts.ResultJSON == "" {
		return fmt.Errorf("missing --result or --result-json")
	}

	classification := strings.TrimSpace(opts.Classification)
	if classification != "" && !schema.IsValidClassificationV1(classification) {
		return fmt.Errorf("invalid --classification (expected missing_primitive|naming_ux|output_shape|already_possible_better_way)")
	}

	if err := requireEvidenceForMode(env); err != nil {
		return err
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
		if len([]byte(resultText)) > schema.FeedbackMaxBytesV1 {
			return fmt.Errorf("result exceeds max bytes (%d)", schema.FeedbackMaxBytesV1)
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
		if len(b) > schema.FeedbackMaxBytesV1 {
			return fmt.Errorf("resultJson exceeds max bytes (%d)", schema.FeedbackMaxBytesV1)
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
		Classification:    classification,
		CreatedAt:         now.UTC().Format(time.RFC3339Nano),
		RedactionsApplied: applied,
	}

	path := filepath.Join(env.OutDirAbs, "feedback.json")
	return store.WriteJSONAtomic(path, payload)
}

func requireEvidenceForMode(env trace.Env) error {
	// Enforce "ci" semantics: primary evidence must exist before we accept a final outcome.
	// This makes it harder to accidentally record a result without funnel-backed actions.
	rawAttempt, err := os.ReadFile(filepath.Join(env.OutDirAbs, "attempt.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("missing attempt.json in attempt directory (need zcl attempt start context)")
		}
		return err
	}
	var a schema.AttemptJSONV1
	if err := json.Unmarshal(rawAttempt, &a); err != nil {
		return fmt.Errorf("invalid attempt.json (cannot determine mode): %w", err)
	}
	if a.Mode != "ci" {
		return nil
	}

	tracePath := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("ci mode requires tool.calls.jsonl before feedback")
		}
		return err
	}
	defer func() { _ = f.Close() }()

	// Cheap check: at least one non-empty line.
	buf := make([]byte, 4096)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			for _, b := range buf[:n] {
				if b != ' ' && b != '\n' && b != '\t' && b != '\r' {
					return nil
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	return fmt.Errorf("ci mode requires non-empty tool.calls.jsonl before feedback")
}
