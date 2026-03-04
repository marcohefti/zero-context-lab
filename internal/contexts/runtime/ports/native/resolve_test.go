package native

import (
	"context"
	"errors"
	"testing"
)

type fakeRuntime struct {
	id       StrategyID
	caps     Capabilities
	probeErr error
}

func (f fakeRuntime) ID() StrategyID              { return f.id }
func (f fakeRuntime) Capabilities() Capabilities  { return f.caps }
func (f fakeRuntime) Probe(context.Context) error { return f.probeErr }
func (f fakeRuntime) StartSession(context.Context, SessionOptions) (Session, error) {
	return nil, errors.New("not implemented")
}

func TestResolve_SelectsFirstAvailableStrategy(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(fakeRuntime{id: "alpha", caps: Capabilities{SupportsEventStream: true}})
	reg.MustRegister(fakeRuntime{id: "beta", caps: Capabilities{SupportsEventStream: true}})

	res, err := Resolve(context.Background(), reg, ResolveInput{
		StrategyChain:        []StrategyID{"alpha", "beta"},
		RequiredCapabilities: []Capability{CapabilityEventStream},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Selected != "alpha" {
		t.Fatalf("expected alpha, got %q", res.Selected)
	}
	if len(res.Chain) != 2 || res.Chain[0] != "alpha" || res.Chain[1] != "beta" {
		t.Fatalf("unexpected chain: %#v", res.Chain)
	}
}

func TestResolve_UnsupportedStrategyIsTyped(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(fakeRuntime{id: "alpha", caps: Capabilities{}})

	_, err := Resolve(context.Background(), reg, ResolveInput{StrategyChain: []StrategyID{"missing"}})
	if err == nil {
		t.Fatalf("expected error")
	}
	nerr, ok := AsError(err)
	if !ok {
		t.Fatalf("expected native error, got %T", err)
	}
	if nerr.Kind != ErrorUnsupportedStrategy {
		t.Fatalf("expected kind %q, got %q", ErrorUnsupportedStrategy, nerr.Kind)
	}
	if nerr.Code != ErrorCodeForKind(ErrorUnsupportedStrategy) {
		t.Fatalf("unexpected code: %q", nerr.Code)
	}
}

func TestResolve_FallbackByCapability(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(fakeRuntime{id: "alpha", caps: Capabilities{SupportsThreadStart: true}})
	reg.MustRegister(fakeRuntime{id: "beta", caps: Capabilities{SupportsThreadStart: true, SupportsEventStream: true}})

	res, err := Resolve(context.Background(), reg, ResolveInput{
		StrategyChain:        []StrategyID{"alpha", "beta"},
		RequiredCapabilities: []Capability{CapabilityThreadStart, CapabilityEventStream},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Selected != "beta" {
		t.Fatalf("expected beta, got %q", res.Selected)
	}
}

func TestResolve_AllUnavailableIncludesFailures(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(fakeRuntime{id: "alpha", caps: Capabilities{SupportsThreadStart: true}, probeErr: errors.New("binary missing")})
	reg.MustRegister(fakeRuntime{id: "beta", caps: Capabilities{}})

	_, err := Resolve(context.Background(), reg, ResolveInput{
		StrategyChain:        []StrategyID{"alpha", "beta"},
		RequiredCapabilities: []Capability{CapabilityThreadStart},
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	nerr, ok := AsError(err)
	if !ok {
		t.Fatalf("expected native error, got %T", err)
	}
	if nerr.Kind != ErrorStrategyUnavailable {
		t.Fatalf("expected %q, got %q", ErrorStrategyUnavailable, nerr.Kind)
	}
	if len(nerr.Failures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(nerr.Failures))
	}
}

func TestNormalizeStrategyChain_DedupesAndNormalizes(t *testing.T) {
	got := NormalizeStrategyChain([]string{" codex_app_server ", "", "CODEX_APP_SERVER", "codex_app_server"})
	if len(got) != 1 || got[0] != StrategyCodexAppServer {
		t.Fatalf("unexpected chain: %#v", got)
	}
}
