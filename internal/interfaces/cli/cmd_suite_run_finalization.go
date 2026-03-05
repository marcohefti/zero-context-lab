package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/feedback"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/trace"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/campaign"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
)

func normalizeSuiteRunFinalizationMode(mode string, feedbackPolicy string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode != "" {
		return mode
	}
	switch schema.NormalizeFeedbackPolicyV1(feedbackPolicy) {
	case schema.FeedbackPolicyStrictV1:
		return campaign.FinalizationModeStrict
	default:
		return campaign.FinalizationModeAutoFail
	}
}

func isValidSuiteRunFinalizationMode(mode string) bool {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case campaign.FinalizationModeStrict, campaign.FinalizationModeAutoFail, campaign.FinalizationModeAutoFromResultJSON:
		return true
	default:
		return false
	}
}

func normalizeSuiteRunResultChannelKind(kind string) string {
	return strings.TrimSpace(strings.ToLower(kind))
}

func isValidSuiteRunResultChannelKind(kind string) bool {
	switch normalizeSuiteRunResultChannelKind(kind) {
	case campaign.ResultChannelNone, campaign.ResultChannelFileJSON, campaign.ResultChannelStdoutJSON:
		return true
	default:
		return false
	}
}

func maybeFinalizeSuiteFeedback(now time.Time, env map[string]string, ar *suiteRunAttemptResult, finalizationMode string, feedbackPolicy string, resultChannel suiteRunResultChannel, stdoutTB *tailBuffer) error {
	mode := normalizeSuiteRunFinalizationMode(finalizationMode, feedbackPolicy)
	switch mode {
	case campaign.FinalizationModeAutoFromResultJSON:
		return maybeWriteAutoResultFeedback(now, env, ar, resultChannel, stdoutTB)
	case campaign.FinalizationModeAutoFail:
		return maybeWriteAutoFailureFeedback(now, env, ar, schema.FeedbackPolicyAutoFailV1)
	default:
		return maybeWriteAutoFailureFeedback(now, env, ar, schema.FeedbackPolicyStrictV1)
	}
}

func maybeWriteAutoResultFeedback(now time.Time, env map[string]string, ar *suiteRunAttemptResult, resultChannel suiteRunResultChannel, stdoutTB *tailBuffer) error {
	outDir := strings.TrimSpace(env["ZCL_OUT_DIR"])
	if outDir == "" {
		return fmt.Errorf("suite run: missing ZCL_OUT_DIR for auto result finalization")
	}
	feedbackPath := filepath.Join(outDir, artifacts.FeedbackJSON)
	if fileExists(feedbackPath) {
		return nil
	}
	if ar.RunnerErrorCode != "" {
		return maybeWriteAutoFailureFeedback(now, env, ar, schema.FeedbackPolicyAutoFailV1)
	}
	if ar.RunnerExitCode != nil && *ar.RunnerExitCode != 0 {
		return maybeWriteAutoFailureFeedback(now, env, ar, schema.FeedbackPolicyAutoFailV1)
	}

	raw, err := readSuiteResultChannel(outDir, resultChannel, stdoutTB)
	if err != nil {
		return maybeWriteResultChannelFailureFeedback(now, env, ar, codeMissionResultMissing, err)
	}
	writeOpts, err := decodeSuiteResultFeedback(raw, resultChannel.MinFinalTurn)
	if err != nil {
		var turnErr *missionResultTurnTooEarlyError
		if errors.As(err, &turnErr) {
			return maybeWriteResultChannelFailureFeedback(now, env, ar, codeMissionResultTurnTooEarly, err)
		}
		return maybeWriteResultChannelFailureFeedback(now, env, ar, codeMissionResultInvalid, err)
	}

	envTrace := trace.Env{
		RunID:     env["ZCL_RUN_ID"],
		SuiteID:   env["ZCL_SUITE_ID"],
		MissionID: env["ZCL_MISSION_ID"],
		AttemptID: env["ZCL_ATTEMPT_ID"],
		AgentID:   env["ZCL_AGENT_ID"],
		OutDirAbs: outDir,
		TmpDirAbs: env["ZCL_TMP_DIR"],
	}
	if err := ensureAutoFeedbackTrace(now, envTrace, "suite-runner-result-channel", "", "auto finalization from mission result channel"); err != nil {
		return err
	}
	if err := feedback.Write(now, envTrace, writeOpts); err != nil {
		return err
	}
	ar.AutoFeedback = true
	ar.AutoFeedbackCode = ""
	return nil
}

