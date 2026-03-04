package schema

import "strings"

const (
	TimeoutStartAttemptStartV1  = "attempt_start"
	TimeoutStartFirstToolCallV1 = "first_tool_call"
)

func IsValidTimeoutStartV1(s string) bool {
	switch strings.TrimSpace(s) {
	case "":
		return true
	case TimeoutStartAttemptStartV1, TimeoutStartFirstToolCallV1:
		return true
	default:
		return false
	}
}
