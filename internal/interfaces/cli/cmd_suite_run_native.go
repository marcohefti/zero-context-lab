package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/feedback"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/trace"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/attempt"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/campaign"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/planner"
	codexappserver "github.com/marcohefti/zero-context-lab/internal/contexts/runtime/infra/codex_app_server"
	providerstub "github.com/marcohefti/zero-context-lab/internal/contexts/runtime/infra/provider_stub"
	"github.com/marcohefti/zero-context-lab/internal/contexts/runtime/ports/native"
	"github.com/marcohefti/zero-context-lab/internal/kernel/ids"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

type nativeAttemptState string

const (
	nativeStateQueued        nativeAttemptState = "queued"
	nativeStateSessionStart  nativeAttemptState = "session_starting"
	nativeStateSessionReady  nativeAttemptState = "session_ready"
	nativeStateThreadStarted nativeAttemptState = "thread_started"
	nativeStateTurnStarted   nativeAttemptState = "turn_started"
	nativeStateTurnCompleted nativeAttemptState = "turn_completed"
	nativeStateInterrupted   nativeAttemptState = "interrupted"
	nativeStateFailed        nativeAttemptState = "failed"
	nativeStateFinalized     nativeAttemptState = "finalized"
)

var nativeStateRank = map[nativeAttemptState]int{
	nativeStateQueued:        1,
	nativeStateSessionStart:  2,
	nativeStateSessionReady:  3,
	nativeStateThreadStarted: 4,
	nativeStateTurnStarted:   5,
	nativeStateTurnCompleted: 6,
	nativeStateInterrupted:   6,
	nativeStateFailed:        6,
	nativeStateFinalized:     7,
}

type nativeAttemptSupervisor struct {
	mu    sync.Mutex
	state nativeAttemptState
}

func newNativeAttemptSupervisor() *nativeAttemptSupervisor {
	return &nativeAttemptSupervisor{state: nativeStateQueued}
}

func (s *nativeAttemptSupervisor) Transition(next nativeAttemptState) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	curr := s.state
	if curr == next {
		return false
	}
	currRank := nativeStateRank[curr]
	nextRank := nativeStateRank[next]
	if nextRank == 0 || nextRank < currRank {
		return false
	}
	s.state = next
	return true
}

func (s *nativeAttemptSupervisor) State() nativeAttemptState {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

type nativeAttemptScheduler struct {
	strategy            native.StrategyID
	sem                 chan struct{}
	minStartInterval    time.Duration
	mu                  sync.Mutex
	nextAllowedStartUTC time.Time
}

func buildNativeAttemptScheduler(strategy native.StrategyID, defaultParallel int) *nativeAttemptScheduler {
	if strings.TrimSpace(string(strategy)) == "" {
		return nil
	}
	maxInflight := parsePositiveIntEnv("ZCL_NATIVE_MAX_INFLIGHT_PER_STRATEGY", 0)
	if maxInflight <= 0 {
		maxInflight = defaultParallel
	}
	if maxInflight <= 0 {
		maxInflight = 1
	}
	minStartMs := parsePositiveIntEnv("ZCL_NATIVE_MIN_START_INTERVAL_MS", 0)
	s := &nativeAttemptScheduler{
		strategy: strategy,
	}
	if maxInflight > 0 {
		s.sem = make(chan struct{}, maxInflight)
	}
	if minStartMs > 0 {
		s.minStartInterval = time.Duration(minStartMs) * time.Millisecond
	}
	return s
}

func (s *nativeAttemptScheduler) Acquire(ctx context.Context) error {
	return s.acquireImpl(ctx)
}

func (s *nativeAttemptScheduler) acquireImpl(ctx context.Context) error {
	return s.acquireCore(ctx)
}

func (s *nativeAttemptScheduler) acquireCore(ctx context.Context) error {
	if s == nil {
		return nil
	}
	acquired, err := s.acquireSemaphore(ctx)
	if err != nil {
		return err
	}
	return s.waitForStartSlot(ctx, acquired)
}

func (s *nativeAttemptScheduler) acquireSemaphore(ctx context.Context) (bool, error) {
	if s.sem == nil {
		return false, nil
	}
	select {
	case s.sem <- struct{}{}:
		return true, nil
	default:
		native.RecordHealth(s.strategy, native.HealthSchedulerWait)
	}
	select {
	case s.sem <- struct{}{}:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (s *nativeAttemptScheduler) waitForStartSlot(ctx context.Context, releaseOnCancel bool) error {
	if s.minStartInterval <= 0 {
		return nil
	}
	wait := s.nextStartWaitDuration()
	if wait <= 0 {
		s.markNextAllowedStart()
		return nil
	}
	native.RecordHealth(s.strategy, native.HealthSchedulerWait)
	if err := waitWithContext(ctx, wait); err != nil {
		if releaseOnCancel {
			s.Release()
		}
		return err
	}
	s.markNextAllowedStart()
	return nil
}

func (s *nativeAttemptScheduler) nextStartWaitDuration() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if now.Before(s.nextAllowedStartUTC) {
		return time.Until(s.nextAllowedStartUTC)
	}
	return 0
}

func (s *nativeAttemptScheduler) markNextAllowedStart() {
	s.mu.Lock()
	s.nextAllowedStartUTC = time.Now().UTC().Add(s.minStartInterval)
	s.mu.Unlock()
}

func waitWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *nativeAttemptScheduler) Release() {
	if s == nil || s.sem == nil {
		return
	}
	select {
	case <-s.sem:
	default:
	}
}

func parsePositiveIntEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func resolveSuiteRunRunnerCwdPolicy(extraAttemptEnv map[string]string) (suiteRunRunnerCwdPolicy, error) {
	return resolveSuiteRunRunnerCwdPolicyImpl(extraAttemptEnv)
}

func resolveSuiteRunRunnerCwdPolicyImpl(extraAttemptEnv map[string]string) (suiteRunRunnerCwdPolicy, error) {
	return resolveSuiteRunRunnerCwdPolicyCore(extraAttemptEnv)
}

func resolveSuiteRunRunnerCwdPolicyCore(extraAttemptEnv map[string]string) (suiteRunRunnerCwdPolicy, error) {
	policy := suiteRunRunnerCwdPolicy{
		Mode:   campaign.RunnerCwdModeInherit,
		Retain: campaign.RunnerCwdRetainNever,
	}
	if len(extraAttemptEnv) == 0 {
		return policy, nil
	}
	policy.Mode = chooseSuiteRunRunnerCwdMode(extraAttemptEnv)
	if !isValidSuiteRunRunnerCwdMode(policy.Mode) {
		return suiteRunRunnerCwdPolicy{}, fmt.Errorf("invalid %s (expected %s|%s)", suiteRunEnvRunnerCwdMode, campaign.RunnerCwdModeInherit, campaign.RunnerCwdModeTempEmptyPerAttempt)
	}
	policy.Retain = chooseSuiteRunRunnerCwdRetain(extraAttemptEnv)
	if !isValidSuiteRunRunnerCwdRetain(policy.Retain) {
		return suiteRunRunnerCwdPolicy{}, fmt.Errorf("invalid %s (expected %s|%s|%s)", suiteRunEnvRunnerCwdRetain, campaign.RunnerCwdRetainNever, campaign.RunnerCwdRetainOnFailure, campaign.RunnerCwdRetainAlways)
	}
	basePath, err := normalizeSuiteRunRunnerCwdBasePath(strings.TrimSpace(extraAttemptEnv[suiteRunEnvRunnerCwdBasePath]))
	if err != nil {
		return suiteRunRunnerCwdPolicy{}, err
	}
	policy.BasePath = basePath
	if err := validateSuiteRunRunnerCwdPolicyShape(policy); err != nil {
		return suiteRunRunnerCwdPolicy{}, err
	}
	return policy, nil
}

func chooseSuiteRunRunnerCwdMode(extraAttemptEnv map[string]string) string {
	rawMode := strings.ToLower(strings.TrimSpace(extraAttemptEnv[suiteRunEnvRunnerCwdMode]))
	if rawMode != "" {
		return rawMode
	}
	return campaign.RunnerCwdModeInherit
}

func chooseSuiteRunRunnerCwdRetain(extraAttemptEnv map[string]string) string {
	rawRetain := strings.ToLower(strings.TrimSpace(extraAttemptEnv[suiteRunEnvRunnerCwdRetain]))
	if rawRetain != "" {
		return rawRetain
	}
	return campaign.RunnerCwdRetainNever
}

func normalizeSuiteRunRunnerCwdBasePath(basePath string) (string, error) {
	if basePath == "" {
		return "", nil
	}
	if !filepath.IsAbs(basePath) {
		abs, err := filepath.Abs(basePath)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", suiteRunEnvRunnerCwdBasePath, err)
		}
		basePath = abs
	}
	return filepath.Clean(basePath), nil
}

