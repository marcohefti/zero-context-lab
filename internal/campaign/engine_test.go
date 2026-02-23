package campaign

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/codes"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/suite"
)

type noopMissionExecutor struct{}

func (noopMissionExecutor) Prepare(context.Context, FlowSpec) error { return nil }

func (noopMissionExecutor) RunMission(context.Context, FlowSpec, int, string) (FlowRunV1, error) {
	return FlowRunV1{}, nil
}

func (noopMissionExecutor) Cleanup(context.Context, FlowSpec) error { return nil }

type watchdogMissionExecutor struct{}

func (watchdogMissionExecutor) Prepare(context.Context, FlowSpec) error { return nil }

func (watchdogMissionExecutor) RunMission(ctx context.Context, _ FlowSpec, missionIndex int, missionID string) (FlowRunV1, error) {
	if missionIndex == 0 {
		<-ctx.Done()
		return FlowRunV1{}, ctx.Err()
	}
	return FlowRunV1{
		OK: true,
		Attempts: []AttemptStatusV1{{
			MissionIndex: missionIndex,
			MissionID:    missionID,
			Status:       AttemptStatusValid,
		}},
	}, nil
}

func (watchdogMissionExecutor) Cleanup(context.Context, FlowSpec) error { return nil }

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

func TestExecuteMissionEngine_WatchdogTimeoutContinueAndHeartbeat(t *testing.T) {
	outRoot := t.TempDir()
	parsed := ParsedSpec{
		SpecPath: filepath.Join(outRoot, "campaign.yaml"),
		Spec: SpecV1{
			SchemaVersion: 1,
			CampaignID:    "cmp-watchdog",
			Execution:     ExecutionSpec{FlowMode: FlowModeSequence},
			Flows: []FlowSpec{
				{
					FlowID: "flow-a",
					Runner: RunnerAdapterSpec{Type: RunnerTypeProcessCmd},
				},
			},
		},
		BaseSuite: suite.ParsedSuite{
			Suite: suite.SuiteFileV1{
				Version: 1,
				SuiteID: "suite-a",
				Missions: []suite.MissionV1{
					{MissionID: "m1", Prompt: "p1"},
					{MissionID: "m2", Prompt: "p2"},
				},
			},
		},
		MissionIndexes: []int{0, 1},
	}

	now := time.Date(2026, 2, 22, 14, 10, 0, 0, time.UTC)
	res, err := ExecuteMissionEngine(
		parsed,
		watchdogMissionExecutor{},
		func(_ ParsedSpec, missionIndex int, missionID string, runs []FlowRunV1) (MissionGateV1, error) {
			mg := MissionGateV1{
				MissionIndex: missionIndex,
				MissionID:    missionID,
				OK:           true,
			}
			if len(runs) > 0 && len(runs[0].Attempts) > 0 && runs[0].Attempts[0].Status != AttemptStatusValid {
				mg.OK = false
				mg.Reasons = []string{codes.CampaignTimeoutGate}
			}
			return mg, nil
		},
		nil,
		EngineOptions{
			OutRoot:                  outRoot,
			RunID:                    "run-watchdog-1",
			MissionIndexes:           []int{0, 1},
			MissionEnvelopeMs:        40,
			WatchdogHeartbeatMs:      10,
			WatchdogHardKillContinue: true,
			Now: func() time.Time {
				now = now.Add(5 * time.Millisecond)
				return now
			},
		},
	)
	if err != nil {
		t.Fatalf("ExecuteMissionEngine: %v", err)
	}
	if res.Exit != 2 {
		t.Fatalf("expected invalid exit=2 due first gate fail, got %d", res.Exit)
	}
	if res.State.MissionsCompleted != 2 {
		t.Fatalf("expected mission 2 to continue after timeout, got %+v", res.State)
	}
	if len(res.State.MissionGates) != 2 {
		t.Fatalf("expected 2 mission gates, got %+v", res.State.MissionGates)
	}
	progressPath := ProgressPath(outRoot, parsed.Spec.CampaignID)
	raw, err := os.ReadFile(progressPath)
	if err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if !strings.Contains(string(raw), `"status":"watchdog_heartbeat"`) {
		t.Fatalf("expected watchdog heartbeat events in progress jsonl: %s", string(raw))
	}
}
