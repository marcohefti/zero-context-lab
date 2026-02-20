package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/update"
)

func TestUpdateStatusJSON(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-cache.json")
	t.Setenv("ZCL_UPDATE_CACHE_FILE", cachePath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tag_name":"v0.9.0","html_url":"https://example.test/releases/v0.9.0"}`)
	}))
	defer srv.Close()
	t.Setenv("ZCL_UPDATE_CHECK_URL", srv.URL)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.8.0",
		Now:     func() time.Time { return time.Date(2026, 2, 19, 21, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"update", "status", "--json"})
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", code, stderr.String())
	}
	var got update.Status
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal update status: %v (stdout=%q)", err, stdout.String())
	}
	if !got.UpdateAvailable {
		t.Fatalf("expected updateAvailable=true, got false: %+v", got)
	}
	if got.LatestVersion != "v0.9.0" {
		t.Fatalf("unexpected latestVersion=%q", got.LatestVersion)
	}
	if got.Policy != "manual_only" {
		t.Fatalf("unexpected policy=%q", got.Policy)
	}
}

func TestVersionFloorBlocksCommands(t *testing.T) {
	t.Setenv("ZCL_MIN_VERSION", "0.2.0")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.1.0",
		Now:     func() time.Time { return time.Date(2026, 2, 19, 21, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	code := r.Run([]string{"contract", "--json"})
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "ZCL_E_VERSION_FLOOR") {
		t.Fatalf("expected ZCL_E_VERSION_FLOOR, got stderr=%q", stderr.String())
	}
}

func TestVersionFloorAllowsVersionAndUpdateHelp(t *testing.T) {
	t.Setenv("ZCL_MIN_VERSION", "99.0.0")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	r := Runner{
		Version: "0.1.0",
		Now:     func() time.Time { return time.Date(2026, 2, 19, 21, 0, 0, 0, time.UTC) },
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	if code := r.Run([]string{"version"}); code != 0 {
		t.Fatalf("version should bypass floor, got %d stderr=%q", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "0.1.0" {
		t.Fatalf("unexpected version stdout=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := r.Run([]string{"--version"}); code != 0 {
		t.Fatalf("--version should bypass floor, got %d stderr=%q", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "0.1.0" {
		t.Fatalf("unexpected --version stdout=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := r.Run([]string{"update", "--help"}); code != 0 {
		t.Fatalf("update --help should bypass floor, got %d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "zcl update status") {
		t.Fatalf("expected update help text, got %q", stdout.String())
	}
}
