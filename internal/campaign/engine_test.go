package campaign

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/suite"
)

type noopMissionExecutor struct{}

func (noopMissionExecutor) Prepare(context.Context, FlowSpec) error { return nil }

func (noopMissionExecutor) RunMission(context.Context, FlowSpec, int, string) (FlowRunV1, error) {
	return FlowRunV1{}, nil
}

func (noopMissionExecutor) Cleanup(context.Context, FlowSpec) error { return nil }

func TestExecuteMissionEngine_NonLockErrorDoesNotMapToLockTimeout(t *testing.T) {
	parsed := ParsedSpec{
		SpecPath: "campaign.yaml",
		Spec: SpecV1{
			SchemaVersion: 1,
			CampaignID:    "cmp-non-lock",
		},
		BaseSuite: suite.ParsedSuite{
			Suite: suite.SuiteFileV1{
				Version: 1,
				SuiteID: "suite-a",
				Missions: []suite.MissionV1{
					{MissionID: "m1", Prompt: "p1"},
				},
			},
		},
		MissionIndexes: []int{1},
	}

	_, err := ExecuteMissionEngine(
		parsed,
		noopMissionExecutor{},
		func(ParsedSpec, int, string, []FlowRunV1) (MissionGateV1, error) {
			return MissionGateV1{OK: true}, nil
		},
		nil,
		EngineOptions{
			OutRoot:  t.TempDir(),
			RunID:    "run-1",
			LockWait: 100 * time.Millisecond,
			Now:      func() time.Time { return time.Date(2026, 2, 22, 14, 0, 0, 0, time.UTC) },
		},
	)
	if err == nil {
		t.Fatalf("expected non-lock error from executeMissionEngineLocked")
	}
	if store.IsLockTimeout(err) {
		t.Fatalf("expected non-lock error, got lock timeout: %v", err)
	}
	if !strings.Contains(err.Error(), "campaign mission index out of range") {
		t.Fatalf("unexpected error: %v", err)
	}
}
