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
	"github.com/marcohefti/zero-context-lab/internal/suite"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

type WriteOpts struct {
	OK             bool
	Result         string
	ResultJSON     string
	Classification string
	DecisionTags   []string
	// SkipSuiteResultShape skips suite expects.result type/shape enforcement.
	// Use only for synthetic infra-failure feedback written by orchestration.
	SkipSuiteResultShape bool
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
	decisionTags := schema.NormalizeDecisionTagsV1(opts.DecisionTags)
	for _, tag := range decisionTags {
		if !schema.IsValidDecisionTagV1(tag) {
			return fmt.Errorf("invalid --decision-tag %q", tag)
		}
	}

	attemptMeta, err := requireEvidenceForMode(env)
	if err != nil {
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
		SchemaVersion:     schema.FeedbackSchemaV1,
		RunID:             env.RunID,
		SuiteID:           env.SuiteID,
		MissionID:         env.MissionID,
		AttemptID:         env.AttemptID,
		OK:                opts.OK,
		Result:            resultText,
		ResultJSON:        resultRaw,
		Classification:    classification,
		DecisionTags:      decisionTags,
		CreatedAt:         now.UTC().Format(time.RFC3339Nano),
		RedactionsApplied: applied,
	}
	if !opts.SkipSuiteResultShape {
		if err := enforceSuiteResultShape(env, attemptMeta, payload); err != nil {
			return err
		}
	}

	path := filepath.Join(env.OutDirAbs, "feedback.json")
	return store.WriteJSONAtomic(path, payload)
}

func requireEvidenceForMode(env trace.Env) (schema.AttemptJSONV1, error) {
	// Enforce "funnel-first" semantics: primary evidence must exist before we accept a final outcome.
	// This makes it harder to accidentally record a result without funnel-backed actions.
	rawAttempt, err := os.ReadFile(filepath.Join(env.OutDirAbs, "attempt.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return schema.AttemptJSONV1{}, fmt.Errorf("missing attempt.json in attempt directory (need zcl attempt start context)")
		}
		return schema.AttemptJSONV1{}, err
	}
	var a schema.AttemptJSONV1
	if err := json.Unmarshal(rawAttempt, &a); err != nil {
		return schema.AttemptJSONV1{}, fmt.Errorf("invalid attempt.json (cannot determine mode): %w", err)
	}

	tracePath := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return schema.AttemptJSONV1{}, fmt.Errorf("tool.calls.jsonl is required before feedback")
		}
		return schema.AttemptJSONV1{}, err
	}
	defer func() { _ = f.Close() }()

	// Cheap check: at least one non-empty line.
	buf := make([]byte, 4096)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			for _, b := range buf[:n] {
				if b != ' ' && b != '\n' && b != '\t' && b != '\r' {
					return a, nil
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	return schema.AttemptJSONV1{}, fmt.Errorf("tool.calls.jsonl must be non-empty before feedback")
}

func enforceSuiteResultShape(env trace.Env, attemptMeta schema.AttemptJSONV1, payload schema.FeedbackJSONV1) error {
	runDir := filepath.Dir(filepath.Dir(env.OutDirAbs))
	if _, err := os.Stat(filepath.Join(runDir, "run.json")); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	suitePath := filepath.Join(runDir, "suite.json")
	b, err := os.ReadFile(suitePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var sf suite.SuiteFileV1
	if err := json.Unmarshal(b, &sf); err != nil {
		return fmt.Errorf("invalid suite.json while enforcing feedback shape: %w", err)
	}
	m := suite.FindMission(sf, attemptMeta.MissionID)
	if m == nil || m.Expects == nil || m.Expects.Result == nil {
		return nil
	}
	failures := suite.ValidateResultShape(m.Expects.Result, payload)
	if len(failures) == 0 {
		return nil
	}
	msgs := make([]string, 0, len(failures))
	for _, f := range failures {
		msgs = append(msgs, f.Code+": "+f.Message)
	}
	return fmt.Errorf("feedback result shape violates suite expects for mission %q: %s", attemptMeta.MissionID, strings.Join(msgs, "; "))
}
