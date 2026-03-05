package campaign

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/kernel/codes"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

const (
	ReasonLockTimeout   = codes.CampaignLockTimeout
	ReasonHookFailed    = codes.CampaignHookFailed
	ReasonGlobalTimeout = codes.CampaignGlobalTimeout
)

type MissionExecutor interface {
	Prepare(ctx context.Context, flow FlowSpec) error
	RunMission(ctx context.Context, flow FlowSpec, missionIndex int, missionID string) (FlowRunV1, error)
	Cleanup(ctx context.Context, flow FlowSpec) error
}

type GateEvaluator func(parsed ParsedSpec, missionIndex int, missionID string, missionFlowRuns []FlowRunV1) (MissionGateV1, error)

type HookExecutor func(ctx context.Context, command string) error

type EngineOptions struct {
	OutRoot                  string
	RunID                    string
	Canary                   bool
	ResumedFromRunID         string
	MissionIndexes           []int
	MissionOffset            int
	GlobalTimeoutMs          int64
	CleanupHookTimeoutMs     int64
	MissionEnvelopeMs        int64
	WatchdogHeartbeatMs      int64
	WatchdogHardKillContinue bool
	LockWait                 time.Duration
	Now                      func() time.Time
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
		if !store.IsLockTimeout(lockErr) {
			return EngineResult{}, lockErr
		}
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
	engine, err := newLockedEngine(parsed, exec, evalGate, runHook, opts)
	if err != nil {
		return EngineResult{}, err
	}
	if out, done := engine.prepareFlows(); done {
		return out, nil
	}
	for _, missionIndex := range engine.pending {
		out, done, err := engine.executeMission(missionIndex)
		if err != nil {
			return EngineResult{}, err
		}
		if done {
			return out, nil
		}
	}
	engine.cleanupFlows()
	return engine.finish()
}

type lockedEngine struct {
	parsed   ParsedSpec
	exec     MissionExecutor
	evalGate GateEvaluator
	runHook  HookExecutor
	opts     EngineOptions

	state        RunStateV1
	statePath    string
	progressPath string

	plan     PlanV1
	pending  []int
	seenKeys map[string]bool
	deadline time.Time
}

func newLockedEngine(parsed ParsedSpec, exec MissionExecutor, evalGate GateEvaluator, runHook HookExecutor, opts EngineOptions) (*lockedEngine, error) {
	now := opts.Now()
	e := &lockedEngine{
		parsed:       parsed,
		exec:         exec,
		evalGate:     evalGate,
		runHook:      runHook,
		opts:         opts,
		statePath:    RunStatePath(opts.OutRoot, parsed.Spec.CampaignID),
		progressPath: ProgressPath(opts.OutRoot, parsed.Spec.CampaignID),
		state: RunStateV1{
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
		},
	}
	plan, err := ensurePlan(PlanPath(opts.OutRoot, parsed.Spec.CampaignID), parsed, opts.Now)
	if err != nil {
		return nil, err
	}
	completed, seenKeys, err := completedMissionIndex(e.progressPath)
	if err != nil {
		return nil, err
	}
	e.plan = plan
	e.seenKeys = seenKeys
	e.pending = pendingMissionIndexes(normalizeMissionIndexes(opts.MissionIndexes, parsed), completed)
	e.state.TotalMissions = len(normalizeMissionIndexes(opts.MissionIndexes, parsed))
	if opts.GlobalTimeoutMs > 0 {
		e.deadline = now.Add(time.Duration(opts.GlobalTimeoutMs) * time.Millisecond)
	}
	if err := SaveRunState(e.statePath, e.state); err != nil {
		return nil, err
	}
	return e, nil
}

func pendingMissionIndexes(selected []int, completed map[int]bool) []int {
	out := make([]int, 0, len(selected))
	for _, idx := range selected {
		if !completed[idx] {
			out = append(out, idx)
		}
	}
	return out
}

func (e *lockedEngine) prepareFlows() (EngineResult, bool) {
	for _, flow := range e.parsed.Spec.Flows {
		if err := e.exec.Prepare(context.Background(), flow); err != nil {
			return e.abort([]string{ReasonFlowFailed, ReasonAborted}, 1), true
		}
	}
	return EngineResult{}, false
}

