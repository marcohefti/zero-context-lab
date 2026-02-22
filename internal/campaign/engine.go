package campaign

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/store"
)

const (
	ReasonLockTimeout   = "ZCL_E_CAMPAIGN_LOCK_TIMEOUT"
	ReasonHookFailed    = "ZCL_E_CAMPAIGN_HOOK_FAILED"
	ReasonGlobalTimeout = "ZCL_E_CAMPAIGN_GLOBAL_TIMEOUT"
)

type MissionExecutor interface {
	Prepare(ctx context.Context, flow FlowSpec) error
	RunMission(ctx context.Context, flow FlowSpec, missionIndex int, missionID string) (FlowRunV1, error)
	Cleanup(ctx context.Context, flow FlowSpec) error
}

type GateEvaluator func(parsed ParsedSpec, missionIndex int, missionID string, missionFlowRuns []FlowRunV1) (MissionGateV1, error)

type HookExecutor func(ctx context.Context, command string) error

type EngineOptions struct {
	OutRoot              string
	RunID                string
	Canary               bool
	ResumedFromRunID     string
	MissionIndexes       []int
	MissionOffset        int
	TotalMissions        int
	GlobalTimeoutMs      int64
	CleanupHookTimeoutMs int64
	LockWait             time.Duration
	Now                  func() time.Time
}

type EngineResult struct {
	State RunStateV1
	Exit  int
}

func ExecuteMissionEngine(parsed ParsedSpec, exec MissionExecutor, evalGate GateEvaluator, runHook HookExecutor, opts EngineOptions) (EngineResult, error) {
	if exec == nil {
		return EngineResult{}, fmt.Errorf("missing mission executor")
	}
	if evalGate == nil {
		return EngineResult{}, fmt.Errorf("missing gate evaluator")
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.LockWait <= 0 {
		opts.LockWait = 5 * time.Second
	}
	lockPath := LockPath(opts.OutRoot, parsed.Spec.CampaignID)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return EngineResult{}, err
	}
	var out EngineResult
	lockErr := store.WithDirLock(lockPath, opts.LockWait, func() error {
		result, err := executeMissionEngineLocked(parsed, exec, evalGate, runHook, opts)
		if err == nil {
			out = result
		}
		return err
	})
	if lockErr != nil {
		st := RunStateV1{
			SchemaVersion: 1,
			CampaignID:    parsed.Spec.CampaignID,
			RunID:         opts.RunID,
			OutRoot:       opts.OutRoot,
			Status:        RunStatusAborted,
			ReasonCodes:   []string{ReasonLockTimeout, ReasonAborted},
			StartedAt:     opts.Now().Format(time.RFC3339Nano),
			UpdatedAt:     opts.Now().Format(time.RFC3339Nano),
			CompletedAt:   opts.Now().Format(time.RFC3339Nano),
		}
		return EngineResult{State: st, Exit: 1}, nil
	}
	return out, nil
}