func validateSuiteRunRunnerCwdPolicyShape(policy suiteRunRunnerCwdPolicy) error {
	if policy.Mode != campaign.RunnerCwdModeInherit {
		return nil
	}
	if policy.BasePath != "" {
		return fmt.Errorf("%s requires %s=%s", suiteRunEnvRunnerCwdBasePath, suiteRunEnvRunnerCwdMode, campaign.RunnerCwdModeTempEmptyPerAttempt)
	}
	if policy.Retain != campaign.RunnerCwdRetainNever {
		return fmt.Errorf("%s requires %s=%s", suiteRunEnvRunnerCwdRetain, suiteRunEnvRunnerCwdMode, campaign.RunnerCwdModeTempEmptyPerAttempt)
	}
	return nil
}

func prepareSuiteRunAttemptStartCwd(pm planner.PlannedMission, policy suiteRunRunnerCwdPolicy) (suiteRunAttemptRuntimeContext, func(bool) error, error) {
	return prepareSuiteRunAttemptStartCwdImpl(pm, policy)
}

func prepareSuiteRunAttemptStartCwdImpl(pm planner.PlannedMission, policy suiteRunRunnerCwdPolicy) (suiteRunAttemptRuntimeContext, func(bool) error, error) {
	return prepareSuiteRunAttemptStartCwdCore(pm, policy)
}

func prepareSuiteRunAttemptStartCwdCore(pm planner.PlannedMission, policy suiteRunRunnerCwdPolicy) (suiteRunAttemptRuntimeContext, func(bool) error, error) {
	mode := normalizeSuiteRunRunnerCwdMode(policy.Mode)
	retain := normalizeSuiteRunRunnerCwdRetain(policy.Retain)
	switch mode {
	case campaign.RunnerCwdModeInherit:
		return prepareSuiteRunInheritedCwd(retain)
	case campaign.RunnerCwdModeTempEmptyPerAttempt:
		return prepareSuiteRunTemporaryCwd(pm, policy.BasePath, mode, retain)
	default:
		return suiteRunAttemptRuntimeContext{}, nil, fmt.Errorf("invalid runner cwd mode %q", mode)
	}
}

func normalizeSuiteRunRunnerCwdMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return campaign.RunnerCwdModeInherit
	}
	return mode
}

func normalizeSuiteRunRunnerCwdRetain(retain string) string {
	retain = strings.ToLower(strings.TrimSpace(retain))
	if retain == "" {
		return campaign.RunnerCwdRetainNever
	}
	return retain
}

func prepareSuiteRunInheritedCwd(retain string) (suiteRunAttemptRuntimeContext, func(bool) error, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return suiteRunAttemptRuntimeContext{}, nil, fmt.Errorf("resolve inherited runner cwd: %w", err)
	}
	return suiteRunAttemptRuntimeContext{
		StartCwdMode:   campaign.RunnerCwdModeInherit,
		StartCwd:       strings.TrimSpace(cwd),
		StartCwdRetain: retain,
	}, nil, nil
}

func prepareSuiteRunTemporaryCwd(pm planner.PlannedMission, basePath, mode, retain string) (suiteRunAttemptRuntimeContext, func(bool) error, error) {
	absBasePath, err := ensureSuiteRunRunnerCwdBasePath(basePath)
	if err != nil {
		return suiteRunAttemptRuntimeContext{}, nil, err
	}
	startCwd, err := os.MkdirTemp(absBasePath, suiteRunRunnerCwdPrefix(pm))
	if err != nil {
		return suiteRunAttemptRuntimeContext{}, nil, fmt.Errorf("create runner cwd temp dir: %w", err)
	}
	cleanup := func(attemptOK bool) error {
		if !shouldRemoveSuiteRunCwd(retain, attemptOK) {
			return nil
		}
		if err := os.RemoveAll(startCwd); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cleanup runner cwd %q: %w", startCwd, err)
		}
		return nil
	}
	return suiteRunAttemptRuntimeContext{
		StartCwdMode:   mode,
		StartCwd:       startCwd,
		StartCwdRetain: retain,
	}, cleanup, nil
}

