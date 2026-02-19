package update

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckMinimum(t *testing.T) {
	ok, msg, err := CheckMinimum("0.3.0", "0.2.0")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ok || msg != "" {
		t.Fatalf("expected ok=true, got ok=%v msg=%q", ok, msg)
	}

	ok, msg, err = CheckMinimum("0.1.9", "0.2.0")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false")
	}
	if msg == "" {
		t.Fatalf("expected non-empty message")
	}

	_, _, err = CheckMinimum("0.3.0", "not-a-semver")
	if err == nil {
		t.Fatalf("expected error for invalid minimum semver")
	}
}

func TestStatusCheck_RefreshAndCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-cache.json")
	t.Setenv("ZCL_UPDATE_CACHE_FILE", cachePath)

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tag_name":"v0.2.1","html_url":"https://example.test/releases/v0.2.1"}`)
	}))
	defer srv.Close()
	t.Setenv("ZCL_UPDATE_CHECK_URL", srv.URL)

	now := time.Date(2026, 2, 19, 22, 0, 0, 0, time.UTC)
	st, err := StatusCheck("0.2.0", now, StatusOptions{Refresh: true})
	if err != nil {
		t.Fatalf("status refresh: %v", err)
	}
	if !st.UpdateAvailable {
		t.Fatalf("expected updateAvailable=true, got false: %+v", st)
	}
	if st.Cached {
		t.Fatalf("expected cached=false after refresh")
	}
	if st.LatestVersion != "v0.2.1" {
		t.Fatalf("unexpected latestVersion: %q", st.LatestVersion)
	}
	if hits != 1 {
		t.Fatalf("expected one network hit, got %d", hits)
	}

	st2, err := StatusCheck("0.2.0", now.Add(1*time.Hour), StatusOptions{Refresh: false})
	if err != nil {
		t.Fatalf("status cached: %v", err)
	}
	if !st2.Cached {
		t.Fatalf("expected cached=true when reading fresh cache")
	}
	if hits != 1 {
		t.Fatalf("expected no extra network hit while cache fresh, got %d", hits)
	}

	// Cache-only mode should avoid network even when cache is stale.
	st3, err := StatusCheck("0.2.0", now.Add(49*time.Hour), StatusOptions{Refresh: false, CacheOnly: true})
	if err != nil {
		t.Fatalf("status cache-only: %v", err)
	}
	if !st3.Cached {
		t.Fatalf("expected cached=true in cache-only mode")
	}
	if hits != 1 {
		t.Fatalf("expected cache-only mode to avoid network, got %d hits", hits)
	}
}

func TestNotifiedLifecycle(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-cache.json")
	t.Setenv("ZCL_UPDATE_CACHE_FILE", cachePath)

	now := time.Date(2026, 2, 19, 22, 0, 0, 0, time.UTC)
	if err := writeCache(cachePath, cacheRecordV1{
		SchemaVersion: cacheSchemaV1,
		Source:        sourceGithubRelease,
		CheckedAt:     now.Format(time.RFC3339Nano),
		LatestVersion: "v1.0.0",
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	recent, err := NotifiedRecently(now, 24*time.Hour)
	if err != nil {
		t.Fatalf("NotifiedRecently: %v", err)
	}
	if recent {
		t.Fatalf("expected recent=false before mark")
	}

	if err := MarkNotified(now); err != nil {
		t.Fatalf("MarkNotified: %v", err)
	}
	recent, err = NotifiedRecently(now.Add(1*time.Hour), 24*time.Hour)
	if err != nil {
		t.Fatalf("NotifiedRecently(after): %v", err)
	}
	if !recent {
		t.Fatalf("expected recent=true after mark")
	}
}

func TestCompareSemver_Prerelease(t *testing.T) {
	a, ok := parseSemver("1.2.3-alpha.1")
	if !ok {
		t.Fatal("parse a failed")
	}
	b, ok := parseSemver("1.2.3")
	if !ok {
		t.Fatal("parse b failed")
	}
	if got := compareSemver(a, b); got >= 0 {
		t.Fatalf("expected prerelease < release, got %d", got)
	}
}
