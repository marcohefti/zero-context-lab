package store

import (
	"bytes"
	"encoding/json"
)

// CanonicalJSON encodes v as JSON with stable map-key ordering (per encoding/json),
// and with HTML escaping disabled for artifact legibility.
func CanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Drop trailing newline for embedded RawMessage usage.
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}
