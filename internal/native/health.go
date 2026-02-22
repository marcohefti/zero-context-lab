package native

import (
	"sort"
	"strings"
	"sync"
)

type HealthMetric string

const (
	HealthSessionStart     HealthMetric = "session_start"
	HealthSessionStartFail HealthMetric = "session_start_fail"
	HealthSessionClosed    HealthMetric = "session_closed"
	HealthRequestSent      HealthMetric = "request_sent"
	HealthRequestFail      HealthMetric = "request_fail"
	HealthStreamDisconnect HealthMetric = "stream_disconnect"
	HealthRuntimeCrash     HealthMetric = "runtime_crash"
	HealthRateLimited      HealthMetric = "rate_limited"
	HealthAuthFail         HealthMetric = "auth_fail"
	HealthListenerFailure  HealthMetric = "listener_failure"
	HealthInterrupted      HealthMetric = "interrupted"
	HealthSchedulerWait    HealthMetric = "scheduler_wait"
)

type HealthStrategySnapshot struct {
	Strategy StrategyID       `json:"strategy"`
	Metrics  map[string]int64 `json:"metrics"`
}

var (
	healthMu    sync.RWMutex
	healthStore = map[StrategyID]map[HealthMetric]int64{}
)

func RecordHealth(strategy StrategyID, metric HealthMetric) {
	strategy = StrategyID(strings.TrimSpace(strings.ToLower(string(strategy))))
	metric = HealthMetric(strings.TrimSpace(strings.ToLower(string(metric))))
	if strategy == "" || metric == "" {
		return
	}
	healthMu.Lock()
	defer healthMu.Unlock()
	row, ok := healthStore[strategy]
	if !ok {
		row = map[HealthMetric]int64{}
		healthStore[strategy] = row
	}
	row[metric]++
}

func HealthSnapshot() []HealthStrategySnapshot {
	healthMu.RLock()
	defer healthMu.RUnlock()
	if len(healthStore) == 0 {
		return nil
	}
	ids := make([]StrategyID, 0, len(healthStore))
	for id := range healthStore {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]HealthStrategySnapshot, 0, len(ids))
	for _, id := range ids {
		row := healthStore[id]
		metrics := make(map[string]int64, len(row))
		for m, c := range row {
			metrics[string(m)] = c
		}
		out = append(out, HealthStrategySnapshot{Strategy: id, Metrics: metrics})
	}
	return out
}

func resetHealthForTests() {
	healthMu.Lock()
	defer healthMu.Unlock()
	healthStore = map[StrategyID]map[HealthMetric]int64{}
}

func CanonicalHealthMetrics() []string {
	return []string{
		string(HealthSessionStart),
		string(HealthSessionStartFail),
		string(HealthSessionClosed),
		string(HealthRequestSent),
		string(HealthRequestFail),
		string(HealthStreamDisconnect),
		string(HealthRuntimeCrash),
		string(HealthRateLimited),
		string(HealthAuthFail),
		string(HealthListenerFailure),
		string(HealthInterrupted),
		string(HealthSchedulerWait),
	}
}
