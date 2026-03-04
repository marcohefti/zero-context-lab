package schema

const (
	NativeResultSourceTaskCompleteLastAgentMessageV1 = "task_complete_last_agent_message"
	NativeResultSourcePhaseFinalAnswerV1             = "phase_final_answer"
	NativeResultSourceDeltaFallbackV1                = "delta_fallback"
)

func IsValidNativeResultSourceV1(v string) bool {
	switch v {
	case NativeResultSourceTaskCompleteLastAgentMessageV1, NativeResultSourcePhaseFinalAnswerV1, NativeResultSourceDeltaFallbackV1:
		return true
	default:
		return false
	}
}
