package conformance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/contexts/runtime/ports/native"
)

type BasicSuiteOptions struct {
	RequiredCapabilities []native.Capability
	SessionOptions       native.SessionOptions
	TurnPrompt           string
	EventTimeout         time.Duration
}

func RunBasic(ctx context.Context, rt native.Runtime, opts BasicSuiteOptions) error {
	opts, err := normalizeBasicSuiteOptions(rt, opts)
	if err != nil {
		return err
	}
	if err := rt.Probe(ctx); err != nil {
		return fmt.Errorf("probe failed: %w", err)
	}
	if err := requireCapabilities(rt, opts.RequiredCapabilities); err != nil {
		return err
	}

	sess, err := rt.StartSession(ctx, opts.SessionOptions)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sess.Close(closeCtx)
	}()

	events := make(chan native.Event, 32)
	listenerID, err := sess.AddListener(func(ev native.Event) {
		select {
		case events <- ev:
		default:
		}
	})
	if err != nil {
		return fmt.Errorf("add listener: %w", err)
	}
	defer func() { _ = sess.RemoveListener(listenerID) }()

	thread, err := sess.StartThread(ctx, native.ThreadStartRequest{})
	if err != nil {
		return fmt.Errorf("start thread: %w", err)
	}
	if strings.TrimSpace(thread.ThreadID) == "" {
		return fmt.Errorf("start thread returned empty thread id")
	}
	turn, err := sess.StartTurn(ctx, native.TurnStartRequest{
		ThreadID: thread.ThreadID,
		Input:    []native.InputItem{{Type: "text", Text: opts.TurnPrompt}},
	})
	if err != nil {
		return fmt.Errorf("start turn: %w", err)
	}
	if strings.TrimSpace(turn.TurnID) == "" {
		return fmt.Errorf("start turn returned empty turn id")
	}
	return waitForTerminalEvent(ctx, events, opts.EventTimeout)
}

func normalizeBasicSuiteOptions(rt native.Runtime, opts BasicSuiteOptions) (BasicSuiteOptions, error) {
	if rt == nil {
		return BasicSuiteOptions{}, fmt.Errorf("runtime is nil")
	}
	if len(opts.RequiredCapabilities) == 0 {
		opts.RequiredCapabilities = []native.Capability{
			native.CapabilityThreadStart,
			native.CapabilityInterrupt,
			native.CapabilityEventStream,
		}
	}
	if opts.EventTimeout <= 0 {
		opts.EventTimeout = 3 * time.Second
	}
	if strings.TrimSpace(opts.TurnPrompt) == "" {
		opts.TurnPrompt = "conformance ping"
	}
	return opts, nil
}

func requireCapabilities(rt native.Runtime, required []native.Capability) error {
	caps := rt.Capabilities()
	for _, cap := range required {
		if caps.Has(cap) {
			continue
		}
		return fmt.Errorf("runtime %s missing capability %s", rt.ID(), cap)
	}
	return nil
}

func waitForTerminalEvent(ctx context.Context, events <-chan native.Event, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev := <-events:
			if ev.Name == "codex/event/turn_completed" || ev.Name == "codex/event/turn_failed" {
				return nil
			}
		case <-timer.C:
			return fmt.Errorf("timed out waiting for turn terminal event")
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for events: %w", ctx.Err())
		}
	}
}
