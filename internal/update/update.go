package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/store"
)

const (
	statusSchemaV1      = 1
	cacheSchemaV1       = 1
	sourceGithubRelease = "github_release_latest"
	defaultCheckURL     = "https://api.github.com/repos/marcohefti/zero-context-lab/releases/latest"
	defaultTimeout      = 3 * time.Second
	cacheTTL            = 24 * time.Hour
)

type Commands struct {
	NPM       string `json:"npm"`
	Homebrew  string `json:"homebrew"`
	GoInstall string `json:"goInstall"`
}

type Status struct {
	SchemaVersion   int      `json:"schemaVersion"`
	Source          string   `json:"source"`
	CheckedAt       string   `json:"checkedAt"`
	Cached          bool     `json:"cached"`
	Policy          string   `json:"policy"`
	CurrentVersion  string   `json:"currentVersion"`
	CurrentSemver   string   `json:"currentSemver,omitempty"`
	LatestVersion   string   `json:"latestVersion,omitempty"`
	LatestSemver    string   `json:"latestSemver,omitempty"`
	LatestURL       string   `json:"latestUrl,omitempty"`
	Comparable      bool     `json:"comparable"`
	UpdateAvailable bool     `json:"updateAvailable"`
	Message         string   `json:"message,omitempty"`
	Commands        Commands `json:"commands"`
}

type StatusOptions struct {
	Refresh   bool
	CacheOnly bool
	Timeout   time.Duration
}

type cacheRecordV1 struct {
	SchemaVersion int    `json:"schemaVersion"`
	Source        string `json:"source"`
	CheckedAt     string `json:"checkedAt"`
	LatestVersion string `json:"latestVersion"`
	LatestURL     string `json:"latestUrl,omitempty"`
	NotifiedAt    string `json:"notifiedAt,omitempty"`
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

func StatusCheck(currentVersion string, now time.Time, opts StatusOptions) (Status, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	now = now.UTC()
	path := cacheFilePath()
	cached, hasCached, cacheErr := readCache(path)
	if cacheErr != nil {
		// Ignore cache parse issues; explicit refresh should still work.
		hasCached = false
	}

	if !opts.Refresh && hasCached {
		if opts.CacheOnly {
			return buildStatus(currentVersion, cached.Source, cached.CheckedAt, cached.LatestVersion, cached.LatestURL, true), nil
		}
		checkedAt, err := time.Parse(time.RFC3339Nano, cached.CheckedAt)
		if err == nil && now.Sub(checkedAt) < cacheTTL {
			return buildStatus(currentVersion, cached.Source, cached.CheckedAt, cached.LatestVersion, cached.LatestURL, true), nil
		}
	}

	latest, latestURL, err := fetchLatestRelease(opts.Timeout)
	if err != nil {
		if !opts.Refresh && hasCached {
			return buildStatus(currentVersion, cached.Source, cached.CheckedAt, cached.LatestVersion, cached.LatestURL, true), nil
		}
		return Status{}, err
	}

	next := cacheRecordV1{
		SchemaVersion: cacheSchemaV1,
		Source:        sourceGithubRelease,
		CheckedAt:     now.Format(time.RFC3339Nano),
		LatestVersion: latest,
		LatestURL:     latestURL,
	}
	if hasCached {
		next.NotifiedAt = cached.NotifiedAt
	}
	_ = writeCache(path, next)
	return buildStatus(currentVersion, next.Source, next.CheckedAt, next.LatestVersion, next.LatestURL, false), nil
}

func CheckMinimum(currentVersion string, minimumVersion string) (bool, string, error) {
	minimum := strings.TrimSpace(minimumVersion)
	if minimum == "" {
		return true, "", nil
	}
	curV, ok := parseSemver(strings.TrimSpace(currentVersion))
	if !ok {
		return false, fmt.Sprintf("current zcl version %q is not semver-comparable; required minimum is %q", currentVersion, minimumVersion), nil
	}
	minV, ok := parseSemver(minimum)
	if !ok {
		return false, "", fmt.Errorf("invalid ZCL_MIN_VERSION %q (expected semver like 0.2.0)", minimumVersion)
	}
	if compareSemver(curV, minV) < 0 {
		return false, fmt.Sprintf("zcl version %q is below required minimum %q", currentVersion, minimumVersion), nil
	}
	return true, "", nil
}

func NotifiedRecently(now time.Time, window time.Duration) (bool, error) {
	if window <= 0 {
		return false, nil
	}
	path := cacheFilePath()
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	c, ok, err := readCache(path)
	if err != nil || !ok || strings.TrimSpace(c.NotifiedAt) == "" {
		return false, err
	}
	t, err := time.Parse(time.RFC3339Nano, c.NotifiedAt)
	if err != nil {
		return false, nil
	}
	return now.UTC().Sub(t.UTC()) < window, nil
}

func MarkNotified(now time.Time) error {
	path := cacheFilePath()
	if strings.TrimSpace(path) == "" {
		return nil
	}
	c, ok, err := readCache(path)
	if err != nil || !ok {
		return err
	}
	c.NotifiedAt = now.UTC().Format(time.RFC3339Nano)
	return writeCache(path, c)
}

func buildStatus(currentVersion string, source string, checkedAt string, latestVersion string, latestURL string, cached bool) Status {
	status := Status{
		SchemaVersion:  statusSchemaV1,
		Source:         strings.TrimSpace(source),
		CheckedAt:      checkedAt,
		Cached:         cached,
		Policy:         "manual_only",
		CurrentVersion: strings.TrimSpace(currentVersion),
		LatestVersion:  strings.TrimSpace(latestVersion),
		LatestURL:      strings.TrimSpace(latestURL),
		Comparable:     false,
		Commands: Commands{
			NPM:       "npm i -g @marcohefti/zcl@latest",
			Homebrew:  "brew upgrade marcohefti/zero-context-lab/zcl",
			GoInstall: "go install github.com/marcohefti/zero-context-lab/cmd/zcl@latest",
		},
	}
	cur, curOK := parseSemver(status.CurrentVersion)
	lat, latOK := parseSemver(status.LatestVersion)
	if curOK {
		status.CurrentSemver = cur.String()
	}
	if latOK {
		status.LatestSemver = lat.String()
	}
	if curOK && latOK {
		status.Comparable = true
		status.UpdateAvailable = compareSemver(cur, lat) < 0
		if status.UpdateAvailable {
			status.Message = fmt.Sprintf("update available: %s -> %s", status.CurrentVersion, status.LatestVersion)
		} else {
			status.Message = "already on latest version"
		}
		return status
	}
	status.Message = "version comparison unavailable for non-semver build tags"
	return status
}

func fetchLatestRelease(timeout time.Duration) (string, string, error) {
	endpoint := strings.TrimSpace(os.Getenv("ZCL_UPDATE_CHECK_URL"))
	if endpoint == "" {
		endpoint = defaultCheckURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "zcl-update-check")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("update check failed: %s", resp.Status)
	}
	var payload githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", err
	}
	tag := strings.TrimSpace(payload.TagName)
	if tag == "" {
		return "", "", fmt.Errorf("update check failed: empty tag_name")
	}
	return tag, strings.TrimSpace(payload.HTMLURL), nil
}