func (e *lockedEngine) executeMission(missionIndex int) (EngineResult, bool, error) {
	mission := planMissionByIndex(e.plan, missionIndex)
	if mission == nil {
		return e.abort([]string{ReasonFlowFailed, ReasonAborted}, 1), true, nil
	}
	if e.globalDeadlineExceeded() {
		e.runFailureHooks(missionIndex, mission.MissionID, []string{ReasonGlobalTimeout, ReasonAborted})
		return e.abort([]string{ReasonGlobalTimeout, ReasonAborted}, 2), true, nil
	}
	if out, done := e.runBeforeMissionHooks(missionIndex, mission.MissionID); done {
		return out, true, nil
	}

	missionRuns := e.executeMissionFlows(missionIndex, mission.MissionID)
	e.appendAttemptProgress(missionIndex, mission.MissionID, missionRuns)

	gate, err := e.evalGate(e.parsed, missionIndex, mission.MissionID, missionRuns)
	if err != nil {
		e.runFailureHooks(missionIndex, mission.MissionID, []string{ReasonFlowFailed, ReasonAborted})
		return e.abort([]string{ReasonFlowFailed, ReasonAborted}, 1), true, nil
	}
	e.recordGateResult(missionIndex, mission.MissionID, gate, missionRuns)

	if out, done := e.runAfterMissionHooks(missionIndex, mission.MissionID); done {
		return out, true, nil
	}
	if err := SaveRunState(e.statePath, e.state); err != nil {
		return EngineResult{}, false, err
	}
	if e.parsed.Spec.PairGate.StopOnFirstMissionFailure && e.state.MissionsCompleted == 1 && !gate.OK {
		return e.abort([]string{ReasonFirstMissionGate, ReasonGateFailed, ReasonAborted}, 2), true, nil
	}
	return EngineResult{}, false, nil
}

func (e *lockedEngine) globalDeadlineExceeded() bool {
	return !e.deadline.IsZero() && e.opts.Now().After(e.deadline)
}

func (e *lockedEngine) runBeforeMissionHooks(missionIndex int, missionID string) (EngineResult, bool) {
	if len(e.parsed.Spec.Cleanup.BeforeMission) > 0 {
		e.appendLifecycle(missionIndex, missionID, "cleanup_before_mission_start", nil)
	}
	if err := runHookList(e.runHook, e.parsed.Spec.Cleanup.BeforeMission, e.opts.CleanupHookTimeoutMs); err != nil {
		e.appendLifecycle(missionIndex, missionID, "cleanup_before_mission_fail", []string{ReasonHookFailed})
		e.runFailureHooks(missionIndex, missionID, []string{ReasonHookFailed, ReasonAborted})
		return e.abort([]string{ReasonHookFailed, ReasonAborted}, 1), true
	}
	if len(e.parsed.Spec.Cleanup.BeforeMission) > 0 {
		e.appendLifecycle(missionIndex, missionID, "cleanup_before_mission_ok", nil)
	}
	return EngineResult{}, false
}

func (e *lockedEngine) runAfterMissionHooks(missionIndex int, missionID string) (EngineResult, bool) {
	if len(e.parsed.Spec.Cleanup.AfterMission) > 0 {
		e.appendLifecycle(missionIndex, missionID, "cleanup_after_mission_start", nil)
	}
	if err := runHookList(e.runHook, e.parsed.Spec.Cleanup.AfterMission, e.opts.CleanupHookTimeoutMs); err != nil {
		e.appendLifecycle(missionIndex, missionID, "cleanup_after_mission_fail", []string{ReasonHookFailed})
		e.runFailureHooks(missionIndex, missionID, []string{ReasonHookFailed, ReasonAborted})
		return e.abort([]string{ReasonHookFailed, ReasonAborted}, 1), true
	}
	if len(e.parsed.Spec.Cleanup.AfterMission) > 0 {
		e.appendLifecycle(missionIndex, missionID, "cleanup_after_mission_ok", nil)
	}
	return EngineResult{}, false
}

func (e *lockedEngine) executeMissionFlows(missionIndex int, missionID string) []FlowRunV1 {
	missionRuns := make([]FlowRunV1, len(e.parsed.Spec.Flows))
	if e.parsed.Spec.Execution.FlowMode == FlowModeParallel {
		var wg sync.WaitGroup
		for i := range e.parsed.Spec.Flows {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				missionRuns[i] = e.executeSingleFlow(e.parsed.Spec.Flows[i], missionIndex, missionID)
			}()
		}
		wg.Wait()
		return missionRuns
	}
	for i := range e.parsed.Spec.Flows {
		missionRuns[i] = e.executeSingleFlow(e.parsed.Spec.Flows[i], missionIndex, missionID)
	}
	return missionRuns
}

type runOutcome struct {
	result FlowRunV1
	err    error
}

