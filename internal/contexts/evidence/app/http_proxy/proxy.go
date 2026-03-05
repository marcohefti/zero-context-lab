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
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/redact"
	"github.com/marcohefti/zero-context-lab/internal/contexts/evidence/app/trace"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
	"github.com/marcohefti/zero-context-lab/internal/kernel/store"
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
	cfg, err := newProxyStartConfig(listenAddr, upstream, maxPreviewBytes, maxRequests, env)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", cfg.listenAddr)
	if err != nil {
		return nil, err
	}

	server := newProxyServer(cfg)
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           http.HandlerFunc(server.handleRequest),
	}
	server.srv = srv

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

type proxyStartConfig struct {
	listenAddr      string
	maxPreviewBytes int
	maxRequests     int
	env             trace.Env
	up              *url.URL
}

func newProxyStartConfig(listenAddr, upstream string, maxPreviewBytes, maxRequests int, env trace.Env) (proxyStartConfig, error) {
	if strings.TrimSpace(listenAddr) == "" {
		listenAddr = "127.0.0.1:0"
	}
	if strings.TrimSpace(upstream) == "" {
		return proxyStartConfig{}, fmt.Errorf("missing upstream url")
	}
	if maxPreviewBytes < 0 {
		maxPreviewBytes = 0
	}
	up, err := url.Parse(strings.TrimSpace(upstream))
	if err != nil {
		return proxyStartConfig{}, err
	}
	if up.Scheme != "http" && up.Scheme != "https" {
		return proxyStartConfig{}, fmt.Errorf("upstream url must be http(s)")
	}
	return proxyStartConfig{
		listenAddr:      listenAddr,
		maxPreviewBytes: maxPreviewBytes,
		maxRequests:     maxRequests,
		env:             env,
		up:              up,
	}, nil
}

type proxyServer struct {
	env             trace.Env
	tracePath       string
	client          *http.Client
	up              *url.URL
	maxPreviewBytes int
	maxRequests     int
	handled         int64
	stopOnce        sync.Once
	srv             *http.Server
}

func newProxyServer(cfg proxyStartConfig) *proxyServer {
	return &proxyServer{
		env:             cfg.env,
		tracePath:       filepath.Join(cfg.env.OutDirAbs, "tool.calls.jsonl"),
		client:          &http.Client{Timeout: 60 * time.Second},
		up:              cfg.up,
		maxPreviewBytes: cfg.maxPreviewBytes,
		maxRequests:     cfg.maxRequests,
	}
}

func (p *proxyServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	idx, blocked := p.nextRequest()
	if blocked {
		http.Error(w, "proxy request limit reached", http.StatusServiceUnavailable)
		return
	}
	start := time.Now()

	reqCtx, err := p.newUpstreamRequest(r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	resp, err := p.client.Do(reqCtx.request)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		writeHTTPEvent(start, p.env, p.tracePath, r.Method, reqCtx.url, reqCtx.reqBytes(), 0, "", false, nil, p.maxPreviewBytes, err, reqCtx.urlRedactions)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	out := p.streamResponse(w, resp)
	writeHTTPEvent(start, p.env, p.tracePath, r.Method, reqCtx.url, reqCtx.reqBytes(), out.totalBytes, out.preview, out.truncated, &resp.StatusCode, p.maxPreviewBytes, nil, append(reqCtx.urlRedactions, out.previewRedactions...))
	p.shutdownAtLimit(idx)
}

func (p *proxyServer) nextRequest() (int64, bool) {
	idx := atomic.AddInt64(&p.handled, 1)
	if p.maxRequests > 0 && int(idx) > p.maxRequests {
		return idx, true
	}
	return idx, false
}

type upstreamRequestContext struct {
	request       *http.Request
	requestBody   *countingReadCloser
	url           string
	urlRedactions []string
}

func (c upstreamRequestContext) reqBytes() int64 {
	if c.requestBody == nil {
		return 0
	}
	return c.requestBody.total
}

func (p *proxyServer) newUpstreamRequest(r *http.Request) (upstreamRequestContext, error) {
	reqBody := p.captureForwardBody(r.Body)
	reqURL := *p.up
	reqURL.Path = singleJoiningSlash(p.up.Path, r.URL.Path)
	reqURL.RawQuery = r.URL.RawQuery
	_, urlApplied := redact.Text(reqURL.String())
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, reqURL.String(), reqBody)
	if err != nil {
		return upstreamRequestContext{}, err
	}
	outReq.Header = r.Header.Clone()
	return upstreamRequestContext{
		request:       outReq,
		requestBody:   reqBody,
		url:           reqURL.String(),
		urlRedactions: urlApplied.Names,
	}, nil
}

func (p *proxyServer) captureForwardBody(body io.ReadCloser) *countingReadCloser {
	bodyRest := body
	if bodyRest == nil {
		bodyRest = http.NoBody
	}
	var head []byte
	if bodyRest != http.NoBody {
		head, _ = io.ReadAll(io.LimitReader(bodyRest, int64(p.maxPreviewBytes)+1))
	}
	forwardBody := io.NopCloser(io.MultiReader(bytes.NewReader(head), bodyRest))
	return &countingReadCloser{rc: forwardBody}
}

type streamedResponse struct {
	preview           string
	totalBytes        int64
	truncated         bool
	previewRedactions []string
}

func (p *proxyServer) streamResponse(w http.ResponseWriter, resp *http.Response) streamedResponse {
	copyResponseHeaders(w, resp.Header)
	w.WriteHeader(resp.StatusCode)

	var cap boundedCapture
	cap.max = p.maxPreviewBytes
	tee := io.TeeReader(resp.Body, &cap)
	_, _ = io.Copy(w, tee)

	prev, total, trunc := cap.snapshot()
	prevRed, prevApplied := redact.Text(prev)
	prevRed, capped := capStringBytes(prevRed, p.maxPreviewBytes)
	return streamedResponse{
		preview:           prevRed,
		totalBytes:        total,
		truncated:         trunc || capped,
		previewRedactions: prevApplied.Names,
	}
}

func copyResponseHeaders(w http.ResponseWriter, headers http.Header) {
	for k, vv := range headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
}

func (p *proxyServer) shutdownAtLimit(idx int64) {
	if p.maxRequests <= 0 || int(idx) < p.maxRequests {
		return
	}
	p.stopOnce.Do(func() {
		go func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = p.srv.Shutdown(shutdownCtx)
		}()
	})
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
