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

	"github.com/marcohefti/zero-context-lab/internal/codes"
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
	// FailureBuckets split failed attempts into mutually-exclusive classes.
	FailureBuckets FailureBucketsV1 `json:"failureBuckets"`

	UpdatedAt string `json:"updatedAt"`
}

type SummaryV1 struct {
	SchemaVersion int      `json:"schemaVersion"`
	CampaignID    string   `json:"campaignId"`
	RunID         string   `json:"runId"`
	Status        string   `json:"status"`
	ReasonCodes   []string `json:"reasonCodes,omitempty"`
	UpdatedAt     string   `json:"updatedAt"`

	TotalMissions     int `json:"totalMissions"`
	MissionsCompleted int `json:"missionsCompleted"`
	GatesPassed       int `json:"gatesPassed"`
	GatesFailed       int `json:"gatesFailed"`

	ClaimedMissionsOK  int `json:"claimedMissionsOk"`
	VerifiedMissionsOK int `json:"verifiedMissionsOk"`
	MismatchCount      int `json:"mismatchCount"`

	TopFailureCodes []CodeCountV1      `json:"topFailureCodes,omitempty"`
	FailureBuckets  FailureBucketsV1   `json:"failureBuckets"`
	Missions        []MissionSummaryV1 `json:"missions,omitempty"`
	EvidencePaths   SummaryEvidenceV1  `json:"evidencePaths"`
	Flows           []FlowReportV1     `json:"flows,omitempty"`
}

type FailureBucketsV1 struct {
	InfraFailed   int `json:"infraFailed"`
	OracleFailed  int `json:"oracleFailed"`
	MissionFailed int `json:"missionFailed"`
}

type CodeCountV1 struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type MissionSummaryV1 struct {
	MissionIndex int                    `json:"missionIndex"`
	MissionID    string                 `json:"missionId"`
	ClaimedOK    bool                   `json:"claimedOk"`
	VerifiedOK   bool                   `json:"verifiedOk"`
	Mismatch     bool                   `json:"mismatch"`
	Reasons      []string               `json:"reasons,omitempty"`
	Flows        []MissionFlowSummaryV1 `json:"flows,omitempty"`
}

