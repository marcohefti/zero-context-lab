package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/attempt"
)

func (r Runner) runAttemptEnv(args []string) int {
	opts, exit, done := r.parseAttemptEnvOptions(args)
	if done {
		return exit
	}
	attemptDirAbs, exit, done := r.resolveAttemptEnvDir(opts.attemptDir)
	if done {
		return exit
	}
	env, envPath, exit, done := r.loadAttemptEnvPayload(attemptDirAbs)
	if done {
		return exit
	}
	if opts.jsonOut {
		return r.writeAttemptEnvJSON(attemptDirAbs, envPath, opts.formatName, env)
	}
	txt, _ := formatEnv(env, opts.formatName)
	fmt.Fprint(r.Stdout, txt)
	return 0
}

type attemptEnvOptions struct {
	formatName string
	jsonOut    bool
	attemptDir string
}

func (r Runner) parseAttemptEnvOptions(args []string) (attemptEnvOptions, int, bool) {
	fs := flag.NewFlagSet("attempt env", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", "sh", "output format: sh|dotenv")
	jsonOut := fs.Bool("json", false, "print JSON output")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return attemptEnvOptions{}, r.failUsage("attempt env: invalid flags"), true
	}
	if *help {
		printAttemptEnvHelp(r.Stdout)
		return attemptEnvOptions{}, 0, true
	}
	formatName := strings.TrimSpace(*format)
	if formatName == "" {
		formatName = "sh"
	}
	if formatName != "sh" && formatName != "dotenv" {
		printAttemptEnvHelp(r.Stderr)
		return attemptEnvOptions{}, r.failUsage("attempt env: invalid --format (expected sh|dotenv)"), true
	}
	rest := fs.Args()
	if len(rest) > 1 {
		printAttemptEnvHelp(r.Stderr)
		return attemptEnvOptions{}, r.failUsage("attempt env: require at most one <attemptDir> (or use ZCL_OUT_DIR)"), true
	}
	attemptDir := os.Getenv("ZCL_OUT_DIR")
	if len(rest) == 1 {
		attemptDir = rest[0]
	}
	if strings.TrimSpace(attemptDir) == "" {
		printAttemptEnvHelp(r.Stderr)
		return attemptEnvOptions{}, r.failUsage("attempt env: missing <attemptDir> (or set ZCL_OUT_DIR)"), true
	}
	return attemptEnvOptions{
		formatName: formatName,
		jsonOut:    *jsonOut,
		attemptDir: attemptDir,
	}, 0, false
}

func (r Runner) resolveAttemptEnvDir(attemptDir string) (string, int, bool) {
	attemptDirAbs, err := filepath.Abs(attemptDir)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return "", 1, true
	}
	info, err := os.Stat(attemptDirAbs)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return "", 1, true
	}
	if !info.IsDir() {
		return "", r.failUsage("attempt env: target must be a directory"), true
	}
	return attemptDirAbs, 0, false
}

func (r Runner) loadAttemptEnvPayload(attemptDirAbs string) (map[string]string, string, int, bool) {
	a, err := attempt.ReadAttempt(attemptDirAbs)
	if err != nil {
		return nil, "", r.failUsage("attempt env: missing/invalid attempt.json"), true
	}
	env, err := attempt.EnvForAttempt(attemptDirAbs, a)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return nil, "", 1, true
	}
	envPath, err := attempt.AttemptEnvSHPath(attemptDirAbs, a)
	if err != nil {
		return nil, "", r.failUsage("attempt env: invalid attemptEnvSh in attempt.json"), true
	}
	if !fileExists(envPath) {
		// Backfill for older attempts that predate attempt.env.sh.
		if err := attempt.WriteEnvSh(envPath, env); err != nil {
			fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
			return nil, "", 1, true
		}
	}
	return env, envPath, 0, false
}

func (r Runner) writeAttemptEnvJSON(attemptDirAbs, envPath, formatName string, env map[string]string) int {
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