func executeMissionEngineLocked(parsed ParsedSpec, exec MissionExecutor, evalGate GateEvaluator, runHook HookExecutor, opts EngineOptions) (EngineResult, error) {
	now := opts.Now()
	state := RunStateV1{
		SchemaVersion:     1,
		CampaignID:        parsed.Spec.CampaignID,
		RunID:             opts.RunID,
		SpecPath:          parsed.SpecPath,
		OutRoot:           opts.OutRoot,
		Status:            RunStatusRunning,
		StartedAt:         now.Format(time.RFC3339Nano),
		UpdatedAt:         now.Format(time.RFC3339Nano),
		Canary:            opts.Canary,
		ResumedFromRunID:  strings.TrimSpace(opts.ResumedFromRunID),
		MissionOffset:     opts.MissionOffset,
		MissionsCompleted: 0,
	}
	statePath := RunStatePath(opts.OutRoot, parsed.Spec.CampaignID)
	planPath := PlanPath(opts.OutRoot, parsed.Spec.CampaignID)
	progressPath := ProgressPath(opts.OutRoot, parsed.Spec.CampaignID)

	plan, err := ensurePlan(planPath, parsed, opts.Now)
	if err != nil {
		return EngineResult{}, err
	}
	completed, seenKeys, err := completedMissionIndex(progressPath)
	if err != nil {
		return EngineResult{}, err
	}

	selected := normalizeMissionIndexes(opts.MissionIndexes, parsed)
	if opts.MissionOffset > 0 || opts.TotalMissions > 0 {
		selected, err = WindowMissionIndexes(selected, opts.MissionOffset, opts.TotalMissions)
		if err != nil {
			return EngineResult{}, err
		}
	}
	pending := make([]int, 0, len(selected))
	for _, idx := range selected {
		if !completed[idx] {
			pending = append(pending, idx)
		}
	}
	state.TotalMissions = len(pending)
	if err := SaveRunState(statePath, state); err != nil {
		return EngineResult{}, err
	}

	deadline := time.Time{}
	if opts.GlobalTimeoutMs > 0 {
		deadline = now.Add(time.Duration(opts.GlobalTimeoutMs) * time.Millisecond)
	}
	appendLifecycle := func(missionIndex int, missionID string, status string, reasonCodes []string) {
		_ = AppendProgress(progressPath, ProgressEventV1{
			SchemaVersion: 1,
			CampaignID:    parsed.Spec.CampaignID,
			RunID:         state.RunID,
			MissionIndex:  missionIndex,
			MissionID:     missionID,
			Status:        status,
			ReasonCodes:   normalizeReasonCodes(reasonCodes),
			CreatedAt:     opts.Now().Format(time.RFC3339Nano),
		})
	}
	runFailureHooks := func(missionIndex int, missionID string, reasons []string) {
		if len(parsed.Spec.Cleanup.OnFailure) == 0 {
			return
		}
		appendLifecycle(missionIndex, missionID, "cleanup_on_failure_start", reasons)
		if err := runHookList(runHook, parsed.Spec.Cleanup.OnFailure, opts.CleanupHookTimeoutMs); err != nil {
			appendLifecycle(missionIndex, missionID, "cleanup_on_failure_fail", append(reasons, ReasonHookFailed))
			return
		}
		appendLifecycle(missionIndex, missionID, "cleanup_on_failure_ok", reasons)
	}

	for _, flow := range parsed.Spec.Flows {
		if err := exec.Prepare(context.Background(), flow); err != nil {
			state.Status = RunStatusAborted
			state.ReasonCodes = normalizeReasonCodes([]string{ReasonFlowFailed, ReasonAborted})
			state.CompletedAt = opts.Now().Format(time.RFC3339Nano)
			state.UpdatedAt = state.CompletedAt
			_ = SaveRunState(statePath, state)
			return EngineResult{State: state, Exit: 1}, nil
		}
	}

	for _, missionIndex := range pending {
		mission := planMissionByIndex(plan, missionIndex)
		if mission == nil {
			state.Status = RunStatusAborted
			state.ReasonCodes = normalizeReasonCodes([]string{ReasonFlowFailed, ReasonAborted})
			state.CompletedAt = opts.Now().Format(time.RFC3339Nano)
			state.UpdatedAt = state.CompletedAt
			_ = SaveRunState(statePath, state)
			return EngineResult{State: state, Exit: 1}, nil
		}
		if !deadline.IsZero() && opts.Now().After(deadline) {
			runFailureHooks(missionIndex, mission.MissionID, []string{ReasonGlobalTimeout, ReasonAborted})
			state.Status = RunStatusAborted
			state.ReasonCodes = normalizeReasonCodes(append(state.ReasonCodes, ReasonGlobalTimeout, ReasonAborted))
			state.CompletedAt = opts.Now().Format(time.RFC3339Nano)
			state.UpdatedAt = state.CompletedAt
			_ = SaveRunState(statePath, state)
			return EngineResult{State: state, Exit: 2}, nil
		}

		if len(parsed.Spec.Cleanup.BeforeMission) > 0 {
			appendLifecycle(missionIndex, mission.MissionID, "cleanup_before_mission_start", nil)
		}
		if err := runHookList(runHook, parsed.Spec.Cleanup.BeforeMission, opts.CleanupHookTimeoutMs); err != nil {
			appendLifecycle(missionIndex, mission.MissionID, "cleanup_before_mission_fail", []string{ReasonHookFailed})
			runFailureHooks(missionIndex, mission.MissionID, []string{ReasonHookFailed, ReasonAborted})
			state.Status = RunStatusAborted
			state.ReasonCodes = normalizeReasonCodes(append(state.ReasonCodes, ReasonHookFailed, ReasonAborted))
			state.CompletedAt = opts.Now().Format(time.RFC3339Nano)
			state.UpdatedAt = state.CompletedAt
			_ = SaveRunState(statePath, state)
			return EngineResult{State: state, Exit: 1}, nil
		}
		if len(parsed.Spec.Cleanup.BeforeMission) > 0 {
			appendLifecycle(missionIndex, mission.MissionID, "cleanup_before_mission_ok", nil)
		}

		runFlow := func(flow FlowSpec) FlowRunV1 {
			result, runErr := exec.RunMission(context.Background(), flow, missionIndex, mission.MissionID)
			if runErr != nil {
				result.FlowID = flow.FlowID
				result.RunnerType = flow.Runner.Type
				result.SuiteFile = flow.SuiteFile
				result.OK = false
				if len(result.Errors) == 0 {
					result.Errors = []string{ReasonFlowFailed}
				}
			}
			return result
		}
		missionRuns := make([]FlowRunV1, len(parsed.Spec.Flows))
		if parsed.Spec.Execution.FlowMode == FlowModeParallel {
			var wg sync.WaitGroup
			for i := range parsed.Spec.Flows {
				i := i
				wg.Add(1)
				go func() {
					defer wg.Done()
					missionRuns[i] = runFlow(parsed.Spec.Flows[i])
				}()
			}
			wg.Wait()
		} else {
			for i := range parsed.Spec.Flows {
				missionRuns[i] = runFlow(parsed.Spec.Flows[i])
			}
		}
		for i := range missionRuns {
			flow := parsed.Spec.Flows[i]
			result := &missionRuns[i]
			if len(result.Attempts) == 0 {
				continue
			}
			for j := range result.Attempts {
				idempotency := progressKey(parsed.Spec.CampaignID, flow.FlowID, missionIndex)
				if seenKeys[idempotency] {
					result.Attempts[j].Errors = normalizeReasonCodes(append(result.Attempts[j].Errors, "ZCL_E_CAMPAIGN_DUPLICATE_ATTEMPT"))
					if result.Attempts[j].Status == AttemptStatusValid {
						result.Attempts[j].Status = AttemptStatusInvalid
					}
				} else {
					seenKeys[idempotency] = true
				}
				_ = AppendProgress(progressPath, ProgressEventV1{
					SchemaVersion:  1,
					CampaignID:     parsed.Spec.CampaignID,
					RunID:          state.RunID,
					MissionIndex:   missionIndex,
					MissionID:      mission.MissionID,
					FlowID:         flow.FlowID,
					AttemptID:      result.Attempts[j].AttemptID,
					AttemptDir:     result.Attempts[j].AttemptDir,
					Status:         result.Attempts[j].Status,
					ReasonCodes:    result.Attempts[j].Errors,
					IdempotencyKey: idempotency,
					CreatedAt:      opts.Now().Format(time.RFC3339Nano),
				})
			}
		}

		gate, err := evalGate(parsed, missionIndex, mission.MissionID, missionRuns)
		if err != nil {
			runFailureHooks(missionIndex, mission.MissionID, []string{ReasonFlowFailed, ReasonAborted})
			state.Status = RunStatusAborted
			state.ReasonCodes = normalizeReasonCodes(append(state.ReasonCodes, ReasonFlowFailed, ReasonAborted))
			state.CompletedAt = opts.Now().Format(time.RFC3339Nano)
			state.UpdatedAt = state.CompletedAt
			_ = SaveRunState(statePath, state)
			return EngineResult{State: state, Exit: 1}, nil
		}
		state.FlowRuns = mergeCampaignFlowRuns(state.FlowRuns, missionRuns)
		state.MissionGates = append(state.MissionGates, gate)
		state.MissionsCompleted++
		state.UpdatedAt = opts.Now().Format(time.RFC3339Nano)
		_ = AppendProgress(progressPath, ProgressEventV1{
			SchemaVersion:  1,
			CampaignID:     parsed.Spec.CampaignID,
			RunID:          state.RunID,
			MissionIndex:   missionIndex,
			MissionID:      mission.MissionID,
			Status:         gateStatus(gate.OK),
			ReasonCodes:    gate.Reasons,
			IdempotencyKey: gateProgressKey(parsed.Spec.CampaignID, missionIndex),
			CreatedAt:      opts.Now().Format(time.RFC3339Nano),
		})
		if !gate.OK {
			runFailureHooks(missionIndex, mission.MissionID, append([]string{ReasonGateFailed}, gate.Reasons...))
		}

		if len(parsed.Spec.Cleanup.AfterMission) > 0 {
			appendLifecycle(missionIndex, mission.MissionID, "cleanup_after_mission_start", nil)
		}
		if err := runHookList(runHook, parsed.Spec.Cleanup.AfterMission, opts.CleanupHookTimeoutMs); err != nil {
			appendLifecycle(missionIndex, mission.MissionID, "cleanup_after_mission_fail", []string{ReasonHookFailed})
			runFailureHooks(missionIndex, mission.MissionID, []string{ReasonHookFailed, ReasonAborted})
			state.Status = RunStatusAborted
			state.ReasonCodes = normalizeReasonCodes(append(state.ReasonCodes, ReasonHookFailed, ReasonAborted))
			state.CompletedAt = opts.Now().Format(time.RFC3339Nano)
			state.UpdatedAt = state.CompletedAt
			_ = SaveRunState(statePath, state)
			return EngineResult{State: state, Exit: 1}, nil
		}
		if len(parsed.Spec.Cleanup.AfterMission) > 0 {
			appendLifecycle(missionIndex, mission.MissionID, "cleanup_after_mission_ok", nil)
		}

		if err := SaveRunState(statePath, state); err != nil {
			return EngineResult{}, err
		}
		if parsed.Spec.PairGate.StopOnFirstMissionFailure && state.MissionsCompleted == 1 && !gate.OK {
			state.Status = RunStatusAborted
			state.ReasonCodes = normalizeReasonCodes(append(state.ReasonCodes, ReasonFirstMissionGate, ReasonGateFailed, ReasonAborted))
			state.CompletedAt = opts.Now().Format(time.RFC3339Nano)
			state.UpdatedAt = state.CompletedAt
			_ = SaveRunState(statePath, state)
			return EngineResult{State: state, Exit: 2}, nil
		}
	}

	for _, flow := range parsed.Spec.Flows {
		_ = exec.Cleanup(context.Background(), flow)
	}

	state.Status = RunStatusValid
	for _, g := range state.MissionGates {
		if !g.OK {
			state.Status = RunStatusInvalid
			state.ReasonCodes = normalizeReasonCodes(append(state.ReasonCodes, ReasonGateFailed))
			break
		}
	}
	state.CompletedAt = opts.Now().Format(time.RFC3339Nano)
	state.UpdatedAt = state.CompletedAt
	if err := SaveRunState(statePath, state); err != nil {
		return EngineResult{}, err
	}
	if state.Status == RunStatusValid {
		return EngineResult{State: state, Exit: 0}, nil
	}
	return EngineResult{State: state, Exit: 2}, nil
}

