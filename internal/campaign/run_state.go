package campaign

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/store"
)

const (
	AttemptStatusValid       = "valid"
	AttemptStatusInvalid     = "invalid"
	AttemptStatusSkipped     = "skipped"
	AttemptStatusInfraFailed = "infra_failed"
)

type RunStateV1 struct {
	SchemaVersion int    `json:"schemaVersion"`
	CampaignID    string `json:"campaignId"`
	RunID         string `json:"runId"`
	SpecPath      string `json:"specPath"`
	OutRoot       string `json:"outRoot"`

	Status      string   `json:"status"`
	ReasonCodes []string `json:"reasonCodes,omitempty"`

	StartedAt   string `json:"startedAt"`
	UpdatedAt   string `json:"updatedAt"`
	CompletedAt string `json:"completedAt,omitempty"`

	TotalMissions     int  `json:"totalMissions"`
	MissionOffset     int  `json:"missionOffset,omitempty"`
	MissionsCompleted int  `json:"missionsCompleted"`
	Canary            bool `json:"canary,omitempty"`

	ResumedFromRunID string `json:"resumedFromRunId,omitempty"`

	FlowRuns     []FlowRunV1     `json:"flowRuns,omitempty"`
	MissionGates []MissionGateV1 `json:"missionGates,omitempty"`
}

type FlowRunV1 struct {
	FlowID      string   `json:"flowId"`
	RunnerType  string   `json:"runnerType"`
	SuiteFile   string   `json:"suiteFile"`
	RunID       string   `json:"runId,omitempty"`
	ExitCode    int      `json:"exitCode"`
	OK          bool     `json:"ok"`
	ErrorOutput string   `json:"errorOutput,omitempty"`
	Errors      []string `json:"errors,omitempty"`

	Attempts []AttemptStatusV1 `json:"attempts,omitempty"`
}

type AttemptStatusV1 struct {
	MissionIndex int    `json:"missionIndex"`
	MissionID    string `json:"missionId"`
	AttemptID    string `json:"attemptId,omitempty"`
	AttemptDir   string `json:"attemptDir,omitempty"`
	RunnerRef    string `json:"runnerRef,omitempty"`

	Status           string `json:"status"`
	RunnerErrorCode  string `json:"runnerErrorCode,omitempty"`
	AutoFeedbackCode string `json:"autoFeedbackCode,omitempty"`

	Errors []string `json:"errors,omitempty"`
}

type MissionGateV1 struct {
	MissionIndex int      `json:"missionIndex"`
	MissionID    string   `json:"missionId"`
	OK           bool     `json:"ok"`
	Reasons      []string `json:"reasons,omitempty"`

	Attempts []MissionGateAttemptV1 `json:"attempts,omitempty"`
}

type MissionGateAttemptV1 struct {
	FlowID     string   `json:"flowId"`
	AttemptID  string   `json:"attemptId,omitempty"`
	AttemptDir string   `json:"attemptDir,omitempty"`
	Status     string   `json:"status"`
	OK         bool     `json:"ok"`
	Errors     []string `json:"errors,omitempty"`
}

type ReportV1 struct {
	SchemaVersion int      `json:"schemaVersion"`
	CampaignID    string   `json:"campaignId"`
	RunID         string   `json:"runId"`
	Status        string   `json:"status"`
	ReasonCodes   []string `json:"reasonCodes,omitempty"`
	OutRoot       string   `json:"outRoot,omitempty"`

	TotalMissions     int `json:"totalMissions"`
	MissionsCompleted int `json:"missionsCompleted"`
	GatesPassed       int `json:"gatesPassed"`
	GatesFailed       int `json:"gatesFailed"`

	Flows []FlowReportV1 `json:"flows,omitempty"`

	UpdatedAt string `json:"updatedAt"`
}

type FlowReportV1 struct {
	FlowID        string `json:"flowId"`
	RunnerType    string `json:"runnerType"`
	AttemptsTotal int    `json:"attemptsTotal"`
	Valid         int    `json:"valid"`
	Invalid       int    `json:"invalid"`
	Skipped       int    `json:"skipped"`
	InfraFailed   int    `json:"infraFailed"`
}

type PlanV1 struct {
	SchemaVersion int             `json:"schemaVersion"`
	CampaignID    string          `json:"campaignId"`
	SpecPath      string          `json:"specPath"`
	Missions      []PlanMissionV1 `json:"missions"`
	CreatedAt     string          `json:"createdAt"`
	UpdatedAt     string          `json:"updatedAt"`
}

