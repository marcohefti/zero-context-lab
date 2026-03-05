package native

import (
	"context"
	"fmt"
	"strings"
)

type ResolveInput struct {
	StrategyChain        []StrategyID
	RequiredCapabilities []Capability
}

type ResolveResult struct {
	Selected     StrategyID   `json:"selected"`
	Chain        []StrategyID `json:"chain"`
	Runtime      Runtime      `json:"-"`
	Capabilities Capabilities `json:"capabilities"`
}

func Resolve(ctx context.Context, reg *Registry, in ResolveInput) (ResolveResult, error) {
	chain, required, err := resolveInputs(reg, in)
	if err != nil {
		return ResolveResult{}, err
	}

	failures := make([]StrategyFailure, 0, len(chain))
	for _, sid := range chain {
		result, failure, done, err := resolveStrategy(ctx, reg, sid, chain, required)
		if err != nil {
			return ResolveResult{}, err
		}
		if done {
			return result, nil
		}
		failures = append(failures, failure)
	}
	return ResolveResult{}, noStrategyAvailableError(failures)
}

func resolveInputs(reg *Registry, in ResolveInput) ([]StrategyID, []Capability, error) {
	if reg == nil {
		return nil, nil, WrapError(ErrorUnsupportedStrategy, "runtime registry is not configured", nil)
	}
	chain := normalizeStrategyIDs(in.StrategyChain)
	if len(chain) == 0 {
		return nil, nil, WrapError(ErrorUnsupportedStrategy, "runtime strategy chain is empty", nil)
	}
	return chain, normalizeCapabilities(in.RequiredCapabilities), nil
}

func resolveStrategy(ctx context.Context, reg *Registry, sid StrategyID, chain []StrategyID, required []Capability) (ResolveResult, StrategyFailure, bool, error) {
	rt, ok := reg.Get(sid)
	if !ok {
		return ResolveResult{}, StrategyFailure{}, false, unsupportedStrategyError(sid)
	}
	caps := rt.Capabilities()
	if missing := missingCapabilities(caps, required); len(missing) > 0 {
		return ResolveResult{}, capabilityFailure(sid, missing), false, nil
	}
	if err := rt.Probe(ctx); err != nil {
		return ResolveResult{}, probeFailure(sid, err), false, nil
	}
	return ResolveResult{
		Selected:     sid,
		Chain:        append([]StrategyID(nil), chain...),
		Runtime:      rt,
		Capabilities: caps,
	}, StrategyFailure{}, true, nil
}

func unsupportedStrategyError(sid StrategyID) error {
	return &Error{
		Code:     ErrorCodeForKind(ErrorUnsupportedStrategy),
		Kind:     ErrorUnsupportedStrategy,
		Strategy: sid,
		Message:  fmt.Sprintf("unsupported runtime strategy %q", sid),
	}
}

func capabilityFailure(sid StrategyID, missing []string) StrategyFailure {
	return StrategyFailure{
		Strategy: sid,
		Code:     ErrorCodeForKind(ErrorCapabilityUnsupported),
		Message:  "strategy missing required capabilities: " + strings.Join(missing, ","),
	}
}

func probeFailure(sid StrategyID, err error) StrategyFailure {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "probe failed"
	}
	code := ErrorCodeForKind(ErrorStrategyUnavailable)
	if nerr, ok := AsError(err); ok && strings.TrimSpace(nerr.Code) != "" {
		code = nerr.Code
	}
	return StrategyFailure{
		Strategy: sid,
		Code:     code,
		Message:  msg,
	}
}

func noStrategyAvailableError(failures []StrategyFailure) error {
	msg := "no runtime strategy available"
	if len(failures) > 0 {
		msg += "; see failures"
	}
	return &Error{
		Code:     ErrorCodeForKind(ErrorStrategyUnavailable),
		Kind:     ErrorStrategyUnavailable,
		Message:  msg,
		Failures: failures,
	}
}

func normalizeStrategyIDs(in []StrategyID) []StrategyID {
	if len(in) == 0 {
		return nil
	}
	seen := map[StrategyID]bool{}
	out := make([]StrategyID, 0, len(in))
	for _, sid := range in {
		raw := strings.ToLower(strings.TrimSpace(string(sid)))
		if raw == "" {
			continue
		}
		norm := StrategyID(raw)
		if seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeCapabilities(in []Capability) []Capability {
	if len(in) == 0 {
		return nil
	}
	seen := map[Capability]bool{}
	out := make([]Capability, 0, len(in))
	for _, cap := range in {
		cap = Capability(strings.TrimSpace(string(cap)))
		if cap == "" || seen[cap] {
			continue
		}
		seen[cap] = true
		out = append(out, cap)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func missingCapabilities(caps Capabilities, required []Capability) []string {
	if len(required) == 0 {
		return nil
	}
	out := make([]string, 0, len(required))
	for _, req := range required {
		if !caps.Has(req) {
			out = append(out, string(req))
		}
	}
	return out
}