func ensureSuiteRunRunnerCwdBasePath(basePath string) (string, error) {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" {
		basePath = os.TempDir()
	}
	if !filepath.IsAbs(basePath) {
		abs, err := filepath.Abs(basePath)
		if err != nil {
			return "", fmt.Errorf("resolve runner cwd base path: %w", err)
		}
		basePath = abs
	}
	basePath = filepath.Clean(basePath)
	if err := os.MkdirAll(basePath, 0o700); err != nil {
		return "", fmt.Errorf("create runner cwd base path: %w", err)
	}
	return basePath, nil
}

func suiteRunRunnerCwdPrefix(pm planner.PlannedMission) string {
	prefix := "zcl-cwd-"
	if attemptID := ids.SanitizeComponent(strings.TrimSpace(pm.AttemptID)); attemptID != "" {
		prefix = "zcl-cwd-" + attemptID + "-"
	}
	return prefix
}

func shouldRemoveSuiteRunCwd(retain string, attemptOK bool) bool {
	switch retain {
	case campaign.RunnerCwdRetainAlways:
		return false
	case campaign.RunnerCwdRetainOnFailure:
		return attemptOK
	default:
		return true
	}
}

func isValidSuiteRunRunnerCwdMode(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case campaign.RunnerCwdModeInherit, campaign.RunnerCwdModeTempEmptyPerAttempt:
		return true
	default:
		return false
	}
}

func isValidSuiteRunRunnerCwdRetain(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case campaign.RunnerCwdRetainNever, campaign.RunnerCwdRetainOnFailure, campaign.RunnerCwdRetainAlways:
		return true
	default:
		return false
	}
}

func runSuiteNativeRuntime(r Runner, pm planner.PlannedMission, env map[string]string, opts suiteRunExecOpts, runtimeCtx suiteRunAttemptRuntimeContext, ar *suiteRunAttemptResult, errWriter io.Writer) bool {
	return runSuiteNativeRuntimeImpl(r, pm, env, opts, runtimeCtx, ar, errWriter)
}

func runSuiteNativeRuntimeImpl(r Runner, pm planner.PlannedMission, env map[string]string, opts suiteRunExecOpts, runtimeCtx suiteRunAttemptRuntimeContext, ar *suiteRunAttemptResult, errWriter io.Writer) bool {
	return runSuiteNativeRuntimeCore(r, pm, env, opts, runtimeCtx, ar, errWriter)
}

func runSuiteNativeRuntimeCore(r Runner, pm planner.PlannedMission, env map[string]string, opts suiteRunExecOpts, runtimeCtx suiteRunAttemptRuntimeContext, ar *suiteRunAttemptResult, errWriter io.Writer) bool {
	errWriter = defaultSuiteRunErrWriter(errWriter, r.Stderr)
	supervisor, emitNativeState := newSuiteNativeStateEmitter(r, pm, env, opts)
	emitNativeState(nativeStateQueued, true, nil)

	setup, ok, harnessErr := prepareSuiteNativeRuntimeSetup(r, pm, env, opts, ar, errWriter, emitNativeState)
	if !ok {
		return harnessErr
	}
	defer setup.cleanup()

	sess, ok, harnessErr := startSuiteNativeSession(setup, pm, env, opts, ar, emitNativeState)
	if !ok {
		return harnessErr
	}
	defer closeSuiteNativeSession(sess, opts.NativeSelection.Selected)

	listener, ok, harnessErr := addSuiteNativeListener(sess, setup.envTrace, opts.NativeSelection.Selected, ar, emitNativeState)
	if !ok {
		return harnessErr
	}
	defer removeSuiteNativeListener(sess, listener.listenerID)

	thread, turn, ok, harnessErr := startSuiteNativeThreadTurn(setup.ctx, sess, pm, opts, runtimeCtx, ar, emitNativeState)
	if !ok {
		return harnessErr
	}
	if !writeSuiteNativeRunnerRef(pm, env, opts, sess, thread, ar, errWriter, emitNativeState) {
		return true
	}

	resultCollector := newNativeResultCollector()
	observeSuiteNativeEvents(setup.ctx, sess, thread, turn, listener.events, resultCollector, opts, ar, emitNativeState)
	if err := listener.traceState.Err(); err != nil {
		return failSuiteNativeTraceAppend(ar, errWriter, err, emitNativeState)
	}
	return finalizeSuiteNativeRun(setup.now, setup.envTrace, supervisor, pm, turn, resultCollector, ar, emitNativeState, errWriter)
}

type suiteNativeRuntimeSetup struct {
	now      time.Time
	ctx      context.Context
	cleanup  func()
	rt       native.Runtime
	envTrace trace.Env
}

type suiteNativeRuntimeListener struct {
	listenerID string
	events     chan native.Event
	traceState *suiteNativeTraceState
}

type suiteNativeTraceState struct {
	mu  sync.Mutex
	err error
}

func (s *suiteNativeTraceState) Set(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *suiteNativeTraceState) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func newSuiteNativeStateEmitter(r Runner, pm planner.PlannedMission, env map[string]string, opts suiteRunExecOpts) (*nativeAttemptSupervisor, func(state nativeAttemptState, force bool, details map[string]any)) {
	supervisor := newNativeAttemptSupervisor()
	emit := func(state nativeAttemptState, force bool, details map[string]any) {
		if !force && !supervisor.Transition(state) {
			return
		}
		if opts.Progress == nil {
			return
		}
		payload := map[string]any{"state": string(state)}
		for k, v := range details {
			payload[k] = v
		}
		_ = opts.Progress.Emit(suiteRunProgressEvent{
			TS:        r.Now().UTC().Format(time.RFC3339Nano),
			Kind:      "attempt_native_state",
			RunID:     env["ZCL_RUN_ID"],
			SuiteID:   env["ZCL_SUITE_ID"],
			MissionID: env["ZCL_MISSION_ID"],
			AttemptID: env["ZCL_ATTEMPT_ID"],
			OutDir:    pm.OutDirAbs,
			Details:   payload,
		})
	}
	return supervisor, emit
}

