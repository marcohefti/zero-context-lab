package providerstub

import (
	"context"

	"github.com/marcohefti/zero-context-lab/internal/native"
)

// Runtime is a compile-time skeleton adapter used to validate onboarding ergonomics.
// It intentionally reports no supported control-plane capabilities.
type Runtime struct{}

func NewRuntime() *Runtime {
	return &Runtime{}
}

func (r *Runtime) ID() native.StrategyID {
	return native.StrategyProviderStub
}

func (r *Runtime) Capabilities() native.Capabilities {
	return native.Capabilities{}
}

func (r *Runtime) Probe(_ context.Context) error {
	return native.NewError(native.ErrorCapabilityUnsupported, "provider_stub has no equivalent control-plane APIs yet")
}

func (r *Runtime) StartSession(_ context.Context, _ native.SessionOptions) (native.Session, error) {
	return nil, native.NewError(native.ErrorCapabilityUnsupported, "provider_stub cannot start native sessions")
}
