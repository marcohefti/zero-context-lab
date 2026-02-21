package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
)

func (r Runner) runAttemptEnv(args []string) int {
	fs := flag.NewFlagSet("attempt env", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "sh", "output format: sh|dotenv")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("attempt env: invalid flags")
	}
	if *help {
		printAttemptEnvHelp(r.Stdout)
		return 0
	}
	formatName := strings.TrimSpace(*format)
	if formatName == "" {
		formatName = "sh"
	}
	if formatName != "sh" && formatName != "dotenv" {
		printAttemptEnvHelp(r.Stderr)
		return r.failUsage("attempt env: invalid --format (expected sh|dotenv)")
	}
	rest := fs.Args()
	if len(rest) > 1 {
		printAttemptEnvHelp(r.Stderr)
		return r.failUsage("attempt env: require at most one <attemptDir> (or use ZCL_OUT_DIR)")
	}

	attemptDir := ""
	if len(rest) == 1 {
		attemptDir = rest[0]
	} else {
		attemptDir = os.Getenv("ZCL_OUT_DIR")
	}
	if strings.TrimSpace(attemptDir) == "" {
		printAttemptEnvHelp(r.Stderr)
		return r.failUsage("attempt env: missing <attemptDir> (or set ZCL_OUT_DIR)")
	}
	attemptDirAbs, err := filepath.Abs(attemptDir)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	info, err := os.Stat(attemptDirAbs)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	if !info.IsDir() {
		return r.failUsage("attempt env: target must be a directory")
	}

	a, err := attempt.ReadAttempt(attemptDirAbs)
	if err != nil {
		return r.failUsage("attempt env: missing/invalid attempt.json")
	}
	env, err := attempt.EnvForAttempt(attemptDirAbs, a)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	envPath, err := attempt.AttemptEnvSHPath(attemptDirAbs, a)
	if err != nil {
		return r.failUsage("attempt env: invalid attemptEnvSh in attempt.json")
	}
	if !fileExists(envPath) {
		// Backfill for older attempts that predate attempt.env.sh.
		if err := attempt.WriteEnvSh(envPath, env); err != nil {
			fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
			return 1
		}
	}

	if *jsonOut {
		out := struct {
			OK             bool              `json:"ok"`
			AttemptDir     string            `json:"attemptDir"`
			AttemptEnvFile string            `json:"attemptEnvFile"`
			Format         string            `json:"format"`
			Env            map[string]string `json:"env"`
		}{
			OK:             true,
			AttemptDir:     attemptDirAbs,
			AttemptEnvFile: envPath,
			Format:         formatName,
			Env:            env,
		}
		return r.writeJSON(out)
	}

	txt, _ := formatEnv(env, formatName)
	fmt.Fprint(r.Stdout, txt)
	return 0
}
