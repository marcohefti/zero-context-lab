package native

import "testing"

func TestHealthRecordAndSnapshot(t *testing.T) {
	resetHealthForTests()
	defer resetHealthForTests()

	RecordHealth(StrategyCodexAppServer, HealthSessionStart)
	RecordHealth(StrategyCodexAppServer, HealthSessionStart)
	RecordHealth(StrategyCodexAppServer, HealthRateLimited)
	RecordHealth(StrategyID("provider_stub"), HealthSessionStartFail)

	snap := HealthSnapshot()
	if len(snap) != 2 {
		t.Fatalf("expected two strategy snapshots, got %d", len(snap))
	}
	if snap[0].Strategy != StrategyCodexAppServer {
		t.Fatalf("expected codex strategy first, got %q", snap[0].Strategy)
	}
	if snap[0].Metrics[string(HealthSessionStart)] != 2 {
		t.Fatalf("unexpected session_start count: %+v", snap[0].Metrics)
	}
	if snap[0].Metrics[string(HealthRateLimited)] != 1 {
		t.Fatalf("unexpected rate_limited count: %+v", snap[0].Metrics)
	}
	if snap[1].Strategy != StrategyID("provider_stub") {
		t.Fatalf("unexpected second strategy: %q", snap[1].Strategy)
	}
}

func TestHealthRecordIgnoresEmptyValues(t *testing.T) {
	resetHealthForTests()
	defer resetHealthForTests()

	RecordHealth("", HealthSessionStart)
	RecordHealth(StrategyCodexAppServer, "")
	if snap := HealthSnapshot(); len(snap) != 0 {
		t.Fatalf("expected empty snapshot, got %+v", snap)
	}
}
