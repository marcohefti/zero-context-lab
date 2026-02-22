package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/native"
)

type fakeRuntime struct {
	id       native.StrategyID
	caps     native.Capabilities
	probeErr error
	session  native.Session
}

func (f fakeRuntime) ID() native.StrategyID             { return f.id }
func (f fakeRuntime) Capabilities() native.Capabilities { return f.caps }
func (f fakeRuntime) Probe(context.Context) error       { return f.probeErr }
func (f fakeRuntime) StartSession(context.Context, native.SessionOptions) (native.Session, error) {
	if f.session == nil {
		return nil, errors.New("missing session")
	}
	return f.session, nil
}

type fakeSession struct {
	threadID string
	turnID   string
}

func (s *fakeSession) RuntimeID() native.StrategyID { return "fake" }
func (s *fakeSession) SessionID() string            { return "sess" }
func (s *fakeSession) ThreadID() string             { return s.threadID }
func (s *fakeSession) StartThread(context.Context, native.ThreadStartRequest) (native.ThreadHandle, error) {
	if s.threadID == "" {
		s.threadID = "thr"
	}
	return native.ThreadHandle{ThreadID: s.threadID}, nil
}
func (s *fakeSession) ResumeThread(context.Context, native.ThreadResumeRequest) (native.ThreadHandle, error) {
	return native.ThreadHandle{ThreadID: s.threadID}, nil
}
func (s *fakeSession) StartTurn(context.Context, native.TurnStartRequest) (native.TurnHandle, error) {
	if s.turnID == "" {
		s.turnID = "turn"
	}
	return native.TurnHandle{TurnID: s.turnID}, nil
}
func (s *fakeSession) SteerTurn(context.Context, native.TurnSteerRequest) (native.TurnHandle, error) {
	return native.TurnHandle{TurnID: s.turnID}, nil
}
func (s *fakeSession) InterruptTurn(context.Context, native.TurnInterruptRequest) error { return nil }
func (s *fakeSession) AddListener(listener native.EventListener) (string, error) {
	go func() {
		time.Sleep(20 * time.Millisecond)
		listener(native.Event{Name: "codex/event/turn_completed"})
	}()
	return "1", nil
}
func (s *fakeSession) RemoveListener(string) error { return nil }
func (s *fakeSession) Close(context.Context) error { return nil }

func TestRunBasicSuccess(t *testing.T) {
	rt := fakeRuntime{
		id: "fake",
		caps: native.Capabilities{
			SupportsThreadStart: true,
			SupportsInterrupt:   true,
			SupportsEventStream: true,
		},
		session: &fakeSession{},
	}
	if err := RunBasic(context.Background(), rt, BasicSuiteOptions{}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

func TestRunBasicCapabilityFailure(t *testing.T) {
	rt := fakeRuntime{
		id:      "fake",
		caps:    native.Capabilities{},
		session: &fakeSession{},
	}
	err := RunBasic(context.Background(), rt, BasicSuiteOptions{})
	if err == nil {
		t.Fatalf("expected capability failure")
	}
}
