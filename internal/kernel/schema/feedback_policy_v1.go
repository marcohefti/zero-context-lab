package schema

import "strings"

const (
	FeedbackPolicyStrictV1   = "strict"
	FeedbackPolicyAutoFailV1 = "auto_fail"
)

func NormalizeFeedbackPolicyV1(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "":
		return FeedbackPolicyAutoFailV1
	case FeedbackPolicyStrictV1, FeedbackPolicyAutoFailV1:
		return v
	default:
		return v
	}
}

func IsValidFeedbackPolicyV1(v string) bool {
	switch NormalizeFeedbackPolicyV1(v) {
	case FeedbackPolicyStrictV1, FeedbackPolicyAutoFailV1:
		return true
	default:
		return false
	}
}
