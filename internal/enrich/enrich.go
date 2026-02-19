package enrich

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/marcohefti/zero-context-lab/internal/enrich/claude"
	"github.com/marcohefti/zero-context-lab/internal/enrich/codex"
	"github.com/marcohefti/zero-context-lab/internal/runners"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
)

type CliError struct {
	Code    string
	Message string
}

func (e *CliError) Error() string { return e.Message }

func EnrichCodexAttempt(attemptDir string, rolloutPath string) error {
	attemptBytes, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json"))
	if err != nil {
		return err
	}
	var attempt schema.AttemptJSONV1
	if err := json.Unmarshal(attemptBytes, &attempt); err != nil {
		return err
	}

	if rolloutPath == "" {
		return &CliError{Code: "ZCL_E_USAGE", Message: "missing --rollout for codex enrichment"}
	}
	metrics, err := codex.ParseRolloutJSONL(rolloutPath)
	if err != nil {
		return err
	}
	return codex.WriteAttemptArtifacts(attemptDir, attempt, rolloutPath, metrics)
}

func EnrichClaudeAttempt(attemptDir string, sessionPath string) error {
	attemptBytes, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json"))
	if err != nil {
		return err
	}
	var attempt schema.AttemptJSONV1
	if err := json.Unmarshal(attemptBytes, &attempt); err != nil {
		return err
	}

	if sessionPath == "" {
		return &CliError{Code: "ZCL_E_USAGE", Message: "missing --rollout for claude enrichment"}
	}
	metrics, err := claude.ParseSessionJSONL(sessionPath)
	if err != nil {
		var parseErr *claude.SessionParseError
		if errors.As(err, &parseErr) {
			return &CliError{Code: "ZCL_E_MISSING_EVIDENCE", Message: parseErr.Error()}
		}
		return err
	}

	ref := schema.RunnerRefJSONV1{
		SchemaVersion: schema.ArtifactSchemaV1,
		Runner:        string(runners.ClaudeRunner),
		RunID:         attempt.RunID,
		SuiteID:       attempt.SuiteID,
		MissionID:     attempt.MissionID,
		AttemptID:     attempt.AttemptID,
		AgentID:       attempt.AgentID,
		RolloutPath:   sessionPath,
	}
	met := schema.RunnerMetricsJSONV1{
		SchemaVersion:         schema.ArtifactSchemaV1,
		Runner:                string(runners.ClaudeRunner),
		Model:                 metrics.Model,
		TotalTokens:           metrics.Usage.TotalTokens,
		InputTokens:           metrics.Usage.InputTokens,
		OutputTokens:          metrics.Usage.OutputTokens,
		CachedInputTokens:     metrics.Usage.CachedInputTokens,
		ReasoningOutputTokens: metrics.Usage.ReasoningOutputTokens,
	}
	if err := store.WriteJSONAtomic(filepath.Join(attemptDir, "runner.ref.json"), ref); err != nil {
		return err
	}
	if err := store.WriteJSONAtomic(filepath.Join(attemptDir, "runner.metrics.json"), met); err != nil {
		return err
	}
	return nil
}

func IsCliError(err error, code string) bool {
	var e *CliError
	return errors.As(err, &e) && e.Code == code
}
