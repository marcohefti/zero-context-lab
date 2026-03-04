package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	reInvalid = regexp.MustCompile(`[^a-z0-9-]+`)
	reDashes  = regexp.MustCompile(`-+`)
	reRunID   = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}Z-[0-9a-f]{6}$`)
)

func NewRunID(now time.Time) (string, error) {
	// Format matches the docs/examples: YYYYMMDD-HHMMSSZ-<hex6>.
	prefix := now.UTC().Format("20060102-150405Z")

	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(b[:]), nil
}

func IsValidRunID(s string) bool {
	return reRunID.MatchString(strings.TrimSpace(s))
}

func SanitizeComponent(s string) string {
	// Keep this strict and stable: lower + [a-z0-9-], collapse dashes.
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.ReplaceAll(v, "_", "-")
	v = reInvalid.ReplaceAllString(v, "-")
	v = reDashes.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-")
	return v
}

func NewAttemptID(index int, missionID string, retry int) string {
	m := SanitizeComponent(missionID)
	if m == "" {
		m = "mission"
	}
	if index < 1 {
		index = 1
	}
	if retry < 1 {
		retry = 1
	}
	return fmt.Sprintf("%03d-%s-r%d", index, m, retry)
}
