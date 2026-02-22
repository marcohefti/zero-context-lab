package campaign

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunState_SaveLoadAndReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "campaign.run.state.json")

	st := RunStateV1{
		SchemaVersion:     1,
		CampaignID:        "cmp-1",
		RunID:             "20260222-101010Z-abc123",
		SpecPath:          "/tmp/campaign.yaml",
		OutRoot:           ".zcl",
		Status:            RunStatusInvalid,
		ReasonCodes:       []string{"ZCL_E_CAMPAIGN_GATE_FAILED"},
		StartedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		UpdatedAt:         time.Now().UTC().Format(time.RFC3339Nano),
		TotalMissions:     2,
		MissionOffset:     0,
		MissionsCompleted: 2,
		FlowRuns: []FlowRunV1{
			{
				FlowID:     "flow-a",
				RunnerType: RunnerTypeProcessCmd,
				OK:         false,
				Attempts: []AttemptStatusV1{
					{MissionIndex: 0, MissionID: "m1", Status: AttemptStatusValid},
					{MissionIndex: 1, MissionID: "m2", Status: AttemptStatusInvalid},
				},
			},
		},
		MissionGates: []MissionGateV1{
			{MissionIndex: 0, MissionID: "m1", OK: true},
			{MissionIndex: 1, MissionID: "m2", OK: false},
		},
	}
	if err := SaveRunState(path, st); err != nil {
		t.Fatalf("SaveRunState: %v", err)
	}
	got, err := LoadRunState(path)
	if err != nil {
		t.Fatalf("LoadRunState: %v", err)
	}
	if got.CampaignID != st.CampaignID || got.RunID != st.RunID || got.Status != st.Status {
		t.Fatalf("unexpected loaded state: %+v", got)
	}
	rep := BuildReport(got)
	if rep.CampaignID != st.CampaignID || rep.RunID != st.RunID {
		t.Fatalf("unexpected report identity: %+v", rep)
	}
	if rep.GatesPassed != 1 || rep.GatesFailed != 1 {
		t.Fatalf("unexpected gate summary: %+v", rep)
	}
	if len(rep.Flows) != 1 || rep.Flows[0].Valid != 1 || rep.Flows[0].Invalid != 1 {
		t.Fatalf("unexpected flow summary: %+v", rep.Flows)
	}
	sum := BuildSummary(got)
	if sum.GatesPassed != 1 || sum.GatesFailed != 1 {
		t.Fatalf("unexpected summary gates: %+v", sum)
	}
	if len(sum.Missions) != 2 {
		t.Fatalf("expected mission summaries, got %+v", sum.Missions)
	}
	if sum.ClaimedMissionsOK != 1 || sum.VerifiedMissionsOK != 1 || sum.MismatchCount != 0 {
		t.Fatalf("unexpected summary claimed/verified counts: %+v", sum)
	}
	if sum.EvidencePaths.RunStatePath == "" || sum.EvidencePaths.ResultsMDPath == "" {
		t.Fatalf("expected evidence paths in summary, got %+v", sum.EvidencePaths)
	}
}

func TestPlanAndProgress_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "campaign.plan.json")
	progressPath := filepath.Join(dir, "campaign.progress.jsonl")
	now := time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)

	plan := PlanV1{
		SchemaVersion: 1,
		CampaignID:    "cmp-1",
		SpecPath:      "/tmp/campaign.yaml",
		Missions: []PlanMissionV1{
			{MissionIndex: 1, MissionID: "m2"},
			{MissionIndex: 0, MissionID: "m1"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := SavePlan(planPath, plan); err != nil {
		t.Fatalf("SavePlan: %v", err)
	}
	gotPlan, err := LoadPlan(planPath)
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}
	if len(gotPlan.Missions) != 2 || gotPlan.Missions[0].MissionID != "m1" {
		t.Fatalf("expected sorted missions in plan, got %+v", gotPlan.Missions)
	}

	ev1 := ProgressEventV1{
		SchemaVersion:  1,
		CampaignID:     "cmp-1",
		RunID:          "run-1",
		MissionIndex:   0,
		MissionID:      "m1",
		FlowID:         "flow-a",
		AttemptID:      "001-m1-r1",
		AttemptDir:     "/tmp/attempt",
		Status:         AttemptStatusValid,
		IdempotencyKey: "cmp-1:flow-a:0",
		CreatedAt:      now,
	}
	ev2 := ProgressEventV1{
		SchemaVersion:  1,
		CampaignID:     "cmp-1",
		RunID:          "run-1",
		MissionIndex:   0,
		MissionID:      "m1",
		Status:         "gate_failed",
		ReasonCodes:    []string{"ZCL_E_CAMPAIGN_GATE_FAILED"},
		IdempotencyKey: "cmp-1:gate:0",
		CreatedAt:      now,
	}
	if err := AppendProgress(progressPath, ev1); err != nil {
		t.Fatalf("AppendProgress(ev1): %v", err)
	}
	if err := AppendProgress(progressPath, ev2); err != nil {
		t.Fatalf("AppendProgress(ev2): %v", err)
	}
	events, err := LoadProgress(progressPath)
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 progress events, got %d", len(events))
	}
	if events[0].IdempotencyKey != "cmp-1:flow-a:0" || events[1].Status != "gate_failed" {
		t.Fatalf("unexpected progress events: %+v", events)
	}

	_, err = os.Stat(LockPath(".zcl", "cmp-1"))
	if err == nil {
		t.Fatalf("lock path should be just a path helper, not pre-created")
	}
}
