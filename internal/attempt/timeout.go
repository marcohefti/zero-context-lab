package attempt

import (
	"path/filepath"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
)

func EnsureTimeoutAnchor(now time.Time, attemptDir string) (schema.AttemptJSONV1, error) {
	a, err := ReadAttempt(attemptDir)
	if err != nil {
		return schema.AttemptJSONV1{}, err
	}
	if a.TimeoutMs <= 0 {
		return a, nil
	}
	timeoutStart := a.TimeoutStart
	if timeoutStart == "" {
		timeoutStart = schema.TimeoutStartAttemptStartV1
	}
	if timeoutStart != schema.TimeoutStartFirstToolCallV1 {
		return a, nil
	}
	if a.TimeoutStartedAt != "" {
		return a, nil
	}
	a.TimeoutStartedAt = now.UTC().Format(time.RFC3339Nano)
	if err := store.WriteJSONAtomic(attemptPath(attemptDir), a); err != nil {
		return schema.AttemptJSONV1{}, err
	}
	return a, nil
}

func attemptPath(attemptDir string) string {
	return filepath.Join(attemptDir, "attempt.json")
}
