package campaign

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/codes"
	"github.com/marcohefti/zero-context-lab/internal/schema"
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
	path := filepath.Join(strings.TrimSpace(attemptDir), "tool.calls.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	total := 0
	actionable := 0
	hasMCPCall := false
	bootstrapOnly := true

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, err
		}
		total++
		op := strings.TrimSpace(ev.Op)
		tool := strings.TrimSpace(ev.Tool)
		if op == "exec" || op == "tools/call" {
			actionable++
		}
		if tool == "mcp" && op == "tools/call" {
			hasMCPCall = true
		}
		if !isBootstrapTraceOp(op) {
			bootstrapOnly = false
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	var reasons []string
	switch profile {
	case TraceProfileMCPRequired:
		if !hasMCPCall {
			reasons = append(reasons, ReasonTraceProfileMCPRequired)
		}
	case TraceProfileStrictBrowserComp:
		if total == 0 || actionable == 0 {
			reasons = append(reasons, ReasonTraceProfileRequiredFamily)
		}
		if total > 0 && bootstrapOnly {
			reasons = append(reasons, ReasonTraceProfileBootstrapOnly)
		}
	}
	return reasons, nil
}

func isBootstrapTraceOp(op string) bool {
	switch strings.TrimSpace(op) {
	case "initialize", "tools/list", "new_page", "take_snapshot", "list_pages":
		return true
	default:
		return false
	}
}
