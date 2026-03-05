package campaign

import (
	"bufio"
	"encoding/json"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/kernel/codes"
	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
)

const (
	ReasonTraceProfileRequiredFamily = codes.CampaignTraceProfileRequiredEventFamily
	ReasonTraceProfileMCPRequired    = codes.CampaignTraceProfileMCPRequired
	ReasonTraceProfileBootstrapOnly  = codes.CampaignTraceProfileBootstrapOnly
)

func EvaluateTraceProfile(profile string, attemptDir string) ([]string, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" || profile == TraceProfileNone {
		return nil, nil
	}
	f, err := openTraceProfileFile(attemptDir)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, nil
	}
	defer func() { _ = f.Close() }()

	stats, err := scanTraceProfileStats(f)
	if err != nil {
		return nil, err
	}
	return profileReasons(profile, stats), nil
}

type traceProfileStats struct {
	total         int
	actionable    int
	hasMCPCall    bool
	bootstrapOnly bool
}

func openTraceProfileFile(attemptDir string) (*os.File, error) {
	path := filepath.Join(strings.TrimSpace(attemptDir), artifacts.ToolCallsJSONL)
	f, err := os.Open(path)
	if err != nil && os.IsNotExist(err) {
		return nil, nil
	}
	return f, err
}

func scanTraceProfileStats(f *os.File) (traceProfileStats, error) {
	stats := traceProfileStats{bootstrapOnly: true}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		ev, skip, err := parseTraceProfileEvent(sc.Text())
		if err != nil {
			return traceProfileStats{}, err
		}
		if skip {
			continue
		}
		updateTraceProfileStats(&stats, ev)
	}
	if err := sc.Err(); err != nil {
		return traceProfileStats{}, err
	}
	return stats, nil
}

func parseTraceProfileEvent(line string) (schema.TraceEventV1, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return schema.TraceEventV1{}, true, nil
	}
	var ev schema.TraceEventV1
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return schema.TraceEventV1{}, false, err
	}
	return ev, false, nil
}

func updateTraceProfileStats(stats *traceProfileStats, ev schema.TraceEventV1) {
	stats.total++
	op := strings.TrimSpace(ev.Op)
	tool := strings.TrimSpace(ev.Tool)
	if op == "exec" || op == "tools/call" {
		stats.actionable++
	}
	if tool == "mcp" && op == "tools/call" {
		stats.hasMCPCall = true
	}
	if !isBootstrapTraceOp(op) {
		stats.bootstrapOnly = false
	}
}

func profileReasons(profile string, stats traceProfileStats) []string {
	var reasons []string
	switch profile {
	case TraceProfileMCPRequired:
		if !stats.hasMCPCall {
			reasons = append(reasons, ReasonTraceProfileMCPRequired)
		}
	case TraceProfileStrictBrowserComp:
		if stats.total == 0 || stats.actionable == 0 {
			reasons = append(reasons, ReasonTraceProfileRequiredFamily)
		}
		if stats.total > 0 && stats.bootstrapOnly {
			reasons = append(reasons, ReasonTraceProfileBootstrapOnly)
		}
	}
	return reasons
}

func isBootstrapTraceOp(op string) bool {
	switch strings.TrimSpace(op) {
	case "initialize", "tools/list", "new_page", "take_snapshot", "list_pages":
		return true
	default:
		return false
	}
}
