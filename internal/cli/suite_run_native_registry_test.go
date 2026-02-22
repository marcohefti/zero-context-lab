package cli

import (
	"context"
	"testing"

	"github.com/marcohefti/zero-context-lab/internal/native"
)

func TestBuildNativeRuntimeRegistry_KnownStrategies(t *testing.T) {
	reg := buildNativeRuntimeRegistry()
	ids := reg.IDs()
	if len(ids) < 2 {
		t.Fatalf("expected at least two runtime strategies, got %#v", ids)
	}
	foundCodex := false
	foundStub := false
	for _, id := range ids {
		if id == native.StrategyCodexAppServer {
			foundCodex = true
		}
		if id == native.StrategyProviderStub {
			foundStub = true
		}
	}
	if !foundCodex || !foundStub {
		t.Fatalf("expected codex + provider_stub in registry, got %#v", ids)
	}
}

func TestProviderStubResolve_IsCapabilityUnsupported(t *testing.T) {
	reg := buildNativeRuntimeRegistry()
	_, err := native.Resolve(context.Background(), reg, native.ResolveInput{
		StrategyChain: []native.StrategyID{native.StrategyProviderStub},
		RequiredCapabilities: []native.Capability{
			native.CapabilityThreadStart,
			native.CapabilityEventStream,
		},
	})
	if err == nil {
		t.Fatalf("expected resolve failure for provider_stub")
	}
	nerr, ok := native.AsError(err)
	if !ok {
		t.Fatalf("expected native error, got %T", err)
	}
	if nerr.Kind != native.ErrorStrategyUnavailable {
		t.Fatalf("unexpected kind: %q", nerr.Kind)
	}
	if len(nerr.Failures) == 0 || nerr.Failures[0].Code != native.ErrorCodeForKind(native.ErrorCapabilityUnsupported) {
		t.Fatalf("expected capability unsupported failure, got %+v", nerr.Failures)
	}
}