func readSuiteResultChannel(outDir string, resultChannel suiteRunResultChannel, stdoutTB *tailBuffer) ([]byte, error) {
	kind := normalizeSuiteRunResultChannelKind(resultChannel.Kind)
	switch kind {
	case campaign.ResultChannelFileJSON:
		rel := strings.TrimSpace(resultChannel.Path)
		if rel == "" {
			rel = campaign.DefaultResultChannelPath
		}
		if filepath.IsAbs(rel) {
			return nil, fmt.Errorf("result channel file path must be attempt-relative")
		}
		path := filepath.Join(outDir, rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return b, nil
	case campaign.ResultChannelStdoutJSON:
		if stdoutTB == nil {
			return nil, fmt.Errorf("stdout result channel requires runner stdout capture")
		}
		buf, _, _ := stdoutTB.Snapshot()
		if len(buf) == 0 {
			return nil, fmt.Errorf("stdout result channel is empty")
		}
		marker := strings.TrimSpace(resultChannel.Marker)
		if marker == "" {
			marker = campaign.DefaultResultChannelMarker
		}
		return extractSuiteResultJSONFromStdout(buf, marker)
	default:
		return nil, fmt.Errorf("unsupported result channel kind %q", kind)
	}
}

func extractSuiteResultJSONFromStdout(buf []byte, marker string) ([]byte, error) {
	text := strings.TrimSpace(string(buf))
	if text == "" {
		return nil, fmt.Errorf("stdout result channel is empty")
	}
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, marker) {
			raw := strings.TrimSpace(strings.TrimPrefix(line, marker))
			if raw == "" {
				return nil, fmt.Errorf("stdout result marker found but payload is empty")
			}
			return []byte(raw), nil
		}
	}
	return nil, fmt.Errorf("stdout result marker %q not found", marker)
}

type missionResultTurnTooEarlyError struct {
	RequiredMin int
	Actual      int
	Missing     bool
}

func (e *missionResultTurnTooEarlyError) Error() string {
	if e == nil {
		return "mission result turn is below required minimum"
	}
	if e.Missing {
		return fmt.Sprintf("mission result requires integer field \"turn\" >= %d", e.RequiredMin)
	}
	return fmt.Sprintf("mission result turn %d is below required minimum %d", e.Actual, e.RequiredMin)
}

func decodeSuiteResultFeedback(raw []byte, minFinalTurn int) (feedback.WriteOpts, error) {
	return decodeSuiteResultFeedbackImpl(raw, minFinalTurn)
}

func decodeSuiteResultFeedbackImpl(raw []byte, minFinalTurn int) (feedback.WriteOpts, error) {
	return decodeSuiteResultFeedbackCore(raw, minFinalTurn)
}

func decodeSuiteResultFeedbackCore(raw []byte, minFinalTurn int) (feedback.WriteOpts, error) {
	minFinalTurn = normalizeSuiteResultMinFinalTurn(minFinalTurn)
	obj, err := decodeMissionResultObject(raw)
	if err != nil {
		return feedback.WriteOpts{}, err
	}
	okVal, err := decodeMissionResultOK(obj)
	if err != nil {
		return feedback.WriteOpts{}, err
	}
	if err := validateMissionResultTurnFloor(obj, minFinalTurn); err != nil {
		return feedback.WriteOpts{}, err
	}

	opts := feedback.WriteOpts{OK: okVal}
	if err := decodeMissionResultDecisionTags(&opts, obj); err != nil {
		return feedback.WriteOpts{}, err
	}
	if err := decodeMissionResultBody(&opts, obj); err != nil {
		return feedback.WriteOpts{}, err
	}
	if err := ensureMissionResultProof(&opts, obj); err != nil {
		return feedback.WriteOpts{}, err
	}
	return opts, nil
}