type PlanMissionV1 struct {
	MissionIndex int    `json:"missionIndex"`
	MissionID    string `json:"missionId"`
}

type ProgressEventV1 struct {
	SchemaVersion  int      `json:"schemaVersion"`
	CampaignID     string   `json:"campaignId"`
	RunID          string   `json:"runId"`
	MissionIndex   int      `json:"missionIndex"`
	MissionID      string   `json:"missionId"`
	FlowID         string   `json:"flowId,omitempty"`
	AttemptID      string   `json:"attemptId,omitempty"`
	AttemptDir     string   `json:"attemptDir,omitempty"`
	Status         string   `json:"status"`
	ReasonCodes    []string `json:"reasonCodes,omitempty"`
	IdempotencyKey string   `json:"idempotencyKey,omitempty"`
	CreatedAt      string   `json:"createdAt"`
}

func CampaignDir(outRoot string, campaignID string) string {
	return filepath.Join(outRoot, "campaigns", campaignID)
}

func RunStatePath(outRoot string, campaignID string) string {
	return filepath.Join(CampaignDir(outRoot, campaignID), "campaign.run.state.json")
}

func ReportPath(outRoot string, campaignID string) string {
	return filepath.Join(CampaignDir(outRoot, campaignID), "campaign.report.json")
}

func PlanPath(outRoot string, campaignID string) string {
	return filepath.Join(CampaignDir(outRoot, campaignID), "campaign.plan.json")
}

func ProgressPath(outRoot string, campaignID string) string {
	return filepath.Join(CampaignDir(outRoot, campaignID), "campaign.progress.jsonl")
}

func LockPath(outRoot string, campaignID string) string {
	return filepath.Join(CampaignDir(outRoot, campaignID), "campaign.lock")
}

func LoadRunState(path string) (RunStateV1, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return RunStateV1{}, err
	}
	var st RunStateV1
	if err := json.Unmarshal(raw, &st); err != nil {
		return RunStateV1{}, err
	}
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 1
	}
	if st.SchemaVersion != 1 {
		return RunStateV1{}, fmt.Errorf("unsupported campaign.run.state schemaVersion")
	}
	return st, nil
}

func SaveRunState(path string, st RunStateV1) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("missing campaign run-state path")
	}
	if strings.TrimSpace(st.CampaignID) == "" {
		return fmt.Errorf("missing campaignId")
	}
	if strings.TrimSpace(st.RunID) == "" {
		return fmt.Errorf("missing runId")
	}
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 1
	}
	if st.SchemaVersion != 1 {
		return fmt.Errorf("unsupported campaign.run.state schemaVersion")
	}
	if !isValidRunStatus(st.Status) {
		return fmt.Errorf("invalid campaign status")
	}
	st.ReasonCodes = normalizeReasonCodes(st.ReasonCodes)
	if strings.TrimSpace(st.UpdatedAt) == "" {
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if st.TotalMissions < 0 || st.MissionsCompleted < 0 || st.MissionOffset < 0 {
		return fmt.Errorf("invalid mission counters")
	}

	sort.Slice(st.FlowRuns, func(i, j int) bool {
		return st.FlowRuns[i].FlowID < st.FlowRuns[j].FlowID
	})
	for i := range st.FlowRuns {
		sort.Slice(st.FlowRuns[i].Attempts, func(a, b int) bool {
			ai := st.FlowRuns[i].Attempts[a].MissionIndex
			bi := st.FlowRuns[i].Attempts[b].MissionIndex
			if ai != bi {
				return ai < bi
			}
			return st.FlowRuns[i].Attempts[a].MissionID < st.FlowRuns[i].Attempts[b].MissionID
		})
	}
	sort.Slice(st.MissionGates, func(i, j int) bool {
		if st.MissionGates[i].MissionIndex != st.MissionGates[j].MissionIndex {
			return st.MissionGates[i].MissionIndex < st.MissionGates[j].MissionIndex
		}
		return st.MissionGates[i].MissionID < st.MissionGates[j].MissionID
	})
	for i := range st.MissionGates {
		st.MissionGates[i].Reasons = normalizeReasonCodes(st.MissionGates[i].Reasons)
		sort.Slice(st.MissionGates[i].Attempts, func(a, b int) bool {
			return st.MissionGates[i].Attempts[a].FlowID < st.MissionGates[i].Attempts[b].FlowID
		})
	}

	return store.WriteJSONAtomic(path, st)
}

