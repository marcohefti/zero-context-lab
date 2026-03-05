package feedback

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/redact"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/trace"
	"github.com/marcohefti/zero-context-lab/internal/contexts/spec/ports/suite"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
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
	classification, decisionTags, err := validateWriteOpts(opts)
	if err != nil {
		return err
	}

	attemptMeta, err := requireEvidenceForMode(env)
	if err != nil {
		return err
	}

	resultText, resultRaw, applied, err := normalizeFeedbackResult(opts)
	if err != nil {
		return err
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
	attemptMeta, err := readAttemptMetadata(env.OutDirAbs)
	if err != nil {
		return schema.AttemptJSONV1{}, err
	}
	if err := requireNonEmptyTrace(filepath.Join(env.OutDirAbs, "tool.calls.jsonl")); err != nil {
		return schema.AttemptJSONV1{}, err
	}
	return attemptMeta, nil
}

func validateWriteOpts(opts WriteOpts) (string, []string, error) {
	if opts.Result != "" && opts.ResultJSON != "" {
		return "", nil, fmt.Errorf("provide only one of --result or --result-json")
	}
	if opts.Result == "" && opts.ResultJSON == "" {
		return "", nil, fmt.Errorf("missing --result or --result-json")
	}
	classification := strings.TrimSpace(opts.Classification)
	if classification != "" && !schema.IsValidClassificationV1(classification) {
		return "", nil, fmt.Errorf("invalid --classification (expected missing_primitive|naming_ux|output_shape|already_possible_better_way)")
	}
	decisionTags := schema.NormalizeDecisionTagsV1(opts.DecisionTags)
	for _, tag := range decisionTags {
		if !schema.IsValidDecisionTagV1(tag) {
			return "", nil, fmt.Errorf("invalid --decision-tag %q", tag)
		}
	}
	return classification, decisionTags, nil
}

func normalizeFeedbackResult(opts WriteOpts) (string, json.RawMessage, []string, error) {
	if opts.Result != "" {
		red, applied := redact.Text(opts.Result)
		if len([]byte(red)) > schema.FeedbackMaxBytesV1 {
			return "", nil, nil, fmt.Errorf("result exceeds max bytes (%d)", schema.FeedbackMaxBytesV1)
		}
		return red, nil, applied.Names, nil
	}
	var v any
	if err := json.Unmarshal([]byte(opts.ResultJSON), &v); err != nil {
		return "", nil, nil, fmt.Errorf("invalid --result-json: %w", err)
	}
	b, err := store.CanonicalJSON(v)
	if err != nil {
		return "", nil, nil, err
	}
	if len(b) > schema.FeedbackMaxBytesV1 {
		return "", nil, nil, fmt.Errorf("resultJson exceeds max bytes (%d)", schema.FeedbackMaxBytesV1)
	}
	return "", b, nil, nil
}

func readAttemptMetadata(attemptDir string) (schema.AttemptJSONV1, error) {
	// Enforce "funnel-first" semantics: primary evidence must exist before we accept a final outcome.
	rawAttempt, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json"))
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
	return a, nil
}

func requireNonEmptyTrace(tracePath string) error {
	f, err := os.Open(tracePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("tool.calls.jsonl is required before feedback")
		}
		return err
	}
	defer func() { _ = f.Close() }()
	if fileHasNonWhitespace(f) {
		return nil
	}
	return fmt.Errorf("tool.calls.jsonl must be non-empty before feedback")
}

func fileHasNonWhitespace(r *os.File) bool {
	buf := make([]byte, 4096)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			for _, b := range buf[:n] {
				if b != ' ' && b != '\n' && b != '\t' && b != '\r' {
					return true
				}
			}
		}
		if rerr != nil {
			return false
		}
	}
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
