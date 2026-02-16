package planner

import (
	"fmt"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/ids"
	"github.com/marcohefti/zero-context-lab/internal/suite"
)

func ParseSuiteSnapshot(path string, suiteID string) (any, error) {
	parsed, err := suite.ParseFile(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}

	sArg := ids.SanitizeComponent(suiteID)
	if parsed.Suite.SuiteID != "" && parsed.Suite.SuiteID != sArg {
		return nil, fmt.Errorf("suiteId mismatch: --suite=%s suite-file=%s", sArg, parsed.Suite.SuiteID)
	}
	return parsed.CanonicalJSON, nil
}
