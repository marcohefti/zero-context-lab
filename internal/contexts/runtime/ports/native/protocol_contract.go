package native

import (
	"fmt"
	"strconv"
	"strings"
)

type ProtocolContract struct {
	RuntimeName           string `json:"runtimeName"`
	MinimumProtocolMajor  int    `json:"minimumProtocolMajor"`
	MinimumProtocolMinor  int    `json:"minimumProtocolMinor"`
	MinimumRuntimeVersion string `json:"minimumRuntimeVersion,omitempty"`
}

func (c ProtocolContract) Validate(actualProtocol string, actualRuntimeVersion string) error {
	maj, min, err := parseMajorMinor(actualProtocol)
	if err != nil {
		return WrapError(ErrorCompatibility, "invalid protocol version from runtime", err)
	}
	if maj < c.MinimumProtocolMajor || (maj == c.MinimumProtocolMajor && min < c.MinimumProtocolMinor) {
		msg := fmt.Sprintf(
			"%s protocol %q is below minimum supported %d.%d; upgrade runtime",
			strings.TrimSpace(c.RuntimeName),
			strings.TrimSpace(actualProtocol),
			c.MinimumProtocolMajor,
			c.MinimumProtocolMinor,
		)
		return NewError(ErrorCompatibility, strings.TrimSpace(msg))
	}
	if strings.TrimSpace(c.MinimumRuntimeVersion) == "" || strings.TrimSpace(actualRuntimeVersion) == "" {
		return nil
	}
	if compareLooseSemver(actualRuntimeVersion, c.MinimumRuntimeVersion) < 0 {
		msg := fmt.Sprintf(
			"%s runtime version %q is below minimum supported %q; upgrade runtime",
			strings.TrimSpace(c.RuntimeName),
			strings.TrimSpace(actualRuntimeVersion),
			strings.TrimSpace(c.MinimumRuntimeVersion),
		)
		return NewError(ErrorCompatibility, strings.TrimSpace(msg))
	}
	return nil
}

func parseMajorMinor(v string) (major int, minor int, err error) {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if v == "" {
		return 0, 0, fmt.Errorf("empty version")
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("expected major.minor")
	}
	major, err = strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid major: %w", err)
	}
	minor, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid minor: %w", err)
	}
	return major, minor, nil
}

func compareLooseSemver(a string, b string) int {
	av := semverParts(a)
	bv := semverParts(b)
	max := len(av)
	if len(bv) > max {
		max = len(bv)
	}
	for i := 0; i < max; i++ {
		ai := 0
		if i < len(av) {
			ai = av[i]
		}
		bi := 0
		if i < len(bv) {
			bi = bv[i]
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

func semverParts(v string) []int {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, raw := range parts {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			out = append(out, 0)
			continue
		}
		n := strings.Builder{}
		for _, ch := range raw {
			if ch < '0' || ch > '9' {
				break
			}
			n.WriteRune(ch)
		}
		if n.Len() == 0 {
			out = append(out, 0)
			continue
		}
		val, err := strconv.Atoi(n.String())
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, val)
	}
	return out
}
