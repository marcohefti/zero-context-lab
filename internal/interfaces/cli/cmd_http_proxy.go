package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	httpproxy "github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/http_proxy"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/trace"
	"github.com/marcohefti/zero-context-lab/internal/contexts/execution/app/attempt"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
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
		fmt.Fprintf(r.Stderr, codeUsage+": unknown http subcommand %q\n", args[0])
		printHTTPHelp(r.Stderr)
		return 2
	}
}

func (r Runner) runHTTPProxy(args []string) int {
	opts, exit, done := r.parseHTTPProxyOptions(args)
	if done {
		return exit
	}
	env, exit, done := r.loadHTTPProxyContext()
	if done {
		return exit
	}
	ctx, cancel, timedOut, exit, done := r.prepareHTTPProxyContext(r.Now(), env.OutDirAbs)
	if done {
		return exit
	}
	if cancel != nil {
		defer cancel()
	}
	if timedOut {
		fmt.Fprintf(r.Stderr, codeTimeout+": attempt deadline exceeded\n")
		return 1
	}

	h, err := httpproxy.Start(ctx, env, opts.listen, opts.upstream, schema.PreviewMaxBytesV1, opts.maxRequests)
	if err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return 1
	}
	defer func() { _ = h.Close() }()

	if opts.jsonOut {
		if exit := r.writeHTTPProxyJSON(h.ListenAddr, opts.upstream); exit != 0 {
			return exit
		}
	}
	return r.waitHTTPProxy(ctx, h.Wait)
}

type httpProxyOptions struct {
	listen      string
	upstream    string
	maxRequests int
	jsonOut     bool
}

func (r Runner) parseHTTPProxyOptions(args []string) (httpProxyOptions, int, bool) {
	fs := flag.NewFlagSet("http proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	listen := fs.String("listen", "127.0.0.1:0", "listen address (default 127.0.0.1:0)")
	upstream := fs.String("upstream", "", "upstream base URL (required; http(s)://...)")
	maxRequests := fs.Int("max-requests", 0, "stop after N requests (0 means run until canceled)")
	jsonOut := fs.Bool("json", false, "print JSON output (prints listen addr) and keep running")
	help := fs.Bool("help", false, "show help")

	if err := fs.Parse(args); err != nil {
		return httpProxyOptions{}, r.failUsage("http proxy: invalid flags"), true
	}
	if *help {
		printHTTPProxyHelp(r.Stdout)
		return httpProxyOptions{}, 0, true
	}
	return httpProxyOptions{
		listen:      strings.TrimSpace(*listen),
		upstream:    strings.TrimSpace(*upstream),
		maxRequests: *maxRequests,
		jsonOut:     *jsonOut,
	}, 0, false
}

func (r Runner) loadHTTPProxyContext() (trace.Env, int, bool) {
	env, err := trace.EnvFromProcess()
	if err != nil {
		printHTTPProxyHelp(r.Stderr)
		return trace.Env{}, r.failUsage("http proxy: missing ZCL attempt context (need ZCL_* env)"), true
	}
	if a, err := attempt.ReadAttempt(env.OutDirAbs); err != nil {
		printHTTPProxyHelp(r.Stderr)
		return trace.Env{}, r.failUsage("http proxy: missing/invalid attempt.json in ZCL_OUT_DIR (need zcl attempt start context)"), true
	} else if a.RunID != env.RunID || a.SuiteID != env.SuiteID || a.MissionID != env.MissionID || a.AttemptID != env.AttemptID {
		printHTTPProxyHelp(r.Stderr)
		return trace.Env{}, r.failUsage("http proxy: attempt.json ids do not match ZCL_* env (refuse to run)"), true
	}
	return env, 0, false
}

func (r Runner) prepareHTTPProxyContext(now time.Time, attemptDir string) (context.Context, context.CancelFunc, bool, int, bool) {
	if _, err := attempt.EnsureTimeoutAnchor(now, attemptDir); err != nil {
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
		return context.Background(), nil, false, 1, true
	}
	ctx, cancel, timedOut := attemptCtxForDeadline(now, attemptDir)
	return ctx, cancel, timedOut, 0, false
}

func (r Runner) writeHTTPProxyJSON(listenAddr, upstream string) int {
	out := struct {
		OK         bool   `json:"ok"`
		ListenAddr string `json:"listenAddr"`
		ProxyURL   string `json:"proxyUrl"`
		Upstream   string `json:"upstream"`
	}{
		OK:         true,
		ListenAddr: listenAddr,
		ProxyURL:   "http://" + listenAddr,
		Upstream:   upstream,
	}
	if r.writeJSON(out) != 0 {
		return 1
	}
	return 0
}

func (r Runner) waitHTTPProxy(ctx context.Context, waitFn func() error) int {
	if err := waitFn(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(r.Stderr, codeTimeout+": attempt deadline exceeded\n")
			return 1
		}
		fmt.Fprintf(r.Stderr, codeIO+": %s\n", err.Error())
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
