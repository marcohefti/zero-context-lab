package doctor

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/marcohefti/zero-context-lab/internal/config"
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

func Run(outRootFlag string) (Result, error) {
	m, err := config.LoadMerged(outRootFlag)
	if err != nil {
		return Result{}, err
	}
	outRoot := m.OutRoot

	res := Result{OK: true, OutRoot: outRoot}

	// Write access: create and remove a temp file under outRoot.
	if err := os.MkdirAll(filepath.Join(outRoot, "runs"), 0o755); err != nil {
		res.OK = false
		res.Checks = append(res.Checks, Check{ID: "write_access", OK: false, Message: err.Error()})
	} else {
		tmp := filepath.Join(outRoot, ".doctor.tmp")
		if err := os.WriteFile(tmp, []byte("ok\n"), 0o644); err != nil {
			res.OK = false
			res.Checks = append(res.Checks, Check{ID: "write_access", OK: false, Message: err.Error()})
		} else {
			_ = os.Remove(tmp)
			res.Checks = append(res.Checks, Check{ID: "write_access", OK: true})
		}
	}

	// Project config parse (best-effort): if present, it must parse.
	if _, err := os.Stat(config.DefaultProjectConfigPath); err == nil {
		if _, err := config.LoadMerged(""); err != nil {
			res.OK = false
			res.Checks = append(res.Checks, Check{ID: "project_config", OK: false, Message: err.Error()})
		} else {
			res.Checks = append(res.Checks, Check{ID: "project_config", OK: true})
		}
	} else {
		res.Checks = append(res.Checks, Check{ID: "project_config", OK: true, Message: "missing (ok)"})
	}

	// Optional runner availability: codex binary.
	if _, err := exec.LookPath("codex"); err == nil {
		res.Checks = append(res.Checks, Check{ID: "runner_codex", OK: true})
	} else {
		res.Checks = append(res.Checks, Check{ID: "runner_codex", OK: true, Message: "codex not on PATH (ok if not enriching)"})
	}

	return res, nil
}
