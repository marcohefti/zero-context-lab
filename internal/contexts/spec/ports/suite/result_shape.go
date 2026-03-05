package suite

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/marcohefti/zero-context-lab/internal/kernel/schema"
)

// IsValidJSONPointer returns true when s is a valid RFC 6901 pointer.
func IsValidJSONPointer(s string) bool {
	_, ok := parseJSONPointer(s)
	return ok
}

// ValidateResultShape validates type/shape only (not mission correctness).
func ValidateResultShape(re *ResultExpectsV1, fb schema.FeedbackJSONV1) []ExpectationFailure {
	if re == nil {
		return nil
	}
	switch re.Type {
	case "string":
		return validateStringResultShape(fb)
	case "json":
		return validateJSONResultShape(re, fb)
	default:
		return []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_RESULT_TYPE",
			Message: "unsupported expects.result.type",
		}}
	}
}

func validateStringResultShape(fb schema.FeedbackJSONV1) []ExpectationFailure {
	if len(fb.ResultJSON) == 0 {
		return nil
	}
	return []ExpectationFailure{{
		Code:    "ZCL_E_EXPECT_RESULT_TYPE",
		Message: "expected result type string, got resultJson",
	}}
}

func validateJSONResultShape(re *ResultExpectsV1, fb schema.FeedbackJSONV1) []ExpectationFailure {
	if len(fb.ResultJSON) == 0 {
		return []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_RESULT_TYPE",
			Message: "expected result type json, got result",
		}}
	}
	if len(re.RequiredJSONPointers) == 0 {
		return nil
	}
	doc, ok := decodeResultJSON(fb.ResultJSON)
	if !ok {
		return []ExpectationFailure{{
			Code:    "ZCL_E_EXPECT_RESULT_JSON",
			Message: "feedback resultJson is not valid json",
		}}
	}
	return missingJSONPointerFailures(re.RequiredJSONPointers, doc)
}

func decodeResultJSON(raw json.RawMessage) (any, bool) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false
	}
	return doc, true
}

func missingJSONPointerFailures(pointers []string, doc any) []ExpectationFailure {
	failures := make([]ExpectationFailure, 0, len(pointers))
	for _, ptr := range pointers {
		if _, ok := jsonPointerLookup(doc, ptr); ok {
			continue
		}
		failures = append(failures, ExpectationFailure{
			Code:    "ZCL_E_EXPECT_RESULT_JSON_POINTER",
			Message: "missing required resultJson pointer " + ptr,
		})
	}
	return failures
}

func parseJSONPointer(s string) ([]string, bool) {
	if s == "" || s[0] != '/' {
		return nil, false
	}
	parts := strings.Split(s[1:], "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		dec, ok := decodeJSONPointerToken(p)
		if !ok {
			return nil, false
		}
		out = append(out, dec)
	}
	return out, true
}

func decodeJSONPointerToken(s string) (string, bool) {
	if !strings.Contains(s, "~") {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch != '~' {
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(s) {
			return "", false
		}
		i++
		switch s[i] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", false
		}
	}
	return b.String(), true
}

func jsonPointerLookup(doc any, pointer string) (any, bool) {
	tokens, ok := parseJSONPointer(pointer)
	if !ok {
		return nil, false
	}
	cur := doc
	for _, tok := range tokens {
		switch n := cur.(type) {
		case map[string]any:
			next, exists := n[tok]
			if !exists {
				return nil, false
			}
			cur = next
		case []any:
			if tok == "-" {
				return nil, false
			}
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(n) {
				return nil, false
			}
			cur = n[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}