func cacheFilePath() string {
	if p := strings.TrimSpace(os.Getenv("ZCL_UPDATE_CACHE_FILE")); p != "" {
		return p
	}
	if d, err := os.UserCacheDir(); err == nil && strings.TrimSpace(d) != "" {
		return filepath.Join(d, "zcl", "update-status-v1.json")
	}
	if h, err := os.UserHomeDir(); err == nil && strings.TrimSpace(h) != "" {
		return filepath.Join(h, ".zcl", "cache", "update-status-v1.json")
	}
	return ""
}

func readCache(path string) (cacheRecordV1, bool, error) {
	if strings.TrimSpace(path) == "" {
		return cacheRecordV1{}, false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cacheRecordV1{}, false, nil
		}
		return cacheRecordV1{}, false, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c cacheRecordV1
	if err := dec.Decode(&c); err != nil {
		return cacheRecordV1{}, false, err
	}
	if c.SchemaVersion != cacheSchemaV1 {
		return cacheRecordV1{}, false, fmt.Errorf("unsupported cache schemaVersion=%d", c.SchemaVersion)
	}
	if strings.TrimSpace(c.CheckedAt) == "" || strings.TrimSpace(c.LatestVersion) == "" {
		return cacheRecordV1{}, false, fmt.Errorf("invalid update cache contents")
	}
	return c, true, nil
}

func writeCache(path string, c cacheRecordV1) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return store.WriteJSONAtomic(path, c)
}
