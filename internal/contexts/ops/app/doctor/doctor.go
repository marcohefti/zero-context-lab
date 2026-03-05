package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/marcohefti/zero-context-lab/internal/contexts/runtime/ports/native"
	"github.com/marcohefti/zero-context-lab/internal/kernel/config"
)

type Check struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type Result struct {
	OK      bool    `json:"ok"`
	OutRoot string  `json:"outRoot"`
	Checks  []Check `json:"checks"`
}

type Opts struct {
	OutRootFlag    string
	NativeRuntimes []native.Runtime
}

func Run(ctx context.Context, opts Opts) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	m, err := config.LoadMerged(opts.OutRootFlag)
	if err != nil {
		return Result{}, err
	}
	outRoot := m.OutRoot

	res := Result{OK: true, OutRoot: outRoot}
	add := func(check Check) {
		if !check.OK {
			res.OK = false
		}
		res.Checks = append(res.Checks, check)
	}

	add(checkWriteAccess(outRoot))
	add(checkProjectConfig())
	add(checkRedactionConfig())
	add(checkCodexRunner())
	for _, runtime := range opts.NativeRuntimes {
		if runtime == nil {
			continue
		}
		add(checkRuntime(ctx, runtime))
	}
	health := native.HealthSnapshot()
	if len(health) == 0 {
		add(Check{ID: "runtime_health", OK: true, Message: "no runtime health counters recorded yet"})
	} else {
		add(Check{ID: "runtime_health", OK: true, Message: "runtime health counters available"})
	}

	return res, nil
}

func checkWriteAccess(outRoot string) Check {
	if err := os.MkdirAll(filepath.Join(outRoot, "runs"), 0o755); err != nil {
		return Check{ID: "write_access", OK: false, Message: err.Error()}
	}
	tmp := filepath.Join(outRoot, ".doctor.tmp")
	if err := os.WriteFile(tmp, []byte("ok\n"), 0o600); err != nil {
		return Check{ID: "write_access", OK: false, Message: err.Error()}
	}
	_ = os.Remove(tmp)
	return Check{ID: "write_access", OK: true}
}

func checkProjectConfig() Check {
	if _, err := os.Stat(config.DefaultProjectConfigPath); err != nil {
		return Check{ID: "project_config", OK: true, Message: "missing (ok)"}
	}
	if _, err := config.LoadMerged(""); err != nil {
		return Check{ID: "project_config", OK: false, Message: err.Error()}
	}
	return Check{ID: "project_config", OK: true}
}

func checkRedactionConfig() Check {
	if _, err := config.LoadRedactionMerged(); err != nil {
		return Check{ID: "redaction_config", OK: false, Message: err.Error()}
	}
	return Check{ID: "redaction_config", OK: true}
}

func checkCodexRunner() Check {
	if _, err := exec.LookPath("codex"); err == nil {
		return Check{ID: "runner_codex", OK: true}
	}
	return Check{ID: "runner_codex", OK: true, Message: "codex not on PATH (ok if not enriching)"}
}

func checkRuntime(ctx context.Context, runtime native.Runtime) Check {
	checkID := "runtime_" + string(runtime.ID())
	if err := runtime.Probe(ctx); err != nil {
		return Check{ID: checkID, OK: true, Message: "native runtime unavailable (" + err.Error() + ")"}
	}
	return Check{ID: checkID, OK: true}
}
