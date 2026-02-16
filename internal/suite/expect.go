package suite

import (
	"fmt"
	"regexp"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

type ExpectationFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ExpectationResult struct {
	Evaluated bool                 `json:"evaluated"`
	OK        bool                 `json:"ok"`
	Failures  []ExpectationFailure `json:"failures,omitempty"`
}

func Evaluate(s SuiteFileV1, missionID string, fb schema.FeedbackJSONV1) ExpectationResult {
	m := FindMission(s, missionID)
	if m == nil || m.Expects == nil {
		return ExpectationResult{Evaluated: false, OK: true}
	}

	var failures []ExpectationFailure
	if m.Expects.OK != nil && fb.OK != *m.Expects.OK {
		failures = append(failures, ExpectationFailure{
			Code:    "ZCL_E_EXPECT_OK",
			Message: fmt.Sprintf("expected ok=%v, got ok=%v", *m.Expects.OK, fb.OK),
		})
	}

	if m.Expects.Result != nil {
		switch m.Expects.Result.Type {
		case "string":
			if fb.ResultJSON != nil && len(fb.ResultJSON) > 0 {
				failures = append(failures, ExpectationFailure{
					Code:    "ZCL_E_EXPECT_RESULT_TYPE",
					Message: "expected result type string, got resultJson",
				})
				break
			}
			if m.Expects.Result.Equals != "" && fb.Result != m.Expects.Result.Equals {
				failures = append(failures, ExpectationFailure{
					Code:    "ZCL_E_EXPECT_RESULT_EQUALS",
					Message: "feedback result does not match expected equals",
				})
			}
			if m.Expects.Result.Pattern != "" {
				re, err := regexp.Compile(m.Expects.Result.Pattern)
				if err != nil {
					failures = append(failures, ExpectationFailure{
						Code:    "ZCL_E_EXPECT_PATTERN_INVALID",
						Message: "invalid expects.result.pattern regex",
					})
				} else if !re.MatchString(fb.Result) {
					failures = append(failures, ExpectationFailure{
						Code:    "ZCL_E_EXPECT_RESULT_PATTERN",
						Message: "feedback result does not match expected pattern",
					})
				}
			}
		case "json":
			if fb.ResultJSON == nil || len(fb.ResultJSON) == 0 {
				failures = append(failures, ExpectationFailure{
					Code:    "ZCL_E_EXPECT_RESULT_TYPE",
					Message: "expected result type json, got result",
				})
			}
		default:
			// Parse already validates, but keep evaluation resilient.
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_RESULT_TYPE",
				Message: "unsupported expects.result.type",
			})
		}
	}

	return ExpectationResult{
		Evaluated: true,
		OK:        len(failures) == 0,
		Failures:  failures,
	}
}
