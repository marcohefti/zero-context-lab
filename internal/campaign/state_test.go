package campaign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateStateAppendsAndUpdatesLatest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "campaign.state.json")

	now := time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC)
	st, err := UpdateState(path, UpdateInput{
		Now:              now,
		CampaignID:       "cmp-1",
		SuiteID:          "suite-a",
		RunID:            "run-1",
		CreatedAt:        now.Format(time.RFC3339Nano),
		Mode:             "discovery",
		OutRoot:          ".zcl",
		SessionIsolation: "process_runner",
		ComparabilityKey: "cp-1",
		FeedbackPolicy:   "auto_fail",
		Parallel:         1,
		Total:            2,
		FailFast:         true,
		Passed:           2,
		Failed:           0,
	})
	if err != nil {
		t.Fatalf("UpdateState first: %v", err)
	}
	if st.LatestRunID != "run-1" || len(st.Runs) != 1 {
		t.Fatalf("unexpected state after first update: %+v", st)
	}

	now2 := now.Add(1 * time.Hour)
	st, err = UpdateState(path, UpdateInput{
		Now:              now2,
		CampaignID:       "cmp-1",
		SuiteID:          "suite-a",
		RunID:            "run-2",
		CreatedAt:        now2.Format(time.RFC3339Nano),
		Mode:             "ci",
		OutRoot:          ".zcl",
		SessionIsolation: "native_spawn",
		ComparabilityKey: "cp-2",
		FeedbackPolicy:   "strict",
		Parallel:         2,
		Total:            4,
		FailFast:         false,
		Passed:           3,
		Failed:           1,
	})
	if err != nil {
		t.Fatalf("UpdateState second: %v", err)
	}
	if st.LatestRunID != "run-2" || len(st.Runs) != 2 {
		t.Fatalf("unexpected state after second update: %+v", st)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var got StateV1
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal state file: %v", err)
	}
	if got.SchemaVersion != 1 || got.CampaignID != "cmp-1" || got.SuiteID != "suite-a" {
		t.Fatalf("unexpected persisted state: %+v", got)
	}
}
