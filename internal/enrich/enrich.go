package enrich

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/marcohefti/zero-context-lab/internal/enrich/codex"
	"github.com/marcohefti/zero-context-lab/internal/schema"
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

func IsCliError(err error, code string) bool {
	var e *CliError
	return errors.As(err, &e) && e.Code == code
}