func (e *lockedEngine) executeSingleFlow(flow FlowSpec, missionIndex int, missionID string) FlowRunV1 {
	runCtx := context.Background()
	cancel := func() {}
	if e.opts.MissionEnvelopeMs > 0 {
		runCtx, cancel = context.WithTimeout(runCtx, time.Duration(e.opts.MissionEnvelopeMs)*time.Millisecond)
	}
	defer cancel()

	done := make(chan runOutcome, 1)
	go func() {
		res, err := e.exec.RunMission(runCtx, flow, missionIndex, missionID)
		done <- runOutcome{result: res, err: err}
	}()

	var heartbeat <-chan time.Time
	var heartbeatTicker *time.Ticker
	if e.opts.WatchdogHeartbeatMs > 0 {
		heartbeatTicker = time.NewTicker(time.Duration(e.opts.WatchdogHeartbeatMs) * time.Millisecond)
		heartbeat = heartbeatTicker.C
	}
	if heartbeatTicker != nil {
		defer heartbeatTicker.Stop()
	}

	runCtxDone := runCtx.Done()
	for {
		select {
		case out := <-done:
			return e.resolveFlowOutcome(flow, missionIndex, missionID, runCtx, out)
		case <-heartbeat:
			e.appendWatchdogHeartbeat(missionIndex, missionID, flow.FlowID)
		case <-runCtxDone:
			if !e.opts.WatchdogHardKillContinue || runCtx.Err() != context.DeadlineExceeded {
				runCtxDone = nil
				continue
			}
			return timeoutFlowRun(flow, missionIndex, missionID)
		}
	}
}

func (e *lockedEngine) resolveFlowOutcome(flow FlowSpec, missionIndex int, missionID string, runCtx context.Context, out runOutcome) FlowRunV1 {
	if out.err != nil && e.opts.WatchdogHardKillContinue &&
		(errors.Is(out.err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded)) {
		return timeoutFlowRun(flow, missionIndex, missionID)
	}
	result := out.result
	if out.err == nil {
		return result
	}
	result.FlowID = flow.FlowID
	result.RunnerType = flow.Runner.Type
	result.SuiteFile = flow.SuiteFile
	result.OK = false
	if len(result.Errors) == 0 {
		result.Errors = []string{ReasonFlowFailed}
	}
	return result
}

func timeoutFlowRun(flow FlowSpec, missionIndex int, missionID string) FlowRunV1 {
	return FlowRunV1{
		FlowID:     flow.FlowID,
		RunnerType: flow.Runner.Type,
		SuiteFile:  flow.SuiteFile,
		OK:         false,
		Errors:     []string{codes.CampaignTimeoutGate},
		Attempts: []AttemptStatusV1{{
			MissionIndex: missionIndex,
			MissionID:    missionID,
			Status:       AttemptStatusInfraFailed,
			Errors:       []string{codes.CampaignTimeoutGate},
		}},
	}
}

func (e *lockedEngine) appendWatchdogHeartbeat(missionIndex int, missionID, flowID string) {
	_ = AppendProgress(e.progressPath, ProgressEventV1{
		SchemaVersion: 1,
		CampaignID:    e.parsed.Spec.CampaignID,
		RunID:         e.state.RunID,
		MissionIndex:  missionIndex,
		MissionID:     missionID,
		FlowID:        flowID,
		Status:        "watchdog_heartbeat",
		CreatedAt:     e.opts.Now().Format(time.RFC3339Nano),
	})
}

func (e *lockedEngine) appendAttemptProgress(missionIndex int, missionID string, missionRuns []FlowRunV1) {
	for i := range missionRuns {
		flow := e.parsed.Spec.Flows[i]
		result := &missionRuns[i]
		if len(result.Attempts) == 0 {
			continue
		}
		for j := range result.Attempts {
			idempotency := progressKey(e.parsed.Spec.CampaignID, flow.FlowID, missionIndex)
			e.applyAttemptIdempotency(result, j, idempotency)
			_ = AppendProgress(e.progressPath, ProgressEventV1{
				SchemaVersion:  1,
				CampaignID:     e.parsed.Spec.CampaignID,
				RunID:          e.state.RunID,
				MissionIndex:   missionIndex,
				MissionID:      missionID,
				FlowID:         flow.FlowID,
				AttemptID:      result.Attempts[j].AttemptID,
				AttemptDir:     result.Attempts[j].AttemptDir,
				Status:         result.Attempts[j].Status,
				ReasonCodes:    result.Attempts[j].Errors,
				IdempotencyKey: idempotency,
				CreatedAt:      e.opts.Now().Format(time.RFC3339Nano),
			})
		}
	}
}