func BuildReport(st RunStateV1) ReportV1 {
	rep := ReportV1{
		SchemaVersion:     1,
		CampaignID:        st.CampaignID,
		RunID:             st.RunID,
		Status:            st.Status,
		ReasonCodes:       normalizeReasonCodes(st.ReasonCodes),
		OutRoot:           st.OutRoot,
		TotalMissions:     st.TotalMissions,
		MissionsCompleted: st.MissionsCompleted,
		UpdatedAt:         st.UpdatedAt,
	}
	byFlow := map[string]*FlowReportV1{}
	for _, fr := range st.FlowRuns {
		cur := &FlowReportV1{
			FlowID:     fr.FlowID,
			RunnerType: fr.RunnerType,
		}
		for _, ar := range fr.Attempts {
			cur.AttemptsTotal++
			switch ar.Status {
			case AttemptStatusValid:
				cur.Valid++
			case AttemptStatusSkipped:
				cur.Skipped++
			case AttemptStatusInfraFailed:
				cur.InfraFailed++
			default:
				cur.Invalid++
			}
		}
		byFlow[fr.FlowID] = cur
	}
	flowIDs := make([]string, 0, len(byFlow))
	for id := range byFlow {
		flowIDs = append(flowIDs, id)
	}
	sort.Strings(flowIDs)
	for _, id := range flowIDs {
		rep.Flows = append(rep.Flows, *byFlow[id])
	}

	for _, mg := range st.MissionGates {
		if mg.OK {
			rep.GatesPassed++
		} else {
			rep.GatesFailed++
		}
	}
	return rep
}

func normalizeReasonCodes(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, code := range in {
		code = strings.TrimSpace(code)
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func isValidRunStatus(v string) bool {
	switch strings.TrimSpace(v) {
	case RunStatusRunning, RunStatusValid, RunStatusInvalid, RunStatusAborted:
		return true
	default:
		return false
	}
}

func LoadPlan(path string) (PlanV1, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return PlanV1{}, err
	}
	var p PlanV1
	if err := json.Unmarshal(raw, &p); err != nil {
		return PlanV1{}, err
	}
	if p.SchemaVersion == 0 {
		p.SchemaVersion = 1
	}
	if p.SchemaVersion != 1 {
		return PlanV1{}, fmt.Errorf("unsupported campaign.plan schemaVersion")
	}
	return p, nil
}

func SavePlan(path string, p PlanV1) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("missing campaign plan path")
	}
	if strings.TrimSpace(p.CampaignID) == "" {
		return fmt.Errorf("missing campaignId")
	}
	if p.SchemaVersion == 0 {
		p.SchemaVersion = 1
	}
	if p.SchemaVersion != 1 {
		return fmt.Errorf("unsupported campaign.plan schemaVersion")
	}
	if len(p.Missions) == 0 {
		return fmt.Errorf("campaign plan requires at least one mission")
	}
	sort.Slice(p.Missions, func(i, j int) bool {
		if p.Missions[i].MissionIndex != p.Missions[j].MissionIndex {
			return p.Missions[i].MissionIndex < p.Missions[j].MissionIndex
		}
		return p.Missions[i].MissionID < p.Missions[j].MissionID
	})
	return store.WriteJSONAtomic(path, p)
}

func AppendProgress(path string, ev ProgressEventV1) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("missing campaign progress path")
	}
	if ev.SchemaVersion == 0 {
		ev.SchemaVersion = 1
	}
	if ev.SchemaVersion != 1 {
		return fmt.Errorf("unsupported campaign.progress schemaVersion")
	}
	if strings.TrimSpace(ev.CampaignID) == "" {
		return fmt.Errorf("missing campaignId")
	}
	if strings.TrimSpace(ev.RunID) == "" {
		return fmt.Errorf("missing runId")
	}
	if strings.TrimSpace(ev.Status) == "" {
		return fmt.Errorf("missing status")
	}
	ev.ReasonCodes = normalizeReasonCodes(ev.ReasonCodes)
	if strings.TrimSpace(ev.CreatedAt) == "" {
		ev.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return store.AppendJSONL(path, ev)
}

func LoadProgress(path string) ([]ProgressEventV1, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []ProgressEventV1
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev ProgressEventV1
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, err
		}
		if ev.SchemaVersion == 0 {
			ev.SchemaVersion = 1
		}
		if ev.SchemaVersion != 1 {
			return nil, fmt.Errorf("unsupported campaign.progress schemaVersion")
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