func ensurePlan(path string, parsed ParsedSpec, now func() time.Time) (PlanV1, error) {
	if p, err := LoadPlan(path); err == nil {
		if strings.TrimSpace(p.SpecPath) == strings.TrimSpace(parsed.SpecPath) {
			return p, nil
		}
	}
	missions := make([]PlanMissionV1, 0, len(parsed.MissionIndexes))
	for _, idx := range parsed.MissionIndexes {
		if idx < 0 || idx >= len(parsed.BaseSuite.Suite.Missions) {
			return PlanV1{}, fmt.Errorf("campaign mission index out of range: %d", idx)
		}
		missions = append(missions, PlanMissionV1{MissionIndex: idx, MissionID: parsed.BaseSuite.Suite.Missions[idx].MissionID})
	}
	created := now().Format(time.RFC3339Nano)
	p := PlanV1{
		SchemaVersion: 1,
		CampaignID:    parsed.Spec.CampaignID,
		SpecPath:      parsed.SpecPath,
		Missions:      missions,
		CreatedAt:     created,
		UpdatedAt:     created,
	}
	if err := SavePlan(path, p); err != nil {
		return PlanV1{}, err
	}
	return p, nil
}

func completedMissionIndex(progressPath string) (map[int]bool, map[string]bool, error) {
	events, err := LoadProgress(progressPath)
	if err != nil {
		return nil, nil, err
	}
	completed := map[int]bool{}
	keys := map[string]bool{}
	for _, ev := range events {
		if strings.TrimSpace(ev.IdempotencyKey) != "" {
			keys[ev.IdempotencyKey] = true
		}
		if ev.Status == "gate_pass" || ev.Status == "gate_fail" {
			completed[ev.MissionIndex] = true
		}
	}
	return completed, keys, nil
}