func prepareSuiteNativeRuntimeSetup(r Runner, pm planner.PlannedMission, env map[string]string, opts suiteRunExecOpts, ar *suiteRunAttemptResult, errWriter io.Writer, emitNativeState func(state nativeAttemptState, force bool, details map[string]any)) (suiteNativeRuntimeSetup, bool, bool) {
	setup := suiteNativeRuntimeSetup{
		now: r.Now(),
	}
	if _, err := attempt.EnsureTimeoutAnchor(setup.now, pm.OutDirAbs); err != nil {
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		emitSuiteNativeFailure(ar, codeIO, emitNativeState, "timeout_anchor_failed")
		return setup, false, true
	}
	ctx, cancel, timedOut := attemptCtxForDeadline(setup.now, pm.OutDirAbs)
	if timedOut {
		emitSuiteNativeFailure(ar, codeRuntimeStall, emitNativeState, "attempt_deadline_exceeded")
		return setup, false, false
	}
	releaseScheduler := func() {}
	if opts.NativeScheduler != nil {
		if err := opts.NativeScheduler.Acquire(ctx); err != nil {
			if cancel != nil {
				cancel()
			}
			emitSuiteNativeFailure(ar, codeRuntimeStall, emitNativeState, "scheduler_acquire_timeout")
			return setup, false, false
		}
		releaseScheduler = opts.NativeScheduler.Release
	}
	setup.ctx = ctx
	setup.cleanup = func() {
		releaseScheduler()
		if cancel != nil {
			cancel()
		}
	}
	setup.rt = opts.NativeSelection.Runtime
	if setup.rt == nil {
		emitSuiteNativeFailure(ar, codeUsage, emitNativeState, "runtime_not_selected")
		return setup, false, true
	}
	setup.envTrace = suiteRunTraceEnv(env, strings.TrimSpace(env["ZCL_OUT_DIR"]))
	return setup, true, false
}

func startSuiteNativeSession(setup suiteNativeRuntimeSetup, pm planner.PlannedMission, env map[string]string, opts suiteRunExecOpts, ar *suiteRunAttemptResult, emitNativeState func(state nativeAttemptState, force bool, details map[string]any)) (native.Session, bool, bool) {
	emitNativeState(nativeStateSessionStart, false, nil)
	native.RecordHealth(opts.NativeSelection.Selected, native.HealthSessionStart)
	sess, err := setup.rt.StartSession(setup.ctx, native.SessionOptions{
		RunID:      env["ZCL_RUN_ID"],
		SuiteID:    env["ZCL_SUITE_ID"],
		MissionID:  env["ZCL_MISSION_ID"],
		AttemptID:  env["ZCL_ATTEMPT_ID"],
		AttemptDir: pm.OutDirAbs,
		Env:        env,
	})
	if err != nil {
		native.RecordHealth(opts.NativeSelection.Selected, native.HealthSessionStartFail)
		ar.RunnerErrorCode = nativeErrorCode(err)
		recordNativeFailureHealth(opts.NativeSelection.Selected, ar.RunnerErrorCode)
		ec := 1
		ar.RunnerExitCode = &ec
		emitNativeState(nativeStateFailed, false, map[string]any{
			"reason": "session_start_failed",
			"code":   ar.RunnerErrorCode,
		})
		_ = trace.AppendNativeRuntimeEvent(setup.now, setup.envTrace, trace.NativeRuntimeEvent{
			RuntimeID: string(opts.NativeSelection.Selected),
			EventName: "codex/event/session_start_failed",
			Code:      ar.RunnerErrorCode,
			Partial:   true,
		})
		return nil, false, false
	}
	_ = trace.AppendNativeRuntimeEvent(setup.now, setup.envTrace, trace.NativeRuntimeEvent{
		RuntimeID: string(opts.NativeSelection.Selected),
		SessionID: sess.SessionID(),
		EventName: "codex/event/session_started",
	})
	emitNativeState(nativeStateSessionReady, false, map[string]any{
		"sessionId": sess.SessionID(),
	})
	return sess, true, false
}

func closeSuiteNativeSession(sess native.Session, strategy native.StrategyID) {
	if sess == nil {
		return
	}
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer closeCancel()
	native.RecordHealth(strategy, native.HealthSessionClosed)
	_ = sess.Close(closeCtx)
}

func addSuiteNativeListener(sess native.Session, envTrace trace.Env, strategy native.StrategyID, ar *suiteRunAttemptResult, emitNativeState func(state nativeAttemptState, force bool, details map[string]any)) (suiteNativeRuntimeListener, bool, bool) {
	state := &suiteNativeTraceState{}
	events := make(chan native.Event, 128)
	listenerID, err := sess.AddListener(func(ev native.Event) {
		if appendErr := trace.AppendNativeRuntimeEvent(ev.ReceivedAt, envTrace, trace.NativeRuntimeEvent{
			RuntimeID: string(strategy),
			SessionID: sess.SessionID(),
			ThreadID:  ev.ThreadID,
			TurnID:    ev.TurnID,
			CallID:    ev.CallID,
			EventName: ev.Name,
			Payload:   ev.Payload,
		}); appendErr != nil {
			state.Set(appendErr)
		}
		select {
		case events <- ev:
		default:
		}
	})
	if err != nil {
		native.RecordHealth(strategy, native.HealthListenerFailure)
		ar.RunnerErrorCode = nativeErrorCode(err)
		recordNativeFailureHealth(strategy, ar.RunnerErrorCode)
		ec := 1
		ar.RunnerExitCode = &ec
		emitNativeState(nativeStateFailed, false, map[string]any{
			"reason": "listener_add_failed",
			"code":   ar.RunnerErrorCode,
		})
		return suiteNativeRuntimeListener{}, false, true
	}
	return suiteNativeRuntimeListener{
		listenerID: listenerID,
		events:     events,
		traceState: state,
	}, true, false
}

func removeSuiteNativeListener(sess native.Session, listenerID string) {
	if sess == nil || strings.TrimSpace(listenerID) == "" {
		return
	}
	_ = sess.RemoveListener(listenerID)
}

func startSuiteNativeThreadTurn(ctx context.Context, sess native.Session, pm planner.PlannedMission, opts suiteRunExecOpts, runtimeCtx suiteRunAttemptRuntimeContext, ar *suiteRunAttemptResult, emitNativeState func(state nativeAttemptState, force bool, details map[string]any)) (native.ThreadHandle, native.TurnHandle, bool, bool) {
	thread, err := sess.StartThread(ctx, native.ThreadStartRequest{
		Model:                strings.TrimSpace(opts.NativeModel),
		ModelReasoningEffort: strings.ToLower(strings.TrimSpace(opts.ReasoningEffort)),
		ModelReasoningPolicy: strings.ToLower(strings.TrimSpace(opts.ReasoningPolicy)),
		Cwd:                  strings.TrimSpace(runtimeCtx.StartCwd),
	})
	if err != nil {
		ar.RunnerErrorCode = nativeErrorCode(err)
		recordNativeFailureHealth(opts.NativeSelection.Selected, ar.RunnerErrorCode)
		ec := 1
		ar.RunnerExitCode = &ec
		emitNativeState(nativeStateFailed, false, map[string]any{
			"reason": "thread_start_failed",
			"code":   ar.RunnerErrorCode,
		})
		return native.ThreadHandle{}, native.TurnHandle{}, false, false
	}
	emitNativeState(nativeStateThreadStarted, false, map[string]any{"threadId": thread.ThreadID})

	prompt := strings.TrimSpace(pm.Prompt)
	if prompt == "" {
		prompt = "complete mission and provide final result"
	}
	turn, err := sess.StartTurn(ctx, native.TurnStartRequest{
		ThreadID: thread.ThreadID,
		Input:    []native.InputItem{{Type: "text", Text: prompt}},
	})
	if err != nil {
		ar.RunnerErrorCode = nativeErrorCode(err)
		recordNativeFailureHealth(opts.NativeSelection.Selected, ar.RunnerErrorCode)
		ec := 1
		ar.RunnerExitCode = &ec
		emitNativeState(nativeStateFailed, false, map[string]any{
			"reason": "turn_start_failed",
			"code":   ar.RunnerErrorCode,
		})
		return native.ThreadHandle{}, native.TurnHandle{}, false, false
	}
	emitNativeState(nativeStateTurnStarted, false, map[string]any{"turnId": turn.TurnID})
	return thread, turn, true, false
}

