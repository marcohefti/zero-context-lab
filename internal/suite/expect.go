package suite

import (
	"fmt"
	"regexp"
	"strings"

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

// TraceFacts are precomputed, trace-derived facts needed to evaluate trace expectations.
// Callers should derive these from tool.calls.jsonl / attempt.report.json deterministically.
type TraceFacts struct {
	ToolCallsTotal            int64
	FailuresTotal             int64
	TimeoutsTotal             int64
	RepeatMaxStreak           int64
	DistinctCommandSignatures int64
	CommandNamesSeen          []string
}

func Evaluate(s SuiteFileV1, missionID string, fb schema.FeedbackJSONV1, tf *TraceFacts) ExpectationResult {
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
			if len(fb.ResultJSON) > 0 {
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
			if len(fb.ResultJSON) == 0 {
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

	if m.Expects.Trace != nil {
		// If trace expectations exist but no trace facts were provided, fail explicitly.
		if tf == nil {
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_TRACE_MISSING",
				Message: "trace expectations require trace-derived facts",
			})
		} else {
			if m.Expects.Trace.MaxToolCallsTotal > 0 && tf.ToolCallsTotal > m.Expects.Trace.MaxToolCallsTotal {
				failures = append(failures, ExpectationFailure{
					Code:    "ZCL_E_EXPECT_MAX_TOOL_CALLS",
					Message: "toolCallsTotal exceeds maxToolCallsTotal",
				})
			}
			if m.Expects.Trace.MaxFailuresTotal > 0 && tf.FailuresTotal > m.Expects.Trace.MaxFailuresTotal {
				failures = append(failures, ExpectationFailure{
					Code:    "ZCL_E_EXPECT_MAX_FAILURES",
					Message: "failuresTotal exceeds maxFailuresTotal",
				})
			}
			if m.Expects.Trace.MaxTimeoutsTotal > 0 && tf.TimeoutsTotal > m.Expects.Trace.MaxTimeoutsTotal {
				failures = append(failures, ExpectationFailure{
					Code:    "ZCL_E_EXPECT_MAX_TIMEOUTS",
					Message: "timeoutsTotal exceeds maxTimeoutsTotal",
				})
			}
			if m.Expects.Trace.MaxRepeatStreak > 0 && tf.RepeatMaxStreak > m.Expects.Trace.MaxRepeatStreak {
				failures = append(failures, ExpectationFailure{
					Code:    "ZCL_E_EXPECT_MAX_REPEAT_STREAK",
					Message: "repeatMaxStreak exceeds maxRepeatStreak",
				})
			}
			if len(m.Expects.Trace.RequireCommandPrefix) > 0 {
				ok := false
				for _, seen := range tf.CommandNamesSeen {
					for _, pref := range m.Expects.Trace.RequireCommandPrefix {
						if pref != "" && strings.HasPrefix(seen, pref) {
							ok = true
							break
						}
					}
					if ok {
						break
					}
				}
				if !ok {
					failures = append(failures, ExpectationFailure{
						Code:    "ZCL_E_EXPECT_REQUIRED_COMMAND",
						Message: "required command prefix not observed in trace",
					})
				}
			}
		}
	}

	return ExpectationResult{
		Evaluated: true,
		OK:        len(failures) == 0,
		Failures:  failures,
	}
}
