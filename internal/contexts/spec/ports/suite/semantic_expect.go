package suite

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
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
	doc, failures := decodeSemanticDoc(fb)
	if doc == nil {
		return failures
	}
	placeholders := semanticPlaceholders(sem.PlaceholderValues)
	failures = append(failures, validateRequiredSemanticPointers(doc, sem.RequiredJSONPointers)...)
	nonEmptyCount, nonEmptyFailures := validateNonEmptySemanticPointers(doc, sem.NonEmptyJSONPointers, placeholders)
	failures = append(failures, nonEmptyFailures...)
	meaningfulCount := countMeaningfulValues(doc, placeholders)
	failures = append(failures, validateSemanticMeaningfulCount(sem.MinMeaningfulFields, meaningfulCount)...)
	traceFailures := validateSemanticTraceRules(sem, tf, meaningfulCount, nonEmptyCount)
	return append(failures, traceFailures...)
}

func decodeSemanticDoc(fb schema.FeedbackJSONV1) (any, []ExpectationFailure) {
	if len(fb.ResultJSON) == 0 {
		return nil, []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_SEMANTIC_RESULT_JSON",
			Message: "semantic expectations require feedback.resultJson",
		}}
	}
	var doc any
	if err := json.Unmarshal(fb.ResultJSON, &doc); err != nil {
		return nil, []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_SEMANTIC_RESULT_JSON",
			Message: "feedback resultJson is not valid json",
		}}
	}
	return doc, nil
}

func semanticPlaceholders(values []string) map[string]bool {
	out := map[string]bool{}
	for _, p := range values {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out[p] = true
		}
	}
	return out
}

func validateRequiredSemanticPointers(doc any, pointers []string) []ExpectationFailure {
	failures := make([]ExpectationFailure, 0, len(pointers))
	for _, ptr := range pointers {
		if _, ok := jsonPointerLookup(doc, ptr); ok {
			continue
		}
		failures = append(failures, ExpectationFailure{
			Code:    "ZCL_E_EXPECT_SEMANTIC_REQUIRED_POINTER",
			Message: "missing required semantic pointer " + ptr,
		})
	}
	return failures
}

func validateNonEmptySemanticPointers(doc any, pointers []string, placeholders map[string]bool) (int64, []ExpectationFailure) {
	failures := make([]ExpectationFailure, 0, len(pointers))
	nonEmptyCount := int64(0)
	for _, ptr := range pointers {
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
	return nonEmptyCount, failures
}

func validateSemanticMeaningfulCount(minFields int64, meaningfulCount int64) []ExpectationFailure {
	if minFields <= 0 || meaningfulCount >= minFields {
		return nil
	}
	return []ExpectationFailure{{
		Code:    "ZCL_E_EXPECT_SEMANTIC_MIN_FIELDS",
		Message: fmt.Sprintf("meaningful fields %d < minMeaningfulFields %d", meaningfulCount, minFields),
	}}
}

func validateSemanticTraceRules(sem *SemanticExpectsV1, tf *TraceFacts, meaningfulCount, nonEmptyCount int64) []ExpectationFailure {
	traceRules := len(sem.RequireToolOps) > 0 || len(sem.RequireCommandPrefix) > 0 || len(sem.RequireMCPTool) > 0 || sem.SuspiciousBoilerplate
	if !traceRules {
		return nil
	}
	if tf == nil {
		return []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_TRACE_MISSING",
			Message: "semantic expectations require trace-derived facts",
		}}
	}
	failures := make([]ExpectationFailure, 0, 4)
	if len(sem.RequireToolOps) > 0 && !containsAny(sem.RequireToolOps, tf.ToolOpsSeen) {
		failures = append(failures, ExpectationFailure{
			Code:    "ZCL_E_EXPECT_SEMANTIC_TOOL_OP",
			Message: "required semantic tool op not observed in trace",
		})
	}
	if len(sem.RequireCommandPrefix) > 0 && !hasPrefixMatch(tf.CommandNamesSeen, sem.RequireCommandPrefix) {
		failures = append(failures, ExpectationFailure{
			Code:    "ZCL_E_EXPECT_SEMANTIC_COMMAND_PREFIX",
			Message: "required semantic command prefix not observed in trace",
		})
	}
	if len(sem.RequireMCPTool) > 0 && !containsAny(sem.RequireMCPTool, tf.MCPToolsSeen) {
		failures = append(failures, ExpectationFailure{
			Code:    "ZCL_E_EXPECT_SEMANTIC_MCP_TOOL",
			Message: "required semantic MCP tool not observed in trace",
		})
	}
	if isSuspiciousBoilerplate(sem, tf, meaningfulCount, nonEmptyCount) {
		failures = append(failures, ExpectationFailure{
			Code:    "ZCL_E_EXPECT_SEMANTIC_BOILERPLATE",
			Message: "trace appears boilerplate for low-semantic output",
		})
	}
	return failures
}

func containsAny(required []string, seen []string) bool {
	for _, item := range seen {
		if slices.Contains(required, item) {
			return true
		}
	}
	return false
}

func hasPrefixMatch(seen []string, prefixes []string) bool {
	for _, item := range seen {
		for _, pref := range prefixes {
			if pref != "" && strings.HasPrefix(item, pref) {
				return true
			}
		}
	}
	return false
}

func isSuspiciousBoilerplate(sem *SemanticExpectsV1, tf *TraceFacts, meaningfulCount, nonEmptyCount int64) bool {
	if !sem.SuspiciousBoilerplate {
		return false
	}
	boilerplateMCP := sem.BoilerplateMCPTools
	if len(boilerplateMCP) == 0 {
		boilerplateMCP = defaultBoilerplateMCPTools
	}
	maxMeaningful := sem.MaxMeaningfulFieldsForBoilerplate
	if maxMeaningful <= 0 {
		maxMeaningful = 1
	}
	suspiciousMCP := allInSet(tf.MCPToolsSeen, boilerplateMCP)
	suspiciousCmd := allHavePrefix(tf.CommandNamesSeen, sem.BoilerplateCommandPrefixes)
	return (suspiciousMCP || suspiciousCmd) && meaningfulCount <= maxMeaningful && nonEmptyCount <= maxMeaningful
}

func allInSet(values []string, allowed []string) bool {
	if len(values) == 0 || len(allowed) == 0 {
		return false
	}
	for _, value := range values {
		if slices.Contains(allowed, value) {
			continue
		}
		return false
	}
	return true
}

func allHavePrefix(values []string, prefixes []string) bool {
	if len(values) == 0 || len(prefixes) == 0 {
		return false
	}
	for _, value := range values {
		matched := false
		for _, pref := range prefixes {
			if pref != "" && strings.HasPrefix(value, pref) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		return false
	}
	return true
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