func writeSuiteNativeRunnerRef(pm planner.PlannedMission, env map[string]string, opts suiteRunExecOpts, sess native.Session, thread native.ThreadHandle, ar *suiteRunAttemptResult, errWriter io.Writer, emitNativeState func(state nativeAttemptState, force bool, details map[string]any)) bool {
	if err := writeNativeRunnerRef(pm.OutDirAbs, env, opts.NativeSelection.Selected, sess.SessionID(), thread.ThreadID); err != nil {
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		emitSuiteNativeFailure(ar, codeIO, emitNativeState, "runner_ref_write_failed")
		return false
	}
	return true
}

func observeSuiteNativeEvents(ctx context.Context, sess native.Session, thread native.ThreadHandle, turn native.TurnHandle, events <-chan native.Event, resultCollector *nativeResultCollector, opts suiteRunExecOpts, ar *suiteRunAttemptResult, emitNativeState func(state nativeAttemptState, force bool, details map[string]any)) {
	for completed := false; !completed; {
		select {
		case ev := <-events:
			resultCollector.Observe(ev)
			if nativeEventIsTurnCompleted(ev, turn.TurnID) {
				emitNativeState(nativeStateTurnCompleted, false, map[string]any{"turnId": turn.TurnID})
				completed = true
				continue
			}
			completed = observeSuiteNativeEventFailure(ev, opts.NativeSelection.Selected, ar, emitNativeState, completed)
		case <-ctx.Done():
			ar.RunnerErrorCode = codeRuntimeStall
			recordNativeFailureHealth(opts.NativeSelection.Selected, ar.RunnerErrorCode)
			native.RecordHealth(opts.NativeSelection.Selected, native.HealthInterrupted)
			if strings.TrimSpace(turn.TurnID) != "" {
				_ = sess.InterruptTurn(context.Background(), native.TurnInterruptRequest{ThreadID: thread.ThreadID, TurnID: turn.TurnID})
			}
			emitNativeState(nativeStateInterrupted, false, map[string]any{
				"reason": "attempt_stall_timeout",
				"code":   ar.RunnerErrorCode,
			})
			completed = true
		}
	}
}

func observeSuiteNativeEventFailure(ev native.Event, strategy native.StrategyID, ar *suiteRunAttemptResult, emitNativeState func(state nativeAttemptState, force bool, details map[string]any), completed bool) bool {
	switch ev.Name {
	case "codex/event/turn_failed":
		ar.RunnerErrorCode = classifyNativeFailureCode(ev.Payload, codeToolFailed)
		recordNativeFailureHealth(strategy, ar.RunnerErrorCode)
		emitNativeState(nativeStateFailed, false, map[string]any{
			"reason": "turn_failed",
			"code":   ar.RunnerErrorCode,
		})
		return true
	case "codex/event/error":
		if ar.RunnerErrorCode == "" {
			ar.RunnerErrorCode = classifyNativeFailureCode(ev.Payload, codeToolFailed)
			recordNativeFailureHealth(strategy, ar.RunnerErrorCode)
			emitNativeState(nativeStateFailed, false, map[string]any{
				"reason": "runtime_error_event",
				"code":   ar.RunnerErrorCode,
			})
		}
		return completed
	case "codex/event/stream_disconnected":
		ar.RunnerErrorCode = classifyNativeFailureCode(ev.Payload, codeRuntimeStreamDisconnect)
		recordNativeFailureHealth(strategy, ar.RunnerErrorCode)
		emitNativeState(nativeStateFailed, false, map[string]any{
			"reason": "stream_disconnected",
			"code":   ar.RunnerErrorCode,
		})
		return true
	case "codex/event/runtime_crashed":
		ar.RunnerErrorCode = classifyNativeFailureCode(ev.Payload, codeRuntimeCrash)
		recordNativeFailureHealth(strategy, ar.RunnerErrorCode)
		emitNativeState(nativeStateFailed, false, map[string]any{
			"reason": "runtime_crashed",
			"code":   ar.RunnerErrorCode,
		})
		return true
	default:
		return completed
	}
}

func failSuiteNativeTraceAppend(ar *suiteRunAttemptResult, errWriter io.Writer, err error, emitNativeState func(state nativeAttemptState, force bool, details map[string]any)) bool {
	fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
	emitSuiteNativeFailure(ar, codeIO, emitNativeState, "trace_append_failed")
	return true
}

func finalizeSuiteNativeRun(now time.Time, envTrace trace.Env, supervisor *nativeAttemptSupervisor, pm planner.PlannedMission, turn native.TurnHandle, resultCollector *nativeResultCollector, ar *suiteRunAttemptResult, emitNativeState func(state nativeAttemptState, force bool, details map[string]any), errWriter io.Writer) bool {
	finalResult, resultSource, foundFinalResult := resultCollector.ResolveFinalResult()
	if err := writeNativeResultProvenance(pm.OutDirAbs, resultCollector.Provenance(resultSource)); err != nil {
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		emitSuiteNativeFailure(ar, codeIO, emitNativeState, "attempt_metadata_write_failed")
		return true
	}
	if ar.RunnerErrorCode == "" && !foundFinalResult {
		ar.RunnerErrorCode = codeRuntimeFinalAnswerNotFound
		emitNativeState(nativeStateFailed, false, map[string]any{
			"reason":                     "final_answer_not_found",
			"code":                       ar.RunnerErrorCode,
			"phaseAware":                 resultCollector.PhaseAware(),
			"commentaryMessagesObserved": resultCollector.CommentaryMessagesObserved(),
			"reasoningItemsObserved":     resultCollector.ReasoningItemsObserved(),
		})
	}
	setSuiteNativeRunnerExitCode(ar)
	if fileExists(filepath.Join(pm.OutDirAbs, artifacts.FeedbackJSON)) {
		return false
	}
	return writeSuiteNativeAutoFeedback(now, envTrace, supervisor, turn.TurnID, finalResult, resultSource, ar, emitNativeState, errWriter)
}

