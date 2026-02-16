package attempt

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

func ReadAttempt(attemptDir string) (schema.AttemptJSONV1, error) {
	raw, err := os.ReadFile(filepath.Join(attemptDir, "attempt.json"))
	if err != nil {
		return schema.AttemptJSONV1{}, err
	}
	var a schema.AttemptJSONV1
	if err := json.Unmarshal(raw, &a); err != nil {
		return schema.AttemptJSONV1{}, err
	}
	return a, nil
}
