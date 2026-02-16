package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/validate"
)

func TestValidate_JSONStableOnFailureAndExitCodes(t *testing.T) {
	// Usage-style failure: directory exists but is not an attemptDir or runDir.
	{
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer
		r := Runner{
			Version: "0.0.0-dev",
			Now:     func() time.Time { return time.Date(2026, 2, 16, 18, 0, 0, 0, time.UTC) },
			Stdout:  &stdout,
			Stderr:  &stderr,
		}

		code := r.Run([]string{"validate", "--json", dir})
		if code != 2 {
			t.Fatalf("expected exit 2 for usage failure, got %d (stderr=%q)", code, stderr.String())
		}
		var res validate.Result
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("expected json output, unmarshal failed: %v (stdout=%q)", err, stdout.String())
		}
		if res.OK {
			t.Fatalf("expected ok=false, got ok=true")
		}
		found := false
		for _, f := range res.Errors {
			if f.Code == "ZCL_E_USAGE" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected ZCL_E_USAGE in errors, got: %+v", res.Errors)
		}
	}

	// IO-style failure: directory does not exist.
	{
		parent := t.TempDir()
		missing := filepath.Join(parent, "does-not-exist")
		var stdout, stderr bytes.Buffer
		r := Runner{
			Version: "0.0.0-dev",
			Now:     func() time.Time { return time.Date(2026, 2, 16, 18, 0, 0, 0, time.UTC) },
			Stdout:  &stdout,
			Stderr:  &stderr,
		}

		code := r.Run([]string{"validate", "--json", missing})
		if code != 1 {
			t.Fatalf("expected exit 1 for io failure, got %d (stderr=%q)", code, stderr.String())
		}
		var res validate.Result
		if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
			t.Fatalf("expected json output, unmarshal failed: %v (stdout=%q)", err, stdout.String())
		}
		if res.OK {
			t.Fatalf("expected ok=false, got ok=true")
		}
		found := false
		for _, f := range res.Errors {
			if f.Code == "ZCL_E_IO" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected ZCL_E_IO in errors, got: %+v", res.Errors)
		}
	}
}