func setSuiteNativeRunnerExitCode(ar *suiteRunAttemptResult) {
	ec := 1
	if ar.RunnerErrorCode == "" {
		ec = 0
	}
	ar.RunnerExitCode = &ec
}

func writeSuiteNativeAutoFeedback(now time.Time, envTrace trace.Env, supervisor *nativeAttemptSupervisor, turnID string, finalResult string, resultSource string, ar *suiteRunAttemptResult, emitNativeState func(state nativeAttemptState, force bool, details map[string]any), errWriter io.Writer) bool {
	if ar.RunnerErrorCode == "" {
		if err := feedback.Write(now, envTrace, feedback.WriteOpts{OK: true, Result: strings.TrimSpace(finalResult)}); err != nil {
			fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
			emitSuiteNativeFailure(ar, codeIO, emitNativeState, "feedback_write_failed")
			return true
		}
		ar.AutoFeedback = true
		emitNativeState(nativeStateFinalized, false, map[string]any{
			"feedbackAuto": true,
			"resultSource": resultSource,
			"state":        string(supervisor.State()),
		})
		return false
	}
	resultJSON, _ := store.CanonicalJSON(map[string]any{
		"kind":   "runtime_failure",
		"code":   ar.RunnerErrorCode,
		"turnId": turnID,
	})
	if err := feedback.Write(now, envTrace, feedback.WriteOpts{
		OK:                   false,
		ResultJSON:           string(resultJSON),
		DecisionTags:         []string{schema.DecisionTagBlocked},
		SkipSuiteResultShape: true,
	}); err != nil {
		fmt.Fprintf(errWriter, codeIO+": suite run: %s\n", err.Error())
		emitSuiteNativeFailure(ar, codeIO, emitNativeState, "feedback_write_failed")
		return true
	}
	ar.AutoFeedback = true
	ar.AutoFeedbackCode = ar.RunnerErrorCode
	emitNativeState(nativeStateFinalized, false, map[string]any{
		"feedbackAuto": true,
		"code":         ar.RunnerErrorCode,
		"state":        string(supervisor.State()),
	})
	return false
}

func emitSuiteNativeFailure(ar *suiteRunAttemptResult, code string, emitNativeState func(state nativeAttemptState, force bool, details map[string]any), reason string) {
	ar.RunnerErrorCode = code
	ec := 1
	ar.RunnerExitCode = &ec
	emitNativeState(nativeStateFailed, false, map[string]any{
		"reason": reason,
		"code":   ar.RunnerErrorCode,
	})
}

func nativeErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if nerr, ok := native.AsError(err); ok {
		return nerr.Code
	}
	return codeIO
}

func classifyNativeFailureCode(raw json.RawMessage, fallback string) string {
	code := strings.TrimSpace(classifyNativeFailureCodeInner(raw))
	if code != "" {
		return code
	}
	if strings.TrimSpace(fallback) != "" {
		return fallback
	}
	return codeToolFailed
}

func classifyNativeFailureCodeInner(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	code := strings.TrimSpace(firstFailureString(payload, "code"))
	if strings.HasPrefix(code, "ZCL_E_") {
		return code
	}
	errPayload := firstFailureMap(payload, "error")
	if turn := firstFailureMap(payload, "turn"); len(turn) > 0 {
		if nestedErr := firstFailureMap(turn, "error"); len(nestedErr) > 0 {
			errPayload = nestedErr
		}
	}
	if len(errPayload) > 0 {
		if nestedCode := strings.TrimSpace(firstFailureString(errPayload, "code")); strings.HasPrefix(nestedCode, "ZCL_E_") {
			return nestedCode
		}
	}
	msg := strings.ToLower(strings.TrimSpace(firstFailureString(errPayload, "message")))
	if msg == "" {
		msg = strings.ToLower(strings.TrimSpace(firstFailureString(payload, "message")))
	}
	info := firstFailureAny(errPayload, "codexErrorInfo")
	if info == nil {
		info = firstFailureAny(payload, "codexErrorInfo")
	}
	if isRateLimitFailure(msg, info) {
		return codeRuntimeRateLimit
	}
	if isAuthFailure(msg, info) {
		return codeRuntimeAuth
	}
	return ""
}

func firstFailureAny(payload map[string]any, key string) any {
	if len(payload) == 0 {
		return nil
	}
	v := payload[key]
	return v
}

func firstFailureString(payload map[string]any, key string) string {
	v := firstFailureAny(payload, key)
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func firstFailureMap(payload map[string]any, key string) map[string]any {
	v := firstFailureAny(payload, key)
	out, _ := v.(map[string]any)
	return out
}

func isRateLimitFailure(msg string, info any) bool {
	if strings.Contains(msg, "rate limit") || strings.Contains(msg, "usage limit") || strings.Contains(msg, "quota") || strings.Contains(msg, "429") {
		return true
	}
	switch v := info.(type) {
	case string:
		low := strings.ToLower(strings.TrimSpace(v))
		return strings.Contains(low, "usagelimit") || strings.Contains(low, "rate")
	case map[string]any:
		kind := strings.ToLower(strings.TrimSpace(firstFailureString(v, "kind")))
		if strings.Contains(kind, "usagelimit") || strings.Contains(kind, "rate") {
			return true
		}
		statusCode := firstFailureString(v, "httpStatusCode")
		if statusCode == "429" {
			return true
		}
	}
	return false
}

func isAuthFailure(msg string, info any) bool {
	if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "forbidden") || strings.Contains(msg, "auth") || strings.Contains(msg, "401") || strings.Contains(msg, "403") {
		return true
	}
	switch v := info.(type) {
	case string:
		low := strings.ToLower(strings.TrimSpace(v))
		if strings.Contains(low, "auth") {
			return true
		}
	case map[string]any:
		kind := strings.ToLower(strings.TrimSpace(firstFailureString(v, "kind")))
		statusCode := firstFailureString(v, "httpStatusCode")
		if strings.Contains(kind, "httpconnectionfailed") && (statusCode == "401" || statusCode == "403") {
			return true
		}
	}
	return false
}

