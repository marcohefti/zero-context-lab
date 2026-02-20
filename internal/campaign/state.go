package campaign

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/store"
)

type StateV1 struct {
	SchemaVersion int            `json:"schemaVersion"`
	CampaignID    string         `json:"campaignId"`
	SuiteID       string         `json:"suiteId"`
	UpdatedAt     string         `json:"updatedAt"`
	LatestRunID   string         `json:"latestRunId"`
	Runs          []RunSummaryV1 `json:"runs"`
}

type RunSummaryV1 struct {
	RunID            string `json:"runId"`
	CreatedAt        string `json:"createdAt"`
	Mode             string `json:"mode"`
	OutRoot          string `json:"outRoot"`
	SessionIsolation string `json:"sessionIsolation"`
	ComparabilityKey string `json:"comparabilityKey"`
	FeedbackPolicy   string `json:"feedbackPolicy"`
	Parallel         int    `json:"parallel"`
	Total            int    `json:"total"`
	FailFast         bool   `json:"failFast"`
	Passed           int    `json:"passed"`
	Failed           int    `json:"failed"`
}

type UpdateInput struct {
	Now              time.Time
	CampaignID       string
	SuiteID          string
	RunID            string
	CreatedAt        string
	Mode             string
	OutRoot          string
	SessionIsolation string
	ComparabilityKey string
	FeedbackPolicy   string
	Parallel         int
	Total            int
	FailFast         bool
	Passed           int
	Failed           int
}

func DefaultStatePath(outRoot string, campaignID string) string {
	return filepath.Join(outRoot, "campaigns", campaignID, "campaign.state.json")
}

func UpdateState(path string, in UpdateInput) (StateV1, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return StateV1{}, fmt.Errorf("missing campaign state path")
	}
	if strings.TrimSpace(in.CampaignID) == "" || strings.TrimSpace(in.SuiteID) == "" || strings.TrimSpace(in.RunID) == "" {
		return StateV1{}, fmt.Errorf("campaign update requires campaignId, suiteId, runId")
	}

	st := StateV1{
		SchemaVersion: 1,
		CampaignID:    strings.TrimSpace(in.CampaignID),
		SuiteID:       strings.TrimSpace(in.SuiteID),
	}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &st); err != nil {
			return StateV1{}, err
		}
		if st.SchemaVersion != 1 {
			return StateV1{}, fmt.Errorf("unsupported campaign.state schemaVersion")
		}
		if st.CampaignID != strings.TrimSpace(in.CampaignID) {
			return StateV1{}, fmt.Errorf("campaignId mismatch in campaign.state")
		}
		if st.SuiteID != strings.TrimSpace(in.SuiteID) {
			return StateV1{}, fmt.Errorf("suiteId mismatch in campaign.state")
		}
	} else if !os.IsNotExist(err) {
		return StateV1{}, err
	}

	run := RunSummaryV1{
		RunID:            strings.TrimSpace(in.RunID),
		CreatedAt:        strings.TrimSpace(in.CreatedAt),
		Mode:             strings.TrimSpace(in.Mode),
		OutRoot:          strings.TrimSpace(in.OutRoot),
		SessionIsolation: strings.TrimSpace(in.SessionIsolation),
		ComparabilityKey: strings.TrimSpace(in.ComparabilityKey),
		FeedbackPolicy:   strings.TrimSpace(in.FeedbackPolicy),
		Parallel:         in.Parallel,
		Total:            in.Total,
		FailFast:         in.FailFast,
		Passed:           in.Passed,
		Failed:           in.Failed,
	}
	upserted := false
	for i := range st.Runs {
		if st.Runs[i].RunID == run.RunID {
			st.Runs[i] = run
			upserted = true
			break
		}
	}
	if !upserted {
		st.Runs = append(st.Runs, run)
	}
	sort.Slice(st.Runs, func(i, j int) bool {
		ti, _ := parseTS(st.Runs[i].CreatedAt)
		tj, _ := parseTS(st.Runs[j].CreatedAt)
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return st.Runs[i].RunID < st.Runs[j].RunID
	})
	st.SchemaVersion = 1
	st.CampaignID = strings.TrimSpace(in.CampaignID)
	st.SuiteID = strings.TrimSpace(in.SuiteID)
	st.UpdatedAt = in.Now.UTC().Format(time.RFC3339Nano)
	st.LatestRunID = latestRunID(st.Runs)

	if err := store.WriteJSONAtomic(path, st); err != nil {
		return StateV1{}, err
	}
	return st, nil
}

func latestRunID(runs []RunSummaryV1) string {
	var (
		out string
		max time.Time
	)
	for _, r := range runs {
		ts, ok := parseTS(r.CreatedAt)
		if ok {
			if max.IsZero() || ts.After(max) {
				max = ts
				out = r.RunID
			}
			continue
		}
		if out == "" || r.RunID > out {
			out = r.RunID
		}
	}
	return out
}

func parseTS(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err == nil {
		return ts, true
	}
	ts, err = time.Parse(time.RFC3339, s)
	if err == nil {
		return ts, true
	}
	return time.Time{}, false
}
