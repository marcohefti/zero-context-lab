package suite

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
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
	ToolOpsSeen               []string
	MCPToolsSeen              []string
}

func Evaluate(s SuiteFileV1, missionID string, fb schema.FeedbackJSONV1, tf *TraceFacts) ExpectationResult {
	m := FindMission(s, missionID)
	if m == nil || m.Expects == nil {
		return ExpectationResult{Evaluated: false, OK: true}
	}

	var failures []ExpectationFailure
	failures = append(failures, evaluateOKExpectation(m.Expects.OK, fb.OK)...)
	failures = append(failures, evaluateResultExpectation(m.Expects.Result, fb)...)
	failures = append(failures, evaluateTraceExpectation(m.Expects.Trace, tf)...)
	failures = append(failures, evaluateSemanticExpectation(m.Expects.Semantic, fb, tf)...)

	return ExpectationResult{
		Evaluated: true,
		OK:        len(failures) == 0,
		Failures:  failures,
	}
}

func evaluateOKExpectation(expectsOK *bool, actualOK bool) []ExpectationFailure {
	if expectsOK == nil || actualOK == *expectsOK {
		return nil
	}
	return []ExpectationFailure{{
		Code:    "ZCL_E_EXPECT_OK",
		Message: fmt.Sprintf("expected ok=%v, got ok=%v", *expectsOK, actualOK),
	}}
}

func evaluateResultExpectation(expects *ResultExpectsV1, fb schema.FeedbackJSONV1) []ExpectationFailure {
	if expects == nil {
		return nil
	}
	failures := ValidateResultShape(expects, fb)
	if expects.Type != "string" || len(fb.ResultJSON) > 0 {
		return failures
	}
	failures = append(failures, evaluateStringResultEquals(expects.Equals, fb.Result)...)
	failures = append(failures, evaluateStringResultPattern(expects.Pattern, fb.Result)...)
	return failures
}

func evaluateStringResultEquals(expected string, actual string) []ExpectationFailure {
	if expected == "" || actual == expected {
		return nil
	}
	return []ExpectationFailure{{
		Code:    "ZCL_E_EXPECT_RESULT_EQUALS",
		Message: "feedback result does not match expected equals",
	}}
}

func evaluateStringResultPattern(pattern string, actual string) []ExpectationFailure {
	if pattern == "" {
		return nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_PATTERN_INVALID",
			Message: "invalid expects.result.pattern regex",
		}}
	}
	if re.MatchString(actual) {
		return nil
	}
	return []ExpectationFailure{{
		Code:    "ZCL_E_EXPECT_RESULT_PATTERN",
		Message: "feedback result does not match expected pattern",
	}}
}

func evaluateTraceExpectation(expects *TraceExpectsV1, tf *TraceFacts) []ExpectationFailure {
	if expects == nil {
		return nil
	}
	if tf == nil {
		return []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_TRACE_MISSING",
			Message: "trace expectations require trace-derived facts",
		}}
	}
	failures := make([]ExpectationFailure, 0, 4)
	failures = append(failures, exceedsTraceLimit(expects.MaxToolCallsTotal, tf.ToolCallsTotal, "ZCL_E_EXPECT_MAX_TOOL_CALLS", "toolCallsTotal exceeds maxToolCallsTotal")...)
	failures = append(failures, exceedsTraceLimit(expects.MaxFailuresTotal, tf.FailuresTotal, "ZCL_E_EXPECT_MAX_FAILURES", "failuresTotal exceeds maxFailuresTotal")...)
	failures = append(failures, exceedsTraceLimit(expects.MaxTimeoutsTotal, tf.TimeoutsTotal, "ZCL_E_EXPECT_MAX_TIMEOUTS", "timeoutsTotal exceeds maxTimeoutsTotal")...)
	failures = append(failures, exceedsTraceLimit(expects.MaxRepeatStreak, tf.RepeatMaxStreak, "ZCL_E_EXPECT_MAX_REPEAT_STREAK", "repeatMaxStreak exceeds maxRepeatStreak")...)
	if len(expects.RequireCommandPrefix) > 0 && !matchesAnyPrefix(tf.CommandNamesSeen, expects.RequireCommandPrefix) {
		failures = append(failures, ExpectationFailure{
			Code:    "ZCL_E_EXPECT_REQUIRED_COMMAND",
			Message: "required command prefix not observed in trace",
		})
	}
	return failures
}

func exceedsTraceLimit(limit int64, actual int64, code string, message string) []ExpectationFailure {
	if limit <= 0 || actual <= limit {
		return nil
	}
	return []ExpectationFailure{{Code: code, Message: message}}
}

func matchesAnyPrefix(seen []string, prefixes []string) bool {
	for _, name := range seen {
		for _, pref := range prefixes {
			if pref != "" && strings.HasPrefix(name, pref) {
				return true
			}
		}
	}
	return false
}

func evaluateSemanticExpectation(semantic *SemanticExpectsV1, fb schema.FeedbackJSONV1, tf *TraceFacts) []ExpectationFailure {
	if semantic == nil {
		return nil
	}
	return ValidateSemantic(semantic, fb, tf)
}