func normalizeSuiteResultMinFinalTurn(minFinalTurn int) int {
	if minFinalTurn <= 0 {
		return campaign.DefaultMinResultTurn
	}
	return minFinalTurn
}

func decodeMissionResultObject(raw []byte) (map[string]any, error) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("invalid mission result json: %w", err)
	}
	return obj, nil
}

func decodeMissionResultOK(obj map[string]any) (bool, error) {
	rawOK, ok := obj["ok"]
	if !ok {
		return false, fmt.Errorf("mission result requires boolean field \"ok\"")
	}
	okVal, ok := rawOK.(bool)
	if !ok {
		return false, fmt.Errorf("mission result field \"ok\" must be boolean")
	}
	return okVal, nil
}

func validateMissionResultTurnFloor(obj map[string]any, minFinalTurn int) error {
	turnVal, hasTurn, err := parseMissionResultTurn(obj)
	if err != nil {
		return err
	}
	if minFinalTurn <= campaign.DefaultMinResultTurn {
		return nil
	}
	if !hasTurn {
		return &missionResultTurnTooEarlyError{RequiredMin: minFinalTurn, Missing: true}
	}
	if turnVal < minFinalTurn {
		return &missionResultTurnTooEarlyError{RequiredMin: minFinalTurn, Actual: turnVal}
	}
	return nil
}

func decodeMissionResultDecisionTags(opts *feedback.WriteOpts, obj map[string]any) error {
	tags, present := obj["decisionTags"]
	if !present {
		return nil
	}
	parsedTags, err := toStringSlice(tags)
	if err != nil {
		return fmt.Errorf("mission result field \"decisionTags\" must be string array")
	}
	opts.DecisionTags = parsedTags
	return nil
}

func decodeMissionResultBody(opts *feedback.WriteOpts, obj map[string]any) error {
	if rawResult, present := obj["result"]; present {
		resultText, ok := rawResult.(string)
		if !ok {
			return fmt.Errorf("mission result field \"result\" must be string")
		}
		opts.Result = resultText
	}
	if rawResultJSON, present := obj["resultJson"]; present {
		b, err := store.CanonicalJSON(rawResultJSON)
		if err != nil {
			return fmt.Errorf("mission result field \"resultJson\" must be valid json")
		}
		opts.ResultJSON = string(b)
	}
	if opts.Result != "" && opts.ResultJSON != "" {
		return fmt.Errorf("mission result cannot include both \"result\" and \"resultJson\"")
	}
	return nil
}

func ensureMissionResultProof(opts *feedback.WriteOpts, obj map[string]any) error {
	if opts.Result != "" || opts.ResultJSON != "" {
		return nil
	}
	payload := missionResultFallbackPayload(obj)
	if len(payload) == 0 {
		return fmt.Errorf("mission result must include \"result\", \"resultJson\", or additional proof fields")
	}
	b, err := store.CanonicalJSON(payload)
	if err != nil {
		return err
	}
	opts.ResultJSON = string(b)
	return nil
}

func missionResultFallbackPayload(obj map[string]any) map[string]any {
	payload := map[string]any{}
	for k, v := range obj {
		switch strings.TrimSpace(k) {
		case "ok", "decisionTags", "turn":
			continue
		default:
			payload[k] = v
		}
	}
	return payload
}

func parseMissionResultTurn(obj map[string]any) (int, bool, error) {
	rawTurn, present := obj["turn"]
	if !present {
		return 0, false, nil
	}
	switch v := rawTurn.(type) {
	case float64:
		if v != float64(int(v)) {
			return 0, false, fmt.Errorf("mission result field \"turn\" must be integer")
		}
		if int(v) < 1 {
			return 0, false, fmt.Errorf("mission result field \"turn\" must be >= 1")
		}
		return int(v), true, nil
	default:
		return 0, false, fmt.Errorf("mission result field \"turn\" must be integer")
	}
}

