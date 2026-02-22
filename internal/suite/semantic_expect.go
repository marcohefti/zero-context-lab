package suite

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/schema"
)

var defaultBoilerplateMCPTools = []string{
	"new_page",
	"take_snapshot",
	"list_pages",
}

// ValidateSemantic validates semantic mission rules against feedback.resultJson and trace facts.
func ValidateSemantic(sem *SemanticExpectsV1, fb schema.FeedbackJSONV1, tf *TraceFacts) []ExpectationFailure {
	if sem == nil {
		return nil
	}

	var failures []ExpectationFailure
	if len(fb.ResultJSON) == 0 {
		return []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_SEMANTIC_RESULT_JSON",
			Message: "semantic expectations require feedback.resultJson",
		}}
	}

	var doc any
	if err := json.Unmarshal(fb.ResultJSON, &doc); err != nil {
		return []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_SEMANTIC_RESULT_JSON",
			Message: "feedback resultJson is not valid json",
		}}
	}

	placeholders := map[string]bool{}
	for _, p := range sem.PlaceholderValues {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			placeholders[p] = true
		}
	}

	for _, ptr := range sem.RequiredJSONPointers {
		if _, ok := jsonPointerLookup(doc, ptr); !ok {
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_SEMANTIC_REQUIRED_POINTER",
				Message: "missing required semantic pointer " + ptr,
			})
		}
	}

	nonEmptyCount := int64(0)
	for _, ptr := range sem.NonEmptyJSONPointers {
		v, ok := jsonPointerLookup(doc, ptr)
		if !ok {
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_SEMANTIC_NONEMPTY_POINTER",
				Message: "missing non-empty semantic pointer " + ptr,
			})
			continue
		}
		if !isMeaningfulValue(v, placeholders) {
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_SEMANTIC_NONEMPTY_POINTER",
				Message: "semantic pointer is empty/placeholder " + ptr,
			})
			continue
		}
		nonEmptyCount++
	}

	meaningfulCount := countMeaningfulValues(doc, placeholders)
	if sem.MinMeaningfulFields > 0 {
		if meaningfulCount < sem.MinMeaningfulFields {
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_SEMANTIC_MIN_FIELDS",
				Message: fmt.Sprintf("meaningful fields %d < minMeaningfulFields %d", meaningfulCount, sem.MinMeaningfulFields),
			})
		}
	}

	traceRules := len(sem.RequireToolOps) > 0 || len(sem.RequireCommandPrefix) > 0 || len(sem.RequireMCPTool) > 0 || sem.SuspiciousBoilerplate
	if traceRules && tf == nil {
		failures = append(failures, ExpectationFailure{
			Code:    "ZCL_E_EXPECT_TRACE_MISSING",
			Message: "semantic expectations require trace-derived facts",
		})
		return failures
	}
	if tf == nil {
		return failures
	}

	if len(sem.RequireToolOps) > 0 {
		ok := false
		for _, seen := range tf.ToolOpsSeen {
			if slices.Contains(sem.RequireToolOps, seen) {
				ok = true
				break
			}
		}
		if !ok {
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_SEMANTIC_TOOL_OP",
				Message: "required semantic tool op not observed in trace",
			})
		}
	}

	if len(sem.RequireCommandPrefix) > 0 {
		ok := false
		for _, seen := range tf.CommandNamesSeen {
			for _, pref := range sem.RequireCommandPrefix {
				if pref != "" && strings.HasPrefix(seen, pref) {
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_SEMANTIC_COMMAND_PREFIX",
				Message: "required semantic command prefix not observed in trace",
			})
		}
	}

	if len(sem.RequireMCPTool) > 0 {
		ok := false
		for _, seen := range tf.MCPToolsSeen {
			for _, req := range sem.RequireMCPTool {
				if seen == req {
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_SEMANTIC_MCP_TOOL",
				Message: "required semantic MCP tool not observed in trace",
			})
		}
	}

	if sem.SuspiciousBoilerplate {
		boilerplateMCP := sem.BoilerplateMCPTools
		if len(boilerplateMCP) == 0 {
			boilerplateMCP = defaultBoilerplateMCPTools
		}
		maxMeaningful := sem.MaxMeaningfulFieldsForBoilerplate
		if maxMeaningful <= 0 {
			maxMeaningful = 1
		}

		suspiciousMCP := false
		if len(tf.MCPToolsSeen) > 0 && len(boilerplateMCP) > 0 {
			suspiciousMCP = true
			for _, name := range tf.MCPToolsSeen {
				if !slices.Contains(boilerplateMCP, name) {
					suspiciousMCP = false
					break
				}
			}
		}

		suspiciousCmd := false
		if len(tf.CommandNamesSeen) > 0 && len(sem.BoilerplateCommandPrefixes) > 0 {
			suspiciousCmd = true
			for _, seen := range tf.CommandNamesSeen {
				matched := false
				for _, pref := range sem.BoilerplateCommandPrefixes {
					if pref != "" && strings.HasPrefix(seen, pref) {
						matched = true
						break
					}
				}
				if !matched {
					suspiciousCmd = false
					break
				}
			}
		}

		if (suspiciousMCP || suspiciousCmd) && meaningfulCount <= maxMeaningful && nonEmptyCount <= maxMeaningful {
			failures = append(failures, ExpectationFailure{
				Code:    "ZCL_E_EXPECT_SEMANTIC_BOILERPLATE",
				Message: "trace appears boilerplate for low-semantic output",
			})
		}
	}

	return failures
}

func isMeaningfulValue(v any, placeholders map[string]bool) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return false
		}
		if placeholders[strings.ToLower(s)] {
			return false
		}
		return true
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

func countMeaningfulValues(v any, placeholders map[string]bool) int64 {
	switch x := v.(type) {
	case map[string]any:
		var n int64
		for _, value := range x {
			if isMeaningfulValue(value, placeholders) {
				n++
			}
		}
		return n
	case []any:
		var n int64
		for _, value := range x {
			if isMeaningfulValue(value, placeholders) {
				n++
			}
		}
		return n
	default:
		if isMeaningfulValue(v, placeholders) {
			return 1
		}
		return 0
	}
}