func recordNativeFailureHealth(strategy native.StrategyID, code string) {
	switch strings.TrimSpace(code) {
	case codeRuntimeRateLimit:
		native.RecordHealth(strategy, native.HealthRateLimited)
	case codeRuntimeAuth:
		native.RecordHealth(strategy, native.HealthAuthFail)
	case codeRuntimeStreamDisconnect:
		native.RecordHealth(strategy, native.HealthStreamDisconnect)
	case codeRuntimeCrash:
		native.RecordHealth(strategy, native.HealthRuntimeCrash)
	case codeRuntimeListenerFailure:
		native.RecordHealth(strategy, native.HealthListenerFailure)
	}
}

type nativeResultCollector struct {
	taskCompleteLastAgentMessage string
	lastPhaseFinalAnswer         string
	deltaFallback                strings.Builder
	phaseAware                   bool
	commentaryByItemID           map[string]bool
	reasoningByItemID            map[string]bool
	commentaryWithoutItemID      int64
	reasoningWithoutItemID       int64
}

func newNativeResultCollector() *nativeResultCollector {
	return &nativeResultCollector{
		commentaryByItemID: map[string]bool{},
		reasoningByItemID:  map[string]bool{},
	}
}

func (c *nativeResultCollector) Observe(ev native.Event) {
	if c == nil {
		return
	}
	payload := nativePayloadObject(ev.Payload)
	if len(payload) == 0 {
		return
	}
	c.observePayload(ev.Name, payload)
	if msg := nativeFirstMap(payload, "msg"); len(msg) > 0 {
		c.observePayload(ev.Name, msg)
	}
}

func (c *nativeResultCollector) observePayload(eventName string, payload map[string]any) {
	c.observePayloadImpl(eventName, payload)
}

func (c *nativeResultCollector) observePayloadImpl(eventName string, payload map[string]any) {
	c.observePayloadCore(eventName, payload)
}

func (c *nativeResultCollector) observePayloadCore(eventName string, payload map[string]any) {
	if len(payload) == 0 {
		return
	}
	c.observeTaskCompletePayload(eventName, payload)
	c.observeAssistantDeltaPayload(eventName, payload)
	c.observeAssistantMessagePayload(eventName, payload)
	c.observeReasoningPayload(payload)
}

func (c *nativeResultCollector) observeTaskCompletePayload(eventName string, payload map[string]any) {
	if !nativePayloadIsTaskComplete(eventName, payload) {
		return
	}
	if last := nativeFirstString(payload, "last_agent_message", "lastAgentMessage"); last != "" {
		c.taskCompleteLastAgentMessage = last
	}
}

func (c *nativeResultCollector) observeAssistantDeltaPayload(eventName string, payload map[string]any) {
	delta := extractNativeEventDeltaFromPayload(payload)
	if delta == "" || !nativePayloadIsAssistantDelta(eventName, payload) {
		return
	}
	c.deltaFallback.WriteString(delta)
}

func (c *nativeResultCollector) observeAssistantMessagePayload(eventName string, payload map[string]any) {
	text, phase, itemID, ok := nativeAssistantMessageFromPayload(eventName, payload)
	if !ok {
		return
	}
	if phase != "" {
		c.phaseAware = true
	}
	switch phase {
	case "commentary":
		c.recordCommentaryItem(itemID)
	case "final_answer":
		trimmed := strings.TrimSpace(text)
		if trimmed != "" {
			c.lastPhaseFinalAnswer = trimmed
		}
	}
}

func (c *nativeResultCollector) recordCommentaryItem(itemID string) {
	if itemID == "" {
		c.commentaryWithoutItemID++
		return
	}
	if !c.commentaryByItemID[itemID] {
		c.commentaryByItemID[itemID] = true
	}
}

func (c *nativeResultCollector) observeReasoningPayload(payload map[string]any) {
	itemID, ok := nativeReasoningItemFromPayload(payload)
	if !ok {
		return
	}
	if itemID == "" {
		c.reasoningWithoutItemID++
		return
	}
	if !c.reasoningByItemID[itemID] {
		c.reasoningByItemID[itemID] = true
	}
}

func (c *nativeResultCollector) ResolveFinalResult() (result string, source string, ok bool) {
	if c == nil {
		return "", "", false
	}
	if last := strings.TrimSpace(c.taskCompleteLastAgentMessage); last != "" {
		return last, schema.NativeResultSourceTaskCompleteLastAgentMessageV1, true
	}
	if msg := strings.TrimSpace(c.lastPhaseFinalAnswer); msg != "" {
		return msg, schema.NativeResultSourcePhaseFinalAnswerV1, true
	}
	if !c.phaseAware {
		if delta := strings.TrimSpace(c.deltaFallback.String()); delta != "" {
			return delta, schema.NativeResultSourceDeltaFallbackV1, true
		}
	}
	return "", "", false
}

func (c *nativeResultCollector) ProvenanceResultSourceOrEmpty(source string) string {
	source = strings.TrimSpace(source)
	if schema.IsValidNativeResultSourceV1(source) {
		return source
	}
	return ""
}

func (c *nativeResultCollector) Provenance(source string) *schema.NativeResultProvenanceV1 {
	if c == nil {
		return nil
	}
	return &schema.NativeResultProvenanceV1{
		ResultSource:               c.ProvenanceResultSourceOrEmpty(source),
		PhaseAware:                 c.phaseAware,
		CommentaryMessagesObserved: c.CommentaryMessagesObserved(),
		ReasoningItemsObserved:     c.ReasoningItemsObserved(),
	}
}

func (c *nativeResultCollector) CommentaryMessagesObserved() int64 {
	if c == nil {
		return 0
	}
	return int64(len(c.commentaryByItemID)) + c.commentaryWithoutItemID
}

func (c *nativeResultCollector) ReasoningItemsObserved() int64 {
	if c == nil {
		return 0
	}
	return int64(len(c.reasoningByItemID)) + c.reasoningWithoutItemID
}

func (c *nativeResultCollector) PhaseAware() bool {
	if c == nil {
		return false
	}
	return c.phaseAware
}

func extractNativeEventDeltaFromPayload(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	delta := nativeFirstString(payload, "delta")
	return strings.TrimSpace(delta)
}

func nativeEventIsTurnCompleted(ev native.Event, expectedTurnID string) bool {
	switch ev.Name {
	case "codex/event/turn_completed", "codex/event/task_complete", "codex/event/turn_complete":
		return nativeTurnIDMatches(expectedTurnID, ev.TurnID, nativePayloadTurnID(ev.Payload))
	}
	payload := nativePayloadObject(ev.Payload)
	if nativePayloadIsTaskComplete(ev.Name, payload) {
		return nativeTurnIDMatches(expectedTurnID, ev.TurnID, nativePayloadTurnID(ev.Payload))
	}
	if msg := nativeFirstMap(payload, "msg"); nativePayloadIsTaskComplete(ev.Name, msg) {
		return nativeTurnIDMatches(expectedTurnID, ev.TurnID, nativeFirstString(msg, "turn_id", "turnId"))
	}
	return false
}

