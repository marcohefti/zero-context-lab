package cli

import "github.com/marcohefti/zero-context-lab/internal/codes"

const (
	codeUsage                   = codes.Usage
	codeIO                      = codes.IO
	codeMissingArtifact         = codes.MissingArtifact
	codeTimeout                 = codes.Timeout
	codeSpawn                   = codes.Spawn
	codeToolFailed              = codes.ToolFailed
	codeContaminatedPrompt      = codes.ContaminatedPrompt
	codeVersionFloor            = codes.VersionFloor
	codeRuntimeStreamDisconnect = codes.RuntimeStreamDisconnect
	codeRuntimeCrash            = codes.RuntimeCrash
	codeRuntimeAuth             = codes.RuntimeAuth
	codeRuntimeRateLimit        = codes.RuntimeRateLimit
	codeRuntimeListenerFailure  = codes.RuntimeListenerFailure

	codeMissionResultMissing      = codes.MissionResultMissing
	codeMissionResultInvalid      = codes.MissionResultInvalid
	codeMissionResultTurnTooEarly = codes.MissionResultTurnTooEarly

	codeCampaignMissingAttempt  = codes.CampaignMissingAttempt
	codeCampaignAttemptNotValid = codes.CampaignAttemptNotValid
	codeCampaignArtifactGate    = codes.CampaignArtifactGate
	codeCampaignTraceGate       = codes.CampaignTraceGate
	codeCampaignTimeoutGate     = codes.CampaignTimeoutGate
	codeCampaignSummaryParse    = codes.CampaignSummaryParse
	codeCampaignSkipped         = codes.CampaignSkipped

	codeShim = codes.Shim
)

func campaignFlowExitCode(exitCode int) string {
	return codes.CampaignFlowExit(exitCode)
}
