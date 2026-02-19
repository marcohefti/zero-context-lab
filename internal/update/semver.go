package update

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var semverRe = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

type semver struct {
	major int
	minor int
	patch int
	pre   []string
}

func (s semver) String() string {
	base := fmt.Sprintf("%d.%d.%d", s.major, s.minor, s.patch)
	if len(s.pre) == 0 {
		return base
	}
	return base + "-" + strings.Join(s.pre, ".")
}

func parseSemver(raw string) (semver, bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return semver{}, false
	}
	maj, err := strconv.Atoi(m[1])
	if err != nil {
		return semver{}, false
	}
	min, err := strconv.Atoi(m[2])
	if err != nil {
		return semver{}, false
	}
	patch, err := strconv.Atoi(m[3])
	if err != nil {
		return semver{}, false
	}
	out := semver{major: maj, minor: min, patch: patch}
	if strings.TrimSpace(m[4]) != "" {
		out.pre = strings.Split(m[4], ".")
	}
	return out, true
}

func compareSemver(a semver, b semver) int {
	if a.major != b.major {
		if a.major < b.major {
			return -1
		}
		return 1
	}
	if a.minor != b.minor {
		if a.minor < b.minor {
			return -1
		}
		return 1
	}
	if a.patch != b.patch {
		if a.patch < b.patch {
			return -1
		}
		return 1
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	return comparePrerelease(a.pre, b.pre)
}

func comparePrerelease(a []string, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] == b[i] {
			continue
		}
		an, aNum := prereleaseNumeric(a[i])
		bn, bNum := prereleaseNumeric(b[i])
		if aNum && bNum {
			if an < bn {
				return -1
			}
			if an > bn {
				return 1
			}
			continue
		}
		if aNum && !bNum {
			return -1
		}
		if !aNum && bNum {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
		return 1
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

func prereleaseNumeric(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
