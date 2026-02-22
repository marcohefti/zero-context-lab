package runners

import (
	"context"
	"errors"
	"testing"

	"github.com/marcohefti/zero-context-lab/internal/campaign"
)

func TestCampaignExecutor_AllRunnerTypesShareContract(t *testing.T) {
	invocations := 0
	exec, err := NewCampaignExecutor(func(_ context.Context, flow campaign.FlowSpec, missionIndex int, missionID string) (campaign.FlowRunV1, error) {
		invocations++
		if missionIndex != 3 || missionID != "m4" {
			t.Fatalf("unexpected mission args: idx=%d id=%s", missionIndex, missionID)
		}
		return campaign.FlowRunV1{
			FlowID:     flow.FlowID,
			RunnerType: flow.Runner.Type,
			SuiteFile:  flow.SuiteFile,
			OK:         true,
			Attempts: []campaign.AttemptStatusV1{{
				MissionIndex: missionIndex,
				MissionID:    missionID,
				AttemptID:    "001-m4-r1",
				AttemptDir:   "/tmp/a",
				Status:       campaign.AttemptStatusValid,
			}},
		}, nil
	})
	if err != nil {
		t.Fatalf("NewCampaignExecutor: %v", err)
	}

	types := []string{campaign.RunnerTypeProcessCmd, campaign.RunnerTypeCodexExec, campaign.RunnerTypeCodexSub, campaign.RunnerTypeClaudeSub}
	for _, typ := range types {
		flow := campaign.FlowSpec{
			FlowID:    "f-" + typ,
			SuiteFile: "/tmp/suite.json",
			Runner: campaign.RunnerAdapterSpec{
				Type:    typ,
				Command: []string{"echo", "ok"},
			},
		}
		if err := exec.Prepare(context.Background(), flow); err != nil {
			t.Fatalf("Prepare(%s): %v", typ, err)
		}
		res, err := exec.RunMission(context.Background(), flow, 3, "m4")
		if err != nil {
			t.Fatalf("RunMission(%s): %v", typ, err)
		}
		if len(res.Attempts) != 1 || res.Attempts[0].AttemptDir == "" || res.Attempts[0].Status == "" {
			t.Fatalf("normalized output contract violated for %s: %+v", typ, res)
		}
		if err := exec.Cleanup(context.Background(), flow); err != nil {
			t.Fatalf("Cleanup(%s): %v", typ, err)
		}
	}
	if invocations != len(types) {
		t.Fatalf("expected %d invocations, got %d", len(types), invocations)
	}
}

func TestCampaignExecutor_UnsupportedTypeFails(t *testing.T) {
	exec, err := NewCampaignExecutor(func(_ context.Context, _ campaign.FlowSpec, _ int, _ string) (campaign.FlowRunV1, error) {
		return campaign.FlowRunV1{}, nil
	})
	if err != nil {
		t.Fatalf("NewCampaignExecutor: %v", err)
	}
	flow := campaign.FlowSpec{FlowID: "x", Runner: campaign.RunnerAdapterSpec{Type: "bogus"}}
	if _, err := exec.RunMission(context.Background(), flow, 0, "m1"); err == nil {
		t.Fatalf("expected unsupported runner type failure")
	}
}

func TestCampaignExecutor_InvokerErrorPreservesNormalizedContract(t *testing.T) {
	exec, err := NewCampaignExecutor(func(_ context.Context, _ campaign.FlowSpec, _ int, _ string) (campaign.FlowRunV1, error) {
		return campaign.FlowRunV1{Attempts: []campaign.AttemptStatusV1{{}}}, errors.New("boom")
	})
	if err != nil {
		t.Fatalf("NewCampaignExecutor: %v", err)
	}
	flow := campaign.FlowSpec{FlowID: "f", Runner: campaign.RunnerAdapterSpec{Type: campaign.RunnerTypeProcessCmd}}
	res, err := exec.RunMission(context.Background(), flow, 0, "m1")
	if err == nil {
		t.Fatalf("expected invoker error")
	}
	if len(res.Attempts) != 1 || res.Attempts[0].Status == "" {
		t.Fatalf("expected normalized attempt status on error: %+v", res)
	}
}
