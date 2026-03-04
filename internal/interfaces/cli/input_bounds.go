package cli

import (
	"encoding/json"

	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

func boundedArgvInputJSON(argv []string) json.RawMessage {
	in := map[string]any{"argv": argv}
	b, err := store.CanonicalJSON(in)
	if err == nil && len(b) <= schema.ToolInputMaxBytesV1 {
		return b
	}
	// Fall back to a minimal stable shape.
	b2, _ := store.CanonicalJSON(map[string]any{"argv": []string{"[TRUNCATED]"}})
	if len(b2) > schema.ToolInputMaxBytesV1 {
		return json.RawMessage(`{}`)
	}
	return b2
}
