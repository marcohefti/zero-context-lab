package attempt

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
)

// EnvForAttempt reconstructs canonical ZCL_* env for an attempt directory.
func EnvForAttempt(attemptDir string, a schema.AttemptJSONV1) (map[string]string, error) {
	outDirAbs, err := filepath.Abs(strings.TrimSpace(attemptDir))
	if err != nil {
		return nil, err
	}
	runDir := filepath.Dir(filepath.Dir(outDirAbs))
	outRootAbs := filepath.Dir(filepath.Dir(runDir))

	tmpDirAbs := ""
	if strings.TrimSpace(a.ScratchDir) != "" {
		if filepath.IsAbs(a.ScratchDir) {
			tmpDirAbs = filepath.Clean(a.ScratchDir)
		} else {
			tmpDirAbs = filepath.Join(outRootAbs, a.ScratchDir)
		}
	} else if a.RunID != "" && a.AttemptID != "" {
		// Backward-compatible fallback for older attempt.json files.
		tmpDirAbs = filepath.Join(outRootAbs, "tmp", a.RunID, a.AttemptID)
	}

	env := map[string]string{
		"ZCL_RUN_ID":     a.RunID,
		"ZCL_SUITE_ID":   a.SuiteID,
		"ZCL_MISSION_ID": a.MissionID,
		"ZCL_ATTEMPT_ID": a.AttemptID,
		"ZCL_OUT_DIR":    outDirAbs,
	}
	if tmpDirAbs != "" {
		env["ZCL_TMP_DIR"] = tmpDirAbs
	}
	if strings.TrimSpace(a.AgentID) != "" {
		env["ZCL_AGENT_ID"] = a.AgentID
	}
	if strings.TrimSpace(a.IsolationModel) != "" {
		env["ZCL_ISOLATION_MODEL"] = a.IsolationModel
	}
	return env, nil
}

// AttemptEnvSHPath resolves attempt.env.sh path for an attempt directory.
func AttemptEnvSHPath(attemptDir string, a schema.AttemptJSONV1) (string, error) {
	baseDir, err := filepath.Abs(strings.TrimSpace(attemptDir))
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(a.AttemptEnvSH)
	if name == "" {
		name = schema.AttemptEnvShFileNameV1
	}
	if filepath.IsAbs(name) {
		return filepath.Clean(name), nil
	}
	if strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return "", fmt.Errorf("invalid attemptEnvSh path (must be a file name)")
	}
	return filepath.Join(baseDir, name), nil
}

func WriteEnvSh(path string, env map[string]string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("missing env output path")
	}
	return store.WriteFileAtomic(path, []byte(formatEnvSh(env)))
}

func formatEnvSh(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("export ")
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(shQuote(env[k]))
		b.WriteByte('\n')
	}
	return b.String()
}

func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
