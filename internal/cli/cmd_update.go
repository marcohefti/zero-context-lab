package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/update"
)

func (r Runner) runUpdate(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printUpdateHelp(r.Stdout)
		return 0
	}
	switch args[0] {
	case "status":
		return r.runUpdateStatus(args[1:])
	default:
		fmt.Fprintf(r.Stderr, codeUsage+": unknown update subcommand %q\n", args[0])
		printUpdateHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runUpdateStatus(args []string) int {
	fs := flag.NewFlagSet("update status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	jsonOut := fs.Bool("json", false, "print JSON output")
	cached := fs.Bool("cached", false, "use cached status when available (avoid network refresh)")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("update status: invalid flags")
	}
	if *help {
		printUpdateStatusHelp(r.Stdout)
		return 0
	}

	res, err := update.StatusCheck(r.Version, r.Now(), update.StatusOptions{
		Refresh:   !*cached,
		CacheOnly: *cached,
		Timeout:   3 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}

	if *jsonOut {
		return r.writeJSON(res)
	}
	fmt.Fprintf(r.Stdout, "update: current=%s latest=%s available=%v cached=%v checkedAt=%s\n",
		res.CurrentVersion, res.LatestVersion, res.UpdateAvailable, res.Cached, res.CheckedAt)
	if strings.TrimSpace(res.Message) != "" {
		fmt.Fprintf(r.Stdout, "update: %s\n", res.Message)
	}
	fmt.Fprintf(r.Stdout, "update: policy=%s (no runtime auto-update)\n", res.Policy)
	if res.UpdateAvailable {
		fmt.Fprintf(r.Stdout, "update: npm: %s\n", res.Commands.NPM)
		fmt.Fprintf(r.Stdout, "update: brew: %s\n", res.Commands.Homebrew)
		fmt.Fprintf(r.Stdout, "update: go: %s\n", res.Commands.GoInstall)
	}
	return 0
}

func (r Runner) enforceVersionFloor(args []string) (int, bool) {
	min := strings.TrimSpace(os.Getenv("ZCL_MIN_VERSION"))
	if min == "" {
		return 0, false
	}
	if len(args) == 0 {
		return 0, false
	}
	for _, a := range args[1:] {
		if a == "-h" || a == "--help" || a == "help" {
			return 0, false
		}
	}
	// Always allow version introspection and update status checks.
	if args[0] == "version" || args[0] == "update" {
		return 0, false
	}

	ok, msg, err := update.CheckMinimum(r.Version, min)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeUsage+": %s\n", err.Error())
		return 2, true
	}
	if ok {
		return 0, false
	}
	fmt.Fprintf(r.Stderr, codeVersionFloor+": %s\n", msg)
	fmt.Fprintf(r.Stderr, codeVersionFloor+": run `zcl update status --json` then update via your package manager.\n")
	return 2, true
}

func (r Runner) maybePrintUpdateNotice(args []string) {
	if len(args) == 0 {
		return
	}
	if args[0] == "update" {
		return
	}
	force := strings.TrimSpace(os.Getenv("ZCL_ENABLE_UPDATE_NOTIFY")) == "1"
	if !force {
		if strings.TrimSpace(os.Getenv("ZCL_DISABLE_UPDATE_NOTIFY")) == "1" {
			return
		}
		// Avoid noise for agents/automation.
		if strings.TrimSpace(os.Getenv("CI")) != "" {
			return
		}
		if strings.TrimSpace(os.Getenv("ZCL_ATTEMPT_ID")) != "" || strings.TrimSpace(os.Getenv("ZCL_OUT_DIR")) != "" {
			return
		}
		if !isCharDevice(r.Stderr) {
			return
		}
	}

	now := r.Now()
	res, err := update.StatusCheck(r.Version, now, update.StatusOptions{
		Refresh: false,
		Timeout: 1500 * time.Millisecond,
	})
	if err != nil || !res.UpdateAvailable {
		return
	}

	recent, err := update.NotifiedRecently(now, 24*time.Hour)
	if err == nil && recent {
		return
	}

	fmt.Fprintf(r.Stderr, "zcl: update available %s -> %s (check: zcl update status --json)\n", res.CurrentVersion, res.LatestVersion)
	_ = update.MarkNotified(now)
}

func isCharDevice(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

func printUpdateHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl update status [--cached] [--json]
`)
}

func printUpdateStatusHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl update status [--cached] [--json]
`)
}
