package campaign

import (
	"bufio"
	"encoding/json"
	"github.com/marcohefti/zero-context-lab/internal/kernel/artifacts"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
)

func EvaluateToolPolicy(policy ToolPolicySpec, attemptDir string) ([]string, error) {
	if !hasToolPolicyRules(policy) {
		return nil, nil
	}
	f, err := openToolPolicyTrace(attemptDir)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, nil
	}
	defer func() { _ = f.Close() }()
	return scanToolPolicyTrace(policy, f)
}

func hasToolPolicyRules(policy ToolPolicySpec) bool {
	return len(policy.Allow) > 0 || len(policy.Deny) > 0
}

func scanToolPolicyTrace(policy ToolPolicySpec, f *os.File) ([]string, error) {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		violation, err := evaluateToolPolicyTraceLine(policy, sc.Text())
		if err != nil {
			return nil, err
		}
		if violation {
			return []string{ReasonToolPolicy}, nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

func evaluateToolPolicyTraceLine(policy ToolPolicySpec, line string) (bool, error) {
	ev, skip, err := parseToolPolicyTraceLine(line)
	if err != nil || skip {
		return false, err
	}
	namespace, target, actionable := toolPolicyMatchContext(ev)
	if !actionable {
		return false, nil
	}
	if !toolPolicyAllowed(policy.Allow, namespace, target, policy.Aliases) {
		return true, nil
	}
	return toolPolicyDenied(policy.Deny, namespace, target, policy.Aliases), nil
}

func openToolPolicyTrace(attemptDir string) (*os.File, error) {
	path := filepath.Join(strings.TrimSpace(attemptDir), artifacts.ToolCallsJSONL)
	f, err := os.Open(path)
	if err != nil && os.IsNotExist(err) {
		return nil, nil
	}
	return f, err
}

func parseToolPolicyTraceLine(line string) (schema.TraceEventV1, bool, error) {
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

func toolPolicyMatchContext(ev schema.TraceEventV1) (namespace, target string, actionable bool) {
	namespace = strings.ToLower(strings.TrimSpace(ev.Tool))
	op := strings.ToLower(strings.TrimSpace(ev.Op))
	if !isToolPolicyActionableOp(op) || namespace == "" {
		return "", "", false
	}
	return namespace, strings.ToLower(strings.TrimSpace(toolPolicyTarget(ev))), true
}

func toolPolicyAllowed(allow []ToolPolicyRuleSpec, namespace, target string, aliases map[string][]string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, rule := range allow {
		if toolPolicyRuleMatches(rule, namespace, target, aliases) {
			return true
		}
	}
	return false
}

func toolPolicyDenied(deny []ToolPolicyRuleSpec, namespace, target string, aliases map[string][]string) bool {
	for _, rule := range deny {
		if toolPolicyRuleMatches(rule, namespace, target, aliases) {
			return true
		}
	}
	return false
}

func isToolPolicyActionableOp(op string) bool {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "exec", "tools/call", "request":
		return true
	default:
		return false
	}
}

func toolPolicyRuleMatches(rule ToolPolicyRuleSpec, namespace string, target string, aliases map[string][]string) bool {
	if ns := strings.ToLower(strings.TrimSpace(rule.Namespace)); ns != "" && ns != namespace {
		return false
	}
	prefix := strings.ToLower(strings.TrimSpace(rule.Prefix))
	if prefix == "" {
		return true
	}
	candidates := []string{prefix}
	if alias := aliases[prefix]; len(alias) > 0 {
		candidates = append(candidates, alias...)
	}
	for _, cand := range candidates {
		if cand != "" && strings.HasPrefix(target, cand) {
			return true
		}
	}
	return false
}

func toolPolicyTarget(ev schema.TraceEventV1) string {
	tool := strings.ToLower(strings.TrimSpace(ev.Tool))
	op := strings.ToLower(strings.TrimSpace(ev.Op))
	if len(ev.Input) == 0 {
		return ""
	}
	switch {
	case tool == "cli" && op == "exec":
		var in struct {
			Argv []string `json:"argv"`
		}
		if err := json.Unmarshal(ev.Input, &in); err == nil && len(in.Argv) > 0 {
			return strings.TrimSpace(in.Argv[0])
		}
	case tool == "mcp" && op == "tools/call":
		var in struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.Unmarshal(ev.Input, &in); err == nil {
			if name := strings.TrimSpace(in.Params.Name); name != "" {
				return name
			}
		}
	case tool == "http" && op == "request":
		var in struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(ev.Input, &in); err == nil {
			return strings.TrimSpace(in.URL)
		}
	}
	return ""
}
