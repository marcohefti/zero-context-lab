package campaign

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

func EvaluateToolPolicy(policy ToolPolicySpec, attemptDir string) ([]string, error) {
	if len(policy.Allow) == 0 && len(policy.Deny) == 0 {
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
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, err
		}
		namespace := strings.ToLower(strings.TrimSpace(ev.Tool))
		op := strings.ToLower(strings.TrimSpace(ev.Op))
		if !isToolPolicyActionableOp(op) || namespace == "" {
			continue
		}
		target := strings.ToLower(strings.TrimSpace(toolPolicyTarget(ev)))
		if len(policy.Allow) > 0 {
			allowed := false
			for _, rule := range policy.Allow {
				if toolPolicyRuleMatches(rule, namespace, target, policy.Aliases) {
					allowed = true
					break
				}
			}
			if !allowed {
				return []string{ReasonToolPolicy}, nil
			}
		}
		for _, rule := range policy.Deny {
			if toolPolicyRuleMatches(rule, namespace, target, policy.Aliases) {
				return []string{ReasonToolPolicy}, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, nil
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
