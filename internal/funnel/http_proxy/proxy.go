package httpproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/redact"
	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
	"github.com/marcohefti/zero-context-lab/internal/trace"
)

type Result struct {
	ListenAddr string
}

type Handle struct {
	ListenAddr string
	done       chan error
	closeFn    func() error
}

func (h *Handle) Close() error {
	if h == nil || h.closeFn == nil {
		return nil
	}
	return h.closeFn()
}

func (h *Handle) Wait() error {
	if h == nil {
		return nil
	}
	return <-h.done
}

type boundedCapture struct {
	max       int
	buf       bytes.Buffer
	total     int64
	truncated bool
}

func (c *boundedCapture) Write(p []byte) (int, error) {
	c.total += int64(len(p))
	remaining := c.max - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	_, _ = c.buf.Write(p)
	return len(p), nil
}

func (c *boundedCapture) snapshot() (preview string, total int64, truncated bool) {
	return c.buf.String(), c.total, c.truncated
}

type countingReadCloser struct {
	rc    io.ReadCloser
	total int64
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	c.total += int64(n)
	return n, err
}

func (c *countingReadCloser) Close() error { return c.rc.Close() }

// Start starts a simple reverse proxy funnel that records one trace event per HTTP request.
// It runs until ctx is canceled (or maxRequests is reached when > 0).
func Start(ctx context.Context, env trace.Env, listenAddr string, upstream string, maxPreviewBytes int, maxRequests int) (*Handle, error) {
	if strings.TrimSpace(listenAddr) == "" {
		listenAddr = "127.0.0.1:0"
	}
	if strings.TrimSpace(upstream) == "" {
		return nil, fmt.Errorf("missing upstream url")
	}
	if maxPreviewBytes < 0 {
		maxPreviewBytes = 0
	}

	up, err := url.Parse(strings.TrimSpace(upstream))
	if err != nil {
		return nil, err
	}
	if up.Scheme != "http" && up.Scheme != "https" {
		return nil, fmt.Errorf("upstream url must be http(s)")
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}

	var handled int64

	tracePath := filepath.Join(env.OutDirAbs, "tool.calls.jsonl")
	client := &http.Client{Timeout: 60 * time.Second}

	srv := &http.Server{}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := atomic.AddInt64(&handled, 1)
		if maxRequests > 0 && int(idx) > maxRequests {
			http.Error(w, "proxy request limit reached", http.StatusServiceUnavailable)
			return
		}

		start := time.Now()
		ctxReq := r.Context()

		// Capture a small prefix for evidence, but forward the full body.
		var head []byte
		bodyRest := r.Body
		if bodyRest == nil {
			bodyRest = http.NoBody
		}
		if bodyRest != http.NoBody {
			head, _ = io.ReadAll(io.LimitReader(bodyRest, int64(maxPreviewBytes)+1))
		}
		forwardBody := io.NopCloser(io.MultiReader(bytes.NewReader(head), bodyRest))
		reqCounter := &countingReadCloser{rc: forwardBody}

		reqURL := *up
		reqURL.Path = singleJoiningSlash(up.Path, r.URL.Path)
		reqURL.RawQuery = r.URL.RawQuery
		_, urlApplied := redact.Text(reqURL.String())
		outReq, err := http.NewRequestWithContext(ctxReq, r.Method, reqURL.String(), reqCounter)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		outReq.Header = r.Header.Clone()

		resp, err := client.Do(outReq)
		if err != nil {
			http.Error(w, "upstream error", http.StatusBadGateway)
			writeHTTPEvent(start, env, tracePath, r.Method, reqURL.String(), reqCounter.total, 0, "", false, nil, maxPreviewBytes, err, urlApplied.Names)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		var cap boundedCapture
		cap.max = maxPreviewBytes
		tee := io.TeeReader(resp.Body, &cap)
		_, _ = io.Copy(w, tee)

		prev, total, trunc := cap.snapshot()
		prevRed, prevApplied := redact.Text(prev)
		prevRed, capped := capStringBytes(prevRed, maxPreviewBytes)
		writeHTTPEvent(start, env, tracePath, r.Method, reqURL.String(), reqCounter.total, total, prevRed, trunc || capped, &resp.StatusCode, maxPreviewBytes, nil, append(urlApplied.Names, prevApplied.Names...))

		if maxRequests > 0 && int(idx) >= maxRequests {
			go func() { _ = srv.Close() }()
		}
	})

	done := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			done <- err
			return
		}
		done <- nil
	}()

	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	h := &Handle{
		ListenAddr: ln.Addr().String(),
		done:       done,
		closeFn: func() error {
			_ = ln.Close()
			return srv.Close()
		},
	}
	return h, nil
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func capStringBytes(s string, maxBytes int) (string, bool) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(s) <= maxBytes {
		return s, false
	}
	return s[:maxBytes], true
}

func writeHTTPEvent(start time.Time, env trace.Env, tracePath string, method string, rawURL string, reqBytes int64, respBytes int64, outPreview string, outTruncated bool, status *int, maxPreviewBytes int, callErr error, redactions []string) {
	urlRed, _ := redact.Text(rawURL)

	inputAny := map[string]any{
		"method": method,
		"url":    urlRed,
	}
	inRaw, err := store.CanonicalJSON(inputAny)
	if err != nil {
		inRaw = []byte(`{"method":"` + method + `"}`)
	}

	ok := callErr == nil
	code := ""
	if callErr != nil {
		ok = false
		code = "HTTP_PROXY_ERROR"
	}
	if status != nil && *status >= 400 {
		ok = false
		code = fmt.Sprintf("HTTP_%d", *status)
	}

	en := map[string]any{
		"http": map[string]any{
			"status":          status,
			"reqBytes":        reqBytes,
			"respBytes":       respBytes,
			"maxPreviewBytes": maxPreviewBytes,
		},
	}
	if callErr != nil {
		en["http"].(map[string]any)["error"] = callErr.Error()
	}
	if outTruncated {
		en["http"].(map[string]any)["respPreviewTruncated"] = true
	}
	enRaw, err := store.CanonicalJSON(en)
	if err != nil || len(enRaw) > schema.EnrichmentMaxBytesV1 {
		enRaw = nil
	}

	ev := schema.TraceEventV1{
		V:         schema.TraceSchemaV1,
		TS:        start.UTC().Format(time.RFC3339Nano),
		RunID:     env.RunID,
		SuiteID:   env.SuiteID,
		MissionID: env.MissionID,
		AttemptID: env.AttemptID,
		AgentID:   env.AgentID,
		Tool:      "http",
		Op:        "request",
		Input:     inRaw,
		Result: schema.TraceResultV1{
			OK:         ok,
			Code:       code,
			DurationMs: time.Since(start).Milliseconds(),
		},
		IO: schema.TraceIOV1{
			OutBytes:   respBytes,
			ErrBytes:   0,
			OutPreview: outPreview,
		},
		RedactionsApplied: redactions,
		Integrity: &schema.TraceIntegrityV1{
			Truncated: outTruncated,
		},
	}
	if enRaw != nil {
		ev.Enrichment = enRaw
	}
	_ = store.AppendJSONL(tracePath, ev)
}
