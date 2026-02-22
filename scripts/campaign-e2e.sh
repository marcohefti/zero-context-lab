#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

pattern='TestCampaignRun_Status_Report_PublishCheck|TestCampaignRun_InvalidAndPublishCheckFails|TestCampaignResume_DoesNotDuplicateAttempts|TestCampaignRun_LockContentionReturnsAborted|TestMissionPromptsBuild_MaterializesDeterministically|TestValidate_SemanticMode_UsesSuiteSemanticRules'
go test ./internal/cli -run "$pattern" -count=1 >/dev/null

echo "campaign-e2e: PASS"