func (e *lockedEngine) applyAttemptIdempotency(result *FlowRunV1, attemptIndex int, idempotency string) {
	if e.seenKeys[idempotency] {
		result.Attempts[attemptIndex].Errors = normalizeReasonCodes(append(result.Attempts[attemptIndex].Errors, codes.CampaignDuplicateAttempt))
		if result.Attempts[attemptIndex].Status == AttemptStatusValid {
			result.Attempts[attemptIndex].Status = AttemptStatusInvalid
		}
		return
	}
	e.seenKeys[idempotency] = true
}

func (e *lockedEngine) recordGateResult(missionIndex int, missionID string, gate MissionGateV1, missionRuns []FlowRunV1) {
	e.state.FlowRuns = mergeCampaignFlowRuns(e.state.FlowRuns, missionRuns)
	e.state.MissionGates = append(e.state.MissionGates, gate)
	e.state.MissionsCompleted++
	e.state.UpdatedAt = e.opts.Now().Format(time.RFC3339Nano)
	_ = AppendProgress(e.progressPath, ProgressEventV1{
		SchemaVersion:  1,
		CampaignID:     e.parsed.Spec.CampaignID,
		RunID:          e.state.RunID,
		MissionIndex:   missionIndex,
		MissionID:      missionID,
		Status:         gateStatus(gate.OK),
		ReasonCodes:    gate.Reasons,
		IdempotencyKey: gateProgressKey(e.parsed.Spec.CampaignID, missionIndex),
		CreatedAt:      e.opts.Now().Format(time.RFC3339Nano),
	})
	if !gate.OK {
		e.runFailureHooks(missionIndex, missionID, append([]string{ReasonGateFailed}, gate.Reasons...))
	}
}

func (e *lockedEngine) appendLifecycle(missionIndex int, missionID string, status string, reasonCodes []string) {
	_ = AppendProgress(e.progressPath, ProgressEventV1{
		SchemaVersion: 1,
		CampaignID:    e.parsed.Spec.CampaignID,
		RunID:         e.state.RunID,
		MissionIndex:  missionIndex,
		MissionID:     missionID,
		Status:        status,
		ReasonCodes:   normalizeReasonCodes(reasonCodes),
		CreatedAt:     e.opts.Now().Format(time.RFC3339Nano),
	})
}

func (e *lockedEngine) runFailureHooks(missionIndex int, missionID string, reasons []string) {
	if len(e.parsed.Spec.Cleanup.OnFailure) == 0 {
		return
	}
	e.appendLifecycle(missionIndex, missionID, "cleanup_on_failure_start", reasons)
	if err := runHookList(e.runHook, e.parsed.Spec.Cleanup.OnFailure, e.opts.CleanupHookTimeoutMs); err != nil {
		e.appendLifecycle(missionIndex, missionID, "cleanup_on_failure_fail", append(reasons, ReasonHookFailed))
		return
	}
	e.appendLifecycle(missionIndex, missionID, "cleanup_on_failure_ok", reasons)
}

func (e *lockedEngine) abort(reasonCodes []string, exit int) EngineResult {
	e.state.Status = RunStatusAborted
	e.state.ReasonCodes = normalizeReasonCodes(append(e.state.ReasonCodes, reasonCodes...))
	e.state.CompletedAt = e.opts.Now().Format(time.RFC3339Nano)
	e.state.UpdatedAt = e.state.CompletedAt
	_ = SaveRunState(e.statePath, e.state)
	return EngineResult{State: e.state, Exit: exit}
}

func (e *lockedEngine) cleanupFlows() {
	for _, flow := range e.parsed.Spec.Flows {
		_ = e.exec.Cleanup(context.Background(), flow)
	}
}

func (e *lockedEngine) finish() (EngineResult, error) {
	e.state.Status = RunStatusValid
	for _, g := range e.state.MissionGates {
		if !g.OK {
			e.state.Status = RunStatusInvalid
			e.state.ReasonCodes = normalizeReasonCodes(append(e.state.ReasonCodes, ReasonGateFailed))
			break
		}
	}
	e.state.CompletedAt = e.opts.Now().Format(time.RFC3339Nano)
	e.state.UpdatedAt = e.state.CompletedAt
	if err := SaveRunState(e.statePath, e.state); err != nil {
		return EngineResult{}, err
	}
	if e.state.Status == RunStatusValid {
		return EngineResult{State: e.state, Exit: 0}, nil
	}
	return EngineResult{State: e.state, Exit: 2}, nil
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