type MissionFlowSummaryV1 struct {
	FlowID     string   `json:"flowId"`
	Status     string   `json:"status"`
	AttemptID  string   `json:"attemptId,omitempty"`
	AttemptDir string   `json:"attemptDir,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

type SummaryEvidenceV1 struct {
	RunStatePath  string   `json:"runStatePath"`
	ReportPath    string   `json:"reportPath"`
	SummaryPath   string   `json:"summaryPath"`
	ResultsMDPath string   `json:"resultsMdPath"`
	AttemptDirs   []string `json:"attemptDirs,omitempty"`
}

type FlowReportV1 struct {
	FlowID        string `json:"flowId"`
	RunnerType    string `json:"runnerType"`
	AttemptsTotal int    `json:"attemptsTotal"`
	Valid         int    `json:"valid"`
	Invalid       int    `json:"invalid"`
	Skipped       int    `json:"skipped"`
	InfraFailed   int    `json:"infraFailed"`
	OracleFailed  int    `json:"oracleFailed"`
	MissionFailed int    `json:"missionFailed"`
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

func SummaryPath(outRoot string, campaignID string) string {
	return filepath.Join(CampaignDir(outRoot, campaignID), "campaign.summary.json")
}

func ResultsMDPath(outRoot string, campaignID string) string {
	return filepath.Join(CampaignDir(outRoot, campaignID), "RESULTS.md")
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
			bucket := classifyAttemptFailureBucket(ar)
			cur.AttemptsTotal++
			switch ar.Status {
			case AttemptStatusValid:
				cur.Valid++
			case AttemptStatusSkipped:
				cur.Skipped++
			case AttemptStatusInfraFailed:
				// Keep status counters backward-compatible; bucket counters track infra failures.
			default:
				cur.Invalid++
			}
			switch bucket {
			case failureBucketInfra:
				cur.InfraFailed++
				rep.FailureBuckets.InfraFailed++
			case failureBucketOracle:
				cur.OracleFailed++
				rep.FailureBuckets.OracleFailed++
			case failureBucketMission:
				cur.MissionFailed++
				rep.FailureBuckets.MissionFailed++
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

func BuildSummary(st RunStateV1) SummaryV1 {
	rep := BuildReport(st)
	out := SummaryV1{
		SchemaVersion:     1,
		CampaignID:        st.CampaignID,
		RunID:             st.RunID,
		Status:            st.Status,
		ReasonCodes:       normalizeReasonCodes(st.ReasonCodes),
		UpdatedAt:         st.UpdatedAt,
		TotalMissions:     rep.TotalMissions,
		MissionsCompleted: rep.MissionsCompleted,
		GatesPassed:       rep.GatesPassed,
		GatesFailed:       rep.GatesFailed,
		FailureBuckets:    rep.FailureBuckets,
		Flows:             rep.Flows,
		EvidencePaths: SummaryEvidenceV1{
			RunStatePath:  RunStatePath(st.OutRoot, st.CampaignID),
			ReportPath:    ReportPath(st.OutRoot, st.CampaignID),
			SummaryPath:   SummaryPath(st.OutRoot, st.CampaignID),
			ResultsMDPath: ResultsMDPath(st.OutRoot, st.CampaignID),
		},
	}
	attemptByFlowMission := map[string]map[int]AttemptStatusV1{}
	attemptDirs := map[string]bool{}
	for _, fr := range st.FlowRuns {
		if _, ok := attemptByFlowMission[fr.FlowID]; !ok {
			attemptByFlowMission[fr.FlowID] = map[int]AttemptStatusV1{}
		}
		for _, a := range fr.Attempts {
			attemptByFlowMission[fr.FlowID][a.MissionIndex] = a
			if strings.TrimSpace(a.AttemptDir) != "" {
				attemptDirs[a.AttemptDir] = true
			}
		}
	}
	failures := map[string]int{}
	for _, mg := range st.MissionGates {
		ms := MissionSummaryV1{
			MissionIndex: mg.MissionIndex,
			MissionID:    mg.MissionID,
			VerifiedOK:   mg.OK,
			Reasons:      normalizeReasonCodes(mg.Reasons),
		}
		if ms.VerifiedOK {
			out.VerifiedMissionsOK++
		}
		claimedAll := len(st.FlowRuns) > 0
		for _, fr := range st.FlowRuns {
			a, ok := attemptByFlowMission[fr.FlowID][mg.MissionIndex]
			if !ok {
				claimedAll = false
				ms.Flows = append(ms.Flows, MissionFlowSummaryV1{
					FlowID: fr.FlowID,
					Status: AttemptStatusInvalid,
					Errors: []string{codes.CampaignMissingAttempt},
				})
				failures[codes.CampaignMissingAttempt]++
				continue
			}
			if a.Status != AttemptStatusValid {
				claimedAll = false
			}
			ms.Flows = append(ms.Flows, MissionFlowSummaryV1{
				FlowID:     fr.FlowID,
				Status:     a.Status,
				AttemptID:  a.AttemptID,
				AttemptDir: a.AttemptDir,
				Errors:     normalizeReasonCodes(a.Errors),
			})
			for _, code := range a.Errors {
				failures[strings.TrimSpace(code)]++
			}
		}
		ms.ClaimedOK = claimedAll
		if ms.ClaimedOK {
			out.ClaimedMissionsOK++
		}
		ms.Mismatch = ms.ClaimedOK != ms.VerifiedOK
		if ms.Mismatch {
			out.MismatchCount++
		}
		for _, code := range ms.Reasons {
			failures[strings.TrimSpace(code)]++
		}
		sort.Slice(ms.Flows, func(i, j int) bool { return ms.Flows[i].FlowID < ms.Flows[j].FlowID })
		out.Missions = append(out.Missions, ms)
	}
	sort.Slice(out.Missions, func(i, j int) bool {
		if out.Missions[i].MissionIndex != out.Missions[j].MissionIndex {
			return out.Missions[i].MissionIndex < out.Missions[j].MissionIndex
		}
		return out.Missions[i].MissionID < out.Missions[j].MissionID
	})
	out.TopFailureCodes = sortCodeCounts(failures)
	for dir := range attemptDirs {
		out.EvidencePaths.AttemptDirs = append(out.EvidencePaths.AttemptDirs, dir)
	}
	sort.Strings(out.EvidencePaths.AttemptDirs)
	return out
}

func sortCodeCounts(in map[string]int) []CodeCountV1 {
	if len(in) == 0 {
		return nil
	}
	out := make([]CodeCountV1, 0, len(in))
	for code, count := range in {
		code = strings.TrimSpace(code)
		if code == "" || count <= 0 {
			continue
		}
		out = append(out, CodeCountV1{Code: code, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Code < out[j].Code
	})
	return out
}

const (
	failureBucketNone    = ""
	failureBucketInfra   = "infra"
	failureBucketOracle  = "oracle"
	failureBucketMission = "mission"
)

func classifyAttemptFailureBucket(ar AttemptStatusV1) string {
	if ar.Status == AttemptStatusValid || ar.Status == AttemptStatusSkipped {
		return failureBucketNone
	}
	if ar.Status == AttemptStatusInfraFailed || isInfraAttemptStatus(ar) {
		return failureBucketInfra
	}
	if hasOracleFailureCode(ar.Errors) {
		return failureBucketOracle
	}
	return failureBucketMission
}

func isInfraAttemptStatus(ar AttemptStatusV1) bool {
	if isInfraCode(ar.RunnerErrorCode) || isInfraCode(ar.AutoFeedbackCode) {
		return true
	}
	for _, code := range ar.Errors {
		if isInfraCode(code) {
			return true
		}
	}
	return false
}

func hasOracleFailureCode(in []string) bool {
	for _, code := range in {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if strings.HasPrefix(code, campaignOracleCodePrefix()) {
			return true
		}
	}
	return false
}

func isInfraCode(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	if strings.HasPrefix(code, runtimeCodePrefix()) {
		return true
	}
	switch code {
	case codes.Timeout, codes.Spawn, codes.ToolFailed, codes.IO, codes.MissingArtifact:
		return true
	default:
		return false
	}
}

func campaignOracleCodePrefix() string {
	return strings.TrimSuffix(codes.CampaignOracleEvaluatorMissing, "EVALUATOR_REQUIRED")
}

func runtimeCodePrefix() string {
	return strings.TrimSuffix(codes.RuntimeTimeout, "TIMEOUT")
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
