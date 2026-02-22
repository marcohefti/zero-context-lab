package codexappserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/native"
	"github.com/marcohefti/zero-context-lab/internal/native/conformance"
)

func TestCodexRuntimeSmokeOneTurn(t *testing.T) {
	rt := NewRuntime(Config{
		Command:         []string{os.Args[0], "-test.run=TestCodexAppServerHelperProcess"},
		StartupTimeout:  2 * time.Second,
		RequestTimeout:  2 * time.Second,
		ShutdownTimeout: 2 * time.Second,
		ExtraEnv: map[string]string{
			"ZCL_HELPER_PROCESS": "1",
			"ZCL_HELPER_MODE":    "smoke",
		},
	})

	if err := conformance.RunBasic(context.Background(), rt, conformance.BasicSuiteOptions{
		SessionOptions: native.SessionOptions{
			Env: map[string]string{"ZCL_RUN_ID": "r"},
		},
	}); err != nil {
		t.Fatalf("codex runtime conformance failed: %v", err)
	}
}

func TestCodexRuntimeCompatibilityError(t *testing.T) {
	rt := NewRuntime(Config{
		Command:         []string{os.Args[0], "-test.run=TestCodexAppServerHelperProcess"},
		StartupTimeout:  2 * time.Second,
		RequestTimeout:  2 * time.Second,
		ShutdownTimeout: 2 * time.Second,
		ExtraEnv: map[string]string{
			"ZCL_HELPER_PROCESS": "1",
			"ZCL_HELPER_MODE":    "compat_missing_method",
		},
	})

	_, err := rt.StartSession(context.Background(), native.SessionOptions{Env: map[string]string{"ZCL_RUN_ID": "r"}})
	if err == nil {
		t.Fatalf("expected compatibility failure")
	}
	nerr, ok := native.AsError(err)
	if !ok {
		t.Fatalf("expected native error, got %T", err)
	}
	if nerr.Kind != native.ErrorCompatibility {
		t.Fatalf("expected compatibility error, got %q (%v)", nerr.Kind, err)
	}
}

func TestCodexRuntimeTimeoutError(t *testing.T) {
	rt := NewRuntime(Config{
		Command:         []string{os.Args[0], "-test.run=TestCodexAppServerHelperProcess"},
		StartupTimeout:  350 * time.Millisecond,
		RequestTimeout:  150 * time.Millisecond,
		ShutdownTimeout: 300 * time.Millisecond,
		ExtraEnv: map[string]string{
			"ZCL_HELPER_PROCESS": "1",
			"ZCL_HELPER_MODE":    "timeout_on_model_list",
		},
	})

	_, err := rt.StartSession(context.Background(), native.SessionOptions{Env: map[string]string{"ZCL_RUN_ID": "r"}})
	if err == nil {
		t.Fatalf("expected timeout failure")
	}
	nerr, ok := native.AsError(err)
	if !ok {
		t.Fatalf("expected native error, got %T", err)
	}
	if nerr.Kind != native.ErrorTimeout {
		t.Fatalf("expected timeout error, got %q (%v)", nerr.Kind, err)
	}
}

func TestCodexRuntimeCrashError(t *testing.T) {
	rt := NewRuntime(Config{
		Command:         []string{os.Args[0], "-test.run=TestCodexAppServerHelperProcess"},
		StartupTimeout:  600 * time.Millisecond,
		RequestTimeout:  300 * time.Millisecond,
		ShutdownTimeout: 300 * time.Millisecond,
		ExtraEnv: map[string]string{
			"ZCL_HELPER_PROCESS": "1",
			"ZCL_HELPER_MODE":    "disconnect_after_initialized",
		},
	})

	_, err := rt.StartSession(context.Background(), native.SessionOptions{Env: map[string]string{"ZCL_RUN_ID": "r"}})
	if err == nil {
		t.Fatalf("expected crash failure")
	}
	nerr, ok := native.AsError(err)
	if !ok {
		t.Fatalf("expected native error, got %T", err)
	}
	if nerr.Kind != native.ErrorCrash && nerr.Kind != native.ErrorStreamDisconnect && nerr.Kind != native.ErrorTimeout {
		t.Fatalf("expected crash/stream-disconnect/timeout error, got %q (%v)", nerr.Kind, err)
	}
}

