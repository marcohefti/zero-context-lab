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
	if reg == nil {
		return ResolveResult{}, WrapError(ErrorUnsupportedStrategy, "runtime registry is not configured", nil)
	}
	chain := normalizeStrategyIDs(in.StrategyChain)
	if len(chain) == 0 {
		return ResolveResult{}, WrapError(ErrorUnsupportedStrategy, "runtime strategy chain is empty", nil)
	}
	required := normalizeCapabilities(in.RequiredCapabilities)

	failures := make([]StrategyFailure, 0, len(chain))
	for _, sid := range chain {
		rt, ok := reg.Get(sid)
		if !ok {
			return ResolveResult{}, &Error{
				Code:     ErrorCodeForKind(ErrorUnsupportedStrategy),
				Kind:     ErrorUnsupportedStrategy,
				Strategy: sid,
				Message:  fmt.Sprintf("unsupported runtime strategy %q", sid),
			}
		}
		caps := rt.Capabilities()
		missing := missingCapabilities(caps, required)
		if len(missing) > 0 {
			failures = append(failures, StrategyFailure{
				Strategy: sid,
				Code:     ErrorCodeForKind(ErrorCapabilityUnsupported),
				Message:  "strategy missing required capabilities: " + strings.Join(missing, ","),
			})
			continue
		}
		if err := rt.Probe(ctx); err != nil {
			msg := strings.TrimSpace(err.Error())
			if msg == "" {
				msg = "probe failed"
			}
			code := ErrorCodeForKind(ErrorStrategyUnavailable)
			if nerr, ok := AsError(err); ok && strings.TrimSpace(nerr.Code) != "" {
				code = nerr.Code
			}
			failures = append(failures, StrategyFailure{
				Strategy: sid,
				Code:     code,
				Message:  msg,
			})
			continue
		}
		return ResolveResult{
			Selected:     sid,
			Chain:        append([]StrategyID(nil), chain...),
			Runtime:      rt,
			Capabilities: caps,
		}, nil
	}
	msg := "no runtime strategy available"
	if len(failures) > 0 {
		msg = msg + "; see failures"
	}
	return ResolveResult{}, &Error{
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
