package providerstub

import (
	"context"
	"testing"

	"github.com/marcohefti/zero-context-lab/internal/native"
)

func TestProviderStubProbeIsCapabilityUnsupported(t *testing.T) {
	rt := NewRuntime()
	err := rt.Probe(context.Background())
	if err == nil {
		t.Fatalf("expected probe error")
	}
	nerr, ok := native.AsError(err)
	if !ok {
		t.Fatalf("expected native error, got %T", err)
	}
	if nerr.Kind != native.ErrorCapabilityUnsupported {
		t.Fatalf("unexpected kind: %q", nerr.Kind)
	}
}
