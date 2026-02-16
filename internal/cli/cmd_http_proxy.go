package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/attempt"
	httpproxy "github.com/marcohefti/zero-context-lab/internal/funnel/http_proxy"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

func (r Runner) runHTTP(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printHTTPHelp(r.Stdout)
		return 0
	}
	switch args[0] {
	case "proxy":
		return r.runHTTPProxy(args[1:])
	default:
		fmt.Fprintf(r.Stderr, "ZCL_E_USAGE: unknown http subcommand %q\n", args[0])
		printHTTPHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runHTTPProxy(args []string) int {
	fs := flag.NewFlagSet("http proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	listen := fs.String("listen", "127.0.0.1:0", "listen address (default 127.0.0.1:0)")
	upstream := fs.String("upstream", "", "upstream base URL (required; http(s)://...)")
	maxRequests := fs.Int("max-requests", 0, "stop after N requests (0 means run until canceled)")
	jsonOut := fs.Bool("json", false, "print JSON output (prints listen addr) and keep running")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return r.failUsage("http proxy: invalid flags")
	}
	if *help {
		printHTTPProxyHelp(r.Stdout)
		return 0
	}

	env, err := trace.EnvFromProcess()
	if err != nil {
		printHTTPProxyHelp(r.Stderr)
		return r.failUsage("http proxy: missing ZCL attempt context (need ZCL_* env)")
	}
	if a, err := attempt.ReadAttempt(env.OutDirAbs); err != nil {
		printHTTPProxyHelp(r.Stderr)
		return r.failUsage("http proxy: missing/invalid attempt.json in ZCL_OUT_DIR (need zcl attempt start context)")
	} else if a.RunID != env.RunID || a.SuiteID != env.SuiteID || a.MissionID != env.MissionID || a.AttemptID != env.AttemptID {
		printHTTPProxyHelp(r.Stderr)
		return r.failUsage("http proxy: attempt.json ids do not match ZCL_* env (refuse to run)")
	}

	now := r.Now()
	ctx, cancel, timedOut := attemptCtxForDeadline(now, env.OutDirAbs)
	if cancel != nil {
		defer cancel()
	}
	if timedOut {
		fmt.Fprintf(r.Stderr, "ZCL_E_TIMEOUT: attempt deadline exceeded\n")
		return 1
	}

	h, err := httpproxy.Start(ctx, env, strings.TrimSpace(*listen), strings.TrimSpace(*upstream), schema.PreviewMaxBytesV1, *maxRequests)
	if err != nil {
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	defer func() { _ = h.Close() }()

	if *jsonOut {
		out := struct {
			OK         bool   `json:"ok"`
			ListenAddr string `json:"listenAddr"`
			ProxyURL   string `json:"proxyUrl"`
			Upstream   string `json:"upstream"`
		}{
			OK:         true,
			ListenAddr: h.ListenAddr,
			ProxyURL:   "http://" + h.ListenAddr,
			Upstream:   strings.TrimSpace(*upstream),
		}
		if r.writeJSON(out) != 0 {
			return 1
		}
	}

	if err := h.Wait(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(r.Stderr, "ZCL_E_TIMEOUT: attempt deadline exceeded\n")
			return 1
		}
		fmt.Fprintf(r.Stderr, "ZCL_E_IO: %s\n", err.Error())
		return 1
	}
	return 0
}

func printHTTPHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  zcl http proxy --upstream <url> [--listen 127.0.0.1:0] [--max-requests N] [--json]
`)
}

func printHTTPProxyHelp(w io.Writer) {
	printHTTPHelp(w)
	fmt.Fprint(w, `
Notes:
  - Requires ZCL attempt context (ZCL_* env + attempt.json in ZCL_OUT_DIR).
  - Emits one tool.calls.jsonl event per proxied request (tool=http op=request).
`)
}