func toStringSlice(v any) ([]string, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("not an array")
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, fmt.Errorf("non-string entry")
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func maybeWriteResultChannelFailureFeedback(now time.Time, env map[string]string, ar *suiteRunAttemptResult, code string, cause error) error {
	outDir := strings.TrimSpace(env["ZCL_OUT_DIR"])
	if outDir == "" {
		return fmt.Errorf("suite run: missing ZCL_OUT_DIR for auto-feedback")
	}
	feedbackPath := filepath.Join(outDir, artifacts.FeedbackJSON)
	if fileExists(feedbackPath) {
		return nil
	}
	envTrace := trace.Env{
		RunID:     env["ZCL_RUN_ID"],
		SuiteID:   env["ZCL_SUITE_ID"],
		MissionID: env["ZCL_MISSION_ID"],
		AttemptID: env["ZCL_ATTEMPT_ID"],
		AgentID:   env["ZCL_AGENT_ID"],
		OutDirAbs: outDir,
		TmpDirAbs: env["ZCL_TMP_DIR"],
	}
	msg := strings.TrimSpace(cause.Error())
	if msg == "" {
		msg = "mission result channel error"
	}
	if err := ensureAutoFeedbackTrace(now, envTrace, "suite-runner-result-channel", code, msg); err != nil {
		return err
	}
	result := map[string]any{
		"kind":   "infra_failure",
		"source": "result_channel",
		"code":   strings.TrimSpace(code),
		"error":  msg,
	}
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := feedback.Write(now, envTrace, feedback.WriteOpts{
		OK:                   false,
		ResultJSON:           string(b),
		DecisionTags:         []string{schema.DecisionTagBlocked},
		SkipSuiteResultShape: true,
	}); err != nil {
		return err
	}
	ar.AutoFeedback = true
	ar.AutoFeedbackCode = strings.TrimSpace(code)
	return nil
}

func ensureAutoFeedbackTrace(now time.Time, envTrace trace.Env, op string, code string, msg string) error {
	tracePath := filepath.Join(envTrace.OutDirAbs, artifacts.ToolCallsJSONL)
	nonEmpty, err := store.JSONLHasNonEmptyLine(tracePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if nonEmpty {
		return nil
	}
	argv := []string{"zcl", strings.TrimSpace(op)}
	res := trace.ResultForTrace{
		ExitCode:   0,
		DurationMs: 0,
		OutBytes:   int64(len(msg)),
		OutPreview: msg,
	}
	if strings.TrimSpace(code) != "" {
		res.SpawnError = strings.TrimSpace(code)
		res.OutBytes = 0
		res.OutPreview = ""
		res.ErrBytes = int64(len(msg))
		res.ErrPreview = msg
	}
	return trace.AppendCLIRunEvent(now, envTrace, argv, res)
}

func maybeWriteAutoFailureFeedback(now time.Time, env map[string]string, ar *suiteRunAttemptResult, feedbackPolicy string) error {
	return maybeWriteAutoFailureFeedbackImpl(now, env, ar, feedbackPolicy)
}

func maybeWriteAutoFailureFeedbackImpl(now time.Time, env map[string]string, ar *suiteRunAttemptResult, feedbackPolicy string) error {
	return maybeWriteAutoFailureFeedbackCore(now, env, ar, feedbackPolicy)
}

func maybeWriteAutoFailureFeedbackCore(now time.Time, env map[string]string, ar *suiteRunAttemptResult, feedbackPolicy string) error {
	outDir, shouldWrite, err := shouldWriteAutoFailureFeedback(env, feedbackPolicy)
	if err != nil || !shouldWrite {
		return err
	}
	envTrace := suiteRunTraceEnv(env, outDir)
	code := autoFailureCode(*ar)
	msg := autoFailureMessage(*ar)
	if err := ensureAutoFailureTraceEvent(now, envTrace, code, msg); err != nil {
		return err
	}
	resultJSON, err := autoFailureResultJSON(*ar, code)
	if err != nil {
		return err
	}
	if err := feedback.Write(now, envTrace, feedback.WriteOpts{
		OK:                   false,
		ResultJSON:           resultJSON,
		DecisionTags:         autoFailureDecisionTags(code, ar.RunnerErrorCode),
		SkipSuiteResultShape: true,
	}); err != nil {
		return err
	}
	ar.AutoFeedback = true
	ar.AutoFeedbackCode = code
	return nil
}

func shouldWriteAutoFailureFeedback(env map[string]string, feedbackPolicy string) (string, bool, error) {
	outDir := strings.TrimSpace(env["ZCL_OUT_DIR"])
	if outDir == "" {
		return "", false, fmt.Errorf("suite run: missing ZCL_OUT_DIR for auto-feedback")
	}
	if fileExists(filepath.Join(outDir, artifacts.FeedbackJSON)) {
		return outDir, false, nil
	}
	if schema.NormalizeFeedbackPolicyV1(feedbackPolicy) == schema.FeedbackPolicyStrictV1 {
		return outDir, false, nil
	}
	return outDir, true, nil
}

func suiteRunTraceEnv(env map[string]string, outDir string) trace.Env {
	return trace.Env{
		RunID:     env["ZCL_RUN_ID"],
		SuiteID:   env["ZCL_SUITE_ID"],
		MissionID: env["ZCL_MISSION_ID"],
		AttemptID: env["ZCL_ATTEMPT_ID"],
		AgentID:   env["ZCL_AGENT_ID"],
		OutDirAbs: outDir,
		TmpDirAbs: env["ZCL_TMP_DIR"],
	}
}

func autoFailureMessage(ar suiteRunAttemptResult) string {
	msg := "canonical feedback missing after suite runner completion"
	if ar.RunnerErrorCode != "" {
		msg += " runnerErrorCode=" + ar.RunnerErrorCode
	}
	if ar.RunnerExitCode != nil {
		msg += fmt.Sprintf(" runnerExitCode=%d", *ar.RunnerExitCode)
	}
	return msg
}

func ensureAutoFailureTraceEvent(now time.Time, envTrace trace.Env, code string, msg string) error {
	tracePath := filepath.Join(envTrace.OutDirAbs, artifacts.ToolCallsJSONL)
	nonEmpty, err := store.JSONLHasNonEmptyLine(tracePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if nonEmpty {
		return nil
	}
	return trace.AppendCLIRunEvent(now, envTrace, []string{"zcl", "suite-runner-auto-feedback"}, trace.ResultForTrace{
		SpawnError: code,
		DurationMs: 0,
		OutBytes:   0,
		ErrBytes:   int64(len(msg)),
		ErrPreview: msg,
	})
}

func autoFailureResultJSON(ar suiteRunAttemptResult, code string) (string, error) {
	result := map[string]any{
		"kind":   "infra_failure",
		"source": "suite_run",
		"code":   code,
	}
	if ar.RunnerErrorCode != "" {
		result["runnerErrorCode"] = ar.RunnerErrorCode
	}
	if ar.RunnerExitCode != nil {
		result["runnerExitCode"] = *ar.RunnerExitCode
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func autoFailureDecisionTags(code string, runnerErrorCode string) []string {
	decisionTags := []string{schema.DecisionTagBlocked}
	if code == codeTimeout || code == codeRuntimeStall || runnerErrorCode == codeTimeout || runnerErrorCode == codeRuntimeStall {
		return append(decisionTags, schema.DecisionTagTimeout)
	}
	return decisionTags
}

func autoFailureCode(ar suiteRunAttemptResult) string {
	if ar.RunnerErrorCode != "" {
		return ar.RunnerErrorCode
	}
	if ar.RunnerExitCode != nil && *ar.RunnerExitCode == 0 {
		return codeMissingArtifact
	}
	if ar.RunnerExitCode != nil && *ar.RunnerExitCode != 0 {
		return codeToolFailed
	}
	return codeMissingArtifact
}