func planMissionByIndex(plan PlanV1, missionIndex int) *PlanMissionV1 {
	for i := range plan.Missions {
		if plan.Missions[i].MissionIndex == missionIndex {
			return &plan.Missions[i]
		}
	}
	return nil
}

func normalizeMissionIndexes(in []int, parsed ParsedSpec) []int {
	if len(in) == 0 {
		out := make([]int, len(parsed.MissionIndexes))
		copy(out, parsed.MissionIndexes)
		return out
	}
	out := make([]int, 0, len(in))
	out = append(out, in...)
	return dedupeIntsStable(out)
}

func progressKey(campaignID string, flowID string, missionIndex int) string {
	return fmt.Sprintf("%s:%s:%d", strings.TrimSpace(campaignID), strings.TrimSpace(flowID), missionIndex)
}

func gateProgressKey(campaignID string, missionIndex int) string {
	return fmt.Sprintf("%s:gate:%d", strings.TrimSpace(campaignID), missionIndex)
}

func gateStatus(ok bool) string {
	if ok {
		return "gate_pass"
	}
	return "gate_fail"
}

func runHookList(runHook HookExecutor, commands []string, hookTimeoutMs int64) error {
	if runHook == nil || len(commands) == 0 {
		return nil
	}
	timeout := hookTimeoutMs
	if timeout <= 0 {
		timeout = 15000
	}
	for _, raw := range commands {
		cmd := strings.TrimSpace(raw)
		if cmd == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Millisecond)
		err := runHook(ctx, cmd)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func SortedMissionIndexes(indexes []int) []int {
	out := dedupeIntsStable(indexes)
	sort.Ints(out)
	return out
}

func mergeCampaignFlowRuns(existing []FlowRunV1, incoming []FlowRunV1) []FlowRunV1 {
	if len(incoming) == 0 {
		return existing
	}
	index := map[string]int{}
	for i := range existing {
		index[existing[i].FlowID] = i
	}
	for _, in := range incoming {
		i, ok := index[in.FlowID]
		if !ok {
			existing = append(existing, in)
			index[in.FlowID] = len(existing) - 1
			continue
		}
		cur := &existing[i]
		if in.RunID != "" {
			cur.RunID = in.RunID
		}
		cur.OK = cur.OK && in.OK
		if in.ExitCode != 0 {
			cur.ExitCode = in.ExitCode
		}
		if in.ErrorOutput != "" {
			cur.ErrorOutput = in.ErrorOutput
		}
		cur.Errors = normalizeReasonCodes(append(cur.Errors, in.Errors...))
		cur.Attempts = append(cur.Attempts, in.Attempts...)
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].FlowID < existing[j].FlowID })
	return existing
}