func TestMapRPCErrorClassifiesRateLimitAndAuth(t *testing.T) {
	rate := mapRPCError("turn/start", &rpcError{Code: -32000, Message: "usage limit exceeded"})
	rateErr, ok := native.AsError(rate)
	if !ok || rateErr.Kind != native.ErrorRateLimit {
		t.Fatalf("expected rate limit error, got %v", rate)
	}
	auth := mapRPCError("turn/start", &rpcError{Code: -32000, Message: "unauthorized"})
	authErr, ok := native.AsError(auth)
	if !ok || authErr.Kind != native.ErrorAuth {
		t.Fatalf("expected auth error, got %v", auth)
	}
}

func TestDecodeEventMapsIDs(t *testing.T) {
	raw := json.RawMessage(`{"threadId":"thr_1","turnId":"trn_1","itemId":"itm_1"}`)
	ev := decodeEvent("turn/completed", raw)
	if ev.Name != "codex/event/turn_completed" {
		t.Fatalf("unexpected event name: %q", ev.Name)
	}
	if ev.ThreadID != "thr_1" || ev.TurnID != "trn_1" || ev.ItemID != "itm_1" {
		t.Fatalf("unexpected ids: %+v", ev)
	}
}

func TestCodexAppServerHelperProcess(t *testing.T) {
	if os.Getenv("ZCL_HELPER_PROCESS") != "1" {
		return
	}
	mode := strings.TrimSpace(os.Getenv("ZCL_HELPER_MODE"))
	if mode == "" {
		mode = "smoke"
	}
	runHelper(mode)
	os.Exit(0)
}

func runHelper(mode string) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var mu sync.Mutex
	writeJSON := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		_, _ = os.Stdout.Write(append(b, '\n'))
	}

	threadID := "thr_1"
	turnID := "turn_1"

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		method, _ := msg["method"].(string)
		id, hasID := msg["id"]

		if !hasID {
			if method == "initialized" && mode == "disconnect_after_initialized" {
				return
			}
			continue
		}

		respond := func(result any) {
			writeJSON(map[string]any{"id": id, "result": result})
		}
		respondErr := func(code int, message string) {
			writeJSON(map[string]any{"id": id, "error": map[string]any{"code": code, "message": message}})
		}

		switch method {
		case "initialize":
			respond(map[string]any{"userAgent": "codex-cli/1.4.0"})
		case "model/list":
			switch mode {
			case "compat_missing_method":
				respondErr(-32601, "method not found")
			case "timeout_on_model_list":
				time.Sleep(2 * time.Second)
			default:
				respond(map[string]any{"data": []any{}})
			}
		case "thread/start":
			respond(map[string]any{"thread": map[string]any{"id": threadID}})
			writeJSON(map[string]any{"method": "thread/started", "params": map[string]any{"threadId": threadID}})
		case "thread/resume":
			respond(map[string]any{"thread": map[string]any{"id": threadID}})
		case "turn/start":
			respond(map[string]any{"turn": map[string]any{"id": turnID, "status": "inProgress", "items": []any{}}})
			writeJSON(map[string]any{"method": "turn/started", "params": map[string]any{"threadId": threadID, "turnId": turnID}})
			writeJSON(map[string]any{"method": "item/started", "params": map[string]any{"threadId": threadID, "turnId": turnID, "itemId": "itm_1"}})
			writeJSON(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": threadID, "turnId": turnID, "itemId": "itm_1"}})
			writeJSON(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": threadID, "turnId": turnID}})
		case "turn/steer":
			respond(map[string]any{"turnId": turnID})
		case "turn/interrupt":
			respond(map[string]any{})
		default:
			respondErr(-32601, fmt.Sprintf("method not found: %s", method))
		}
	}
}