func nativeTurnIDMatches(expected string, candidates ...string) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return true
	}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" || c == expected {
			return true
		}
	}
	return false
}

func nativePayloadTurnID(raw json.RawMessage) string {
	payload := nativePayloadObject(raw)
	if len(payload) == 0 {
		return ""
	}
	if turnID := nativeFirstString(payload, "turnId", "turn_id"); turnID != "" {
		return turnID
	}
	if msg := nativeFirstMap(payload, "msg"); len(msg) > 0 {
		return nativeFirstString(msg, "turnId", "turn_id")
	}
	return ""
}

func nativePayloadObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

func nativePayloadIsTaskComplete(eventName string, payload map[string]any) bool {
	if len(payload) == 0 {
		return false
	}
	switch strings.TrimSpace(eventName) {
	case "codex/event/task_complete", "codex/event/turn_complete":
		return true
	}
	typ := strings.ToLower(strings.TrimSpace(nativeFirstString(payload, "type")))
	return typ == "task_complete" || typ == "turn_complete"
}

func nativePayloadIsAssistantDelta(eventName string, payload map[string]any) bool {
	switch strings.TrimSpace(eventName) {
	case "codex/event/item_agentMessage_delta", "codex/event/agent_message_delta", "codex/event/agent_message_content_delta":
		return true
	}
	typ := strings.ToLower(strings.TrimSpace(nativeFirstString(payload, "type")))
	return typ == "agent_message_delta" || typ == "agent_message_content_delta"
}

func nativeAssistantMessageFromPayload(eventName string, payload map[string]any) (text string, phase string, itemID string, ok bool) {
	if item := nativeFirstMap(payload, "item"); len(item) > 0 {
		return nativeAssistantMessageFromItem(item)
	}

	typ := strings.ToLower(strings.TrimSpace(nativeFirstString(payload, "type")))
	if typ == "agent_message" || strings.TrimSpace(eventName) == "codex/event/agent_message" {
		msg := strings.TrimSpace(nativeFirstString(payload, "message"))
		return msg, nativeNormalizePhase(nativeFirstString(payload, "phase")), nativeFirstString(payload, "item_id", "itemId", "id"), msg != ""
	}
	return "", "", "", false
}

func nativeAssistantMessageFromItem(item map[string]any) (text string, phase string, itemID string, ok bool) {
	typ := strings.ToLower(strings.TrimSpace(nativeFirstString(item, "type")))
	switch typ {
	case "agentmessage", "agent_message", "message":
	default:
		return "", "", "", false
	}
	if typ == "message" {
		role := strings.ToLower(strings.TrimSpace(nativeFirstString(item, "role")))
		if role != "" && role != "assistant" {
			return "", "", "", false
		}
	}
	phase = nativeNormalizePhase(nativeFirstString(item, "phase"))
	itemID = nativeFirstString(item, "id", "item_id", "itemId")
	text = nativeExtractAssistantText(item)
	if strings.TrimSpace(text) == "" && phase == "" {
		return "", "", "", false
	}
	return strings.TrimSpace(text), phase, strings.TrimSpace(itemID), true
}

func nativeExtractAssistantText(item map[string]any) string {
	if len(item) == 0 {
		return ""
	}
	if msg := nativeFirstString(item, "message", "text"); msg != "" {
		return msg
	}
	parts, _ := item["content"].([]any)
	if len(parts) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case string:
			sb.WriteString(p)
		case map[string]any:
			sb.WriteString(nativeFirstString(p, "text"))
		}
	}
	return strings.TrimSpace(sb.String())
}

func nativeNormalizePhase(phase string) string {
	phase = strings.ToLower(strings.TrimSpace(phase))
	phase = strings.ReplaceAll(phase, "-", "_")
	switch phase {
	case "commentary":
		return "commentary"
	case "finalanswer":
		return "final_answer"
	default:
		return phase
	}
}

func nativeReasoningItemFromPayload(payload map[string]any) (itemID string, ok bool) {
	if item := nativeFirstMap(payload, "item"); len(item) > 0 {
		typ := strings.ToLower(strings.TrimSpace(nativeFirstString(item, "type")))
		if typ == "reasoning" {
			return nativeFirstString(item, "id", "item_id", "itemId"), true
		}
	}
	typ := strings.ToLower(strings.TrimSpace(nativeFirstString(payload, "type")))
	switch typ {
	case "agent_reasoning", "reasoning":
		return nativeFirstString(payload, "item_id", "itemId", "id"), true
	default:
		return "", false
	}
}

func nativeFirstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := payload[key].(string); ok {
			v = strings.TrimSpace(v)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

func nativeFirstMap(payload map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if v, ok := payload[key].(map[string]any); ok {
			return v
		}
	}
	return nil
}

func writeNativeResultProvenance(attemptDir string, provenance *schema.NativeResultProvenanceV1) error {
	if strings.TrimSpace(attemptDir) == "" || provenance == nil {
		return nil
	}
	meta, err := attempt.ReadAttempt(attemptDir)
	if err != nil {
		return err
	}
	cloned := *provenance
	meta.NativeResult = &cloned
	return store.WriteJSONAtomic(filepath.Join(attemptDir, artifacts.AttemptJSON), meta)
}

func buildNativeRuntimeRegistry() *native.Registry {
	reg := native.NewRegistry()
	reg.MustRegister(codexappserver.NewRuntime(codexappserver.Config{
		Command: codexappserver.DefaultCommandFromEnv(),
	}))
	reg.MustRegister(providerstub.NewRuntime())
	return reg
}

func writeNativeRunnerRef(attemptDir string, env map[string]string, runtimeID native.StrategyID, sessionID string, threadID string) error {
	ref := schema.RunnerRefJSONV1{
		SchemaVersion: schema.ArtifactSchemaV1,
		Runner:        string(runtimeID),
		RunID:         env["ZCL_RUN_ID"],
		SuiteID:       env["ZCL_SUITE_ID"],
		MissionID:     env["ZCL_MISSION_ID"],
		AttemptID:     env["ZCL_ATTEMPT_ID"],
		AgentID:       env["ZCL_AGENT_ID"],
		ThreadID:      strings.TrimSpace(threadID),
		RuntimeID:     string(runtimeID),
		SessionID:     strings.TrimSpace(sessionID),
		Transport:     "stdio",
	}
	return store.WriteJSONAtomic(filepath.Join(attemptDir, artifacts.RunnerRefJSON), ref)
}
