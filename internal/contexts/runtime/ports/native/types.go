package native

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type StrategyID string

const (
	StrategyCodexAppServer StrategyID = "codex_app_server"
	StrategyProviderStub   StrategyID = "provider_stub"
)

type Capability string

const (
	CapabilityThreadStart      Capability = "supports_thread_start"
	CapabilityTurnSteer        Capability = "supports_turn_steer"
	CapabilityInterrupt        Capability = "supports_interrupt"
	CapabilityEventStream      Capability = "supports_event_stream"
	CapabilityParallelSessions Capability = "supports_parallel_sessions"
)

type Capabilities struct {
	SupportsThreadStart      bool `json:"supports_thread_start"`
	SupportsTurnSteer        bool `json:"supports_turn_steer"`
	SupportsInterrupt        bool `json:"supports_interrupt"`
	SupportsEventStream      bool `json:"supports_event_stream"`
	SupportsParallelSessions bool `json:"supports_parallel_sessions"`
}

func (c Capabilities) Has(cap Capability) bool {
	switch cap {
	case CapabilityThreadStart:
		return c.SupportsThreadStart
	case CapabilityTurnSteer:
		return c.SupportsTurnSteer
	case CapabilityInterrupt:
		return c.SupportsInterrupt
	case CapabilityEventStream:
		return c.SupportsEventStream
	case CapabilityParallelSessions:
		return c.SupportsParallelSessions
	default:
		return false
	}
}

type Runtime interface {
	ID() StrategyID
	Capabilities() Capabilities
	Probe(ctx context.Context) error
	StartSession(ctx context.Context, opts SessionOptions) (Session, error)
}

type SessionOptions struct {
	RunID      string
	SuiteID    string
	MissionID  string
	AttemptID  string
	AttemptDir string
	Env        map[string]string
}

type Session interface {
	RuntimeID() StrategyID
	SessionID() string
	ThreadID() string

	StartThread(ctx context.Context, req ThreadStartRequest) (ThreadHandle, error)
	ResumeThread(ctx context.Context, req ThreadResumeRequest) (ThreadHandle, error)
	StartTurn(ctx context.Context, req TurnStartRequest) (TurnHandle, error)
	SteerTurn(ctx context.Context, req TurnSteerRequest) (TurnHandle, error)
	InterruptTurn(ctx context.Context, req TurnInterruptRequest) error

	AddListener(listener EventListener) (listenerID string, err error)
	RemoveListener(listenerID string) error

	Close(ctx context.Context) error
}

type ThreadHandle struct {
	ThreadID string `json:"threadId"`
}

type TurnHandle struct {
	TurnID   string `json:"turnId"`
	Status   string `json:"status,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
}

type ThreadStartRequest struct {
	Model                string `json:"model,omitempty"`
	ModelReasoningEffort string `json:"modelReasoningEffort,omitempty"`
	ModelReasoningPolicy string `json:"modelReasoningPolicy,omitempty"`
	Cwd                  string `json:"cwd,omitempty"`
	ApprovalPolicy       string `json:"approvalPolicy,omitempty"`
	Sandbox              string `json:"sandbox,omitempty"`
	Personality          string `json:"personality,omitempty"`
}

type ThreadResumeRequest struct {
	ThreadID string `json:"threadId"`
}

type InputItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
	Path     string `json:"path,omitempty"`
	Name     string `json:"name,omitempty"`
}

type TurnStartRequest struct {
	ThreadID string      `json:"threadId"`
	Input    []InputItem `json:"input"`
}

type TurnSteerRequest struct {
	ThreadID string      `json:"threadId"`
	TurnID   string      `json:"turnId"`
	Input    []InputItem `json:"input"`
}

type TurnInterruptRequest struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type Event struct {
	Name       string          `json:"name"`
	ThreadID   string          `json:"threadId,omitempty"`
	TurnID     string          `json:"turnId,omitempty"`
	ItemID     string          `json:"itemId,omitempty"`
	CallID     string          `json:"callId,omitempty"`
	ReceivedAt time.Time       `json:"receivedAt"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type EventListener func(Event)

func NormalizeStrategyChain(in []string) []StrategyID {
	if len(in) == 0 {
		return nil
	}
	seen := map[StrategyID]bool{}
	out := make([]StrategyID, 0, len(in))
	for _, raw := range in {
		raw = strings.ToLower(strings.TrimSpace(raw))
		if raw == "" {
			continue
		}
		id := StrategyID(raw)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func CanonicalCapabilities() []Capability {
	return []Capability{
		CapabilityThreadStart,
		CapabilityTurnSteer,
		CapabilityInterrupt,
		CapabilityEventStream,
		CapabilityParallelSessions,
	}
}
