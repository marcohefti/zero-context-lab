package oracle

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	OpEQ             = "eq"
	OpEQRef          = "eq_ref"
	OpStartsWith     = "starts_with"
	OpContains       = "contains"
	OpGTE            = "gte"
	OpNonEmpty       = "non_empty"
	OpURLEQLoose     = "url_eq_loose"
	OpSetEQ          = "set_eq"
	OpNumEQ          = "num_eq"
	OpContainsPhrase = "contains_phrase"
	OpCommandHeadEQ  = "command_head_eq"

	NormalizerTrim               = "trim"
	NormalizerLower              = "lower"
	NormalizerStripTrailingSlash = "strip_trailing_slash"
	NormalizerCSSPXToNumber      = "css_px_to_number"
	NormalizerCSVToArray         = "csv_to_array"
	NormalizerShellPromptStrip   = "shell_prompt_strip"

	MismatchFormat   = "format"
	MismatchType     = "type"
	MismatchSemantic = "semantic"

	PolicyModeStrict     = "strict"
	PolicyModeNormalized = "normalized"
	PolicyModeSemantic   = "semantic"
)

var cssPXNumberRE = regexp.MustCompile(`^\s*(-?(?:\d+\.?\d*|\d*\.?\d+))\s*px\s*$`)

type RuleV1 struct {
	Field     string   `json:"field,omitempty"`
	Op        string   `json:"op,omitempty"`
	Value     any      `json:"value,omitempty"`
	Values    []any    `json:"values,omitempty"`
	Ref       string   `json:"ref,omitempty"`
	Normalize []string `json:"normalize,omitempty"`
	AnyOf     []RuleV1 `json:"anyOf,omitempty"`
	AllOf     []RuleV1 `json:"allOf,omitempty"`
}

type FileV1 struct {
	SchemaVersion int      `json:"schemaVersion,omitempty"`
	MissionID     string   `json:"missionId,omitempty"`
	CollectFields []string `json:"collectFields,omitempty"`
	Rules         []RuleV1 `json:"rules,omitempty"`

	root map[string]any
}

type Mismatch struct {
	Field              string `json:"field,omitempty"`
	Op                 string `json:"op,omitempty"`
	MismatchClass      string `json:"mismatchClass,omitempty"` // format|type|semantic
	Message            string `json:"message,omitempty"`
	Expected           any    `json:"expected,omitempty"`
	Actual             any    `json:"actual,omitempty"`
	NormalizedExpected any    `json:"normalizedExpected,omitempty"`
	NormalizedActual   any    `json:"normalizedActual,omitempty"`
}

type Verdict struct {
	OK         bool       `json:"ok"`
	Message    string     `json:"message,omitempty"`
	Mismatches []Mismatch `json:"mismatches,omitempty"`
}

func LoadFile(path string) (FileV1, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return FileV1{}, err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return FileV1{}, fmt.Errorf("invalid oracle json: %w", err)
	}
	var file FileV1
	if err := json.Unmarshal(raw, &file); err != nil {
		return FileV1{}, fmt.Errorf("invalid oracle json: %w", err)
	}
	file.CollectFields = normalizeNonEmptyStrings(file.CollectFields, false)
	for i := range file.Rules {
		normalizeRule(&file.Rules[i])
	}
	file.root = root
	return file, nil
}

func EvaluateProof(file FileV1, proof map[string]any, policyMode string) Verdict {
	policyMode = strings.TrimSpace(strings.ToLower(policyMode))
	if policyMode == "" {
		policyMode = PolicyModeStrict
	}
	mismatches := make([]Mismatch, 0, 8)
	for _, field := range file.CollectFields {
		if _, ok := proof[field]; !ok {
			mismatches = append(mismatches, Mismatch{
				Field:         field,
				Op:            "collect_field_present",
				MismatchClass: MismatchSemantic,
				Message:       "missing proof field: " + field,
				Expected:      "present",
				Actual:        nil,
			})
		}
	}
	if len(file.Rules) == 0 {
		mismatches = append(mismatches, Mismatch{
			Op:            "rules",
			MismatchClass: MismatchSemantic,
			Message:       "oracle rules are missing",
		})
		return Verdict{OK: false, Message: composeMessage(mismatches), Mismatches: mismatches}
	}
	for _, rule := range file.Rules {
		mismatches = append(mismatches, evaluateRule(rule, file.root, proof, policyMode)...)
	}
	ok := len(mismatches) == 0
	return Verdict{
		OK:         ok,
		Message:    composeMessage(mismatches),
		Mismatches: mismatches,
	}
}

func InferMismatch(field string, op string, expected any, actual any, policyMode string) *Mismatch {
	policyMode = strings.TrimSpace(strings.ToLower(policyMode))
	if policyMode == "" {
		policyMode = PolicyModeStrict
	}
	rule := RuleV1{Field: strings.TrimSpace(field), Op: strings.TrimSpace(strings.ToLower(op)), Value: expected}
	if rule.Op == "" {
		rule.Op = OpEQ
	}
	normalizeRule(&rule)
	_, mm := evaluateSingle(rule, map[string]any{}, map[string]any{rule.Field: actual}, policyMode)
	if mm == nil {
		return nil
	}
	return mm
}

func AllMismatchesClass(mismatches []Mismatch, class string) bool {
	class = strings.TrimSpace(strings.ToLower(class))
	if class == "" || len(mismatches) == 0 {
		return false
	}
	for _, mm := range mismatches {
		if strings.TrimSpace(strings.ToLower(mm.MismatchClass)) != class {
			return false
		}
	}
	return true
}

func evaluateRule(rule RuleV1, oracleRoot map[string]any, proof map[string]any, policyMode string) []Mismatch {
	if len(rule.AllOf) > 0 {
		var out []Mismatch
		for _, sub := range rule.AllOf {
			out = append(out, evaluateRule(sub, oracleRoot, proof, policyMode)...)
		}
		return out
	}
	if len(rule.AnyOf) > 0 {
		var best []Mismatch
		for _, sub := range rule.AnyOf {
			cur := evaluateRule(sub, oracleRoot, proof, policyMode)
			if len(cur) == 0 {
				return nil
			}
			if len(best) == 0 || len(cur) < len(best) {
				best = cur
			}
		}
		return best
	}
	_, mm := evaluateSingle(rule, oracleRoot, proof, policyMode)
	if mm == nil {
		return nil
	}
	return []Mismatch{*mm}
}

func evaluateSingle(rule RuleV1, oracleRoot map[string]any, proof map[string]any, policyMode string) (bool, *Mismatch) {
	op := strings.TrimSpace(strings.ToLower(rule.Op))
	field := strings.TrimSpace(rule.Field)
	if op == "" {
		return false, &Mismatch{
			Field:         field,
			Op:            op,
			MismatchClass: MismatchSemantic,
			Message:       "oracle rule missing field/op",
		}
	}
	if field == "" {
		return false, &Mismatch{
			Op:            op,
			MismatchClass: MismatchSemantic,
			Message:       "oracle rule missing field/op",
		}
	}
	actual := proof[field]
	switch op {
	case OpNonEmpty:
		if isNonEmpty(actual) {
			return true, nil
		}
		mm := inferPairMismatch(field, op, "non-empty", actual, "must be non-empty", policyMode)
		return false, mm
	}

	expectedCandidates := expectedCandidates(rule, oracleRoot)
	if len(expectedCandidates) == 0 {
		return false, &Mismatch{
			Field:         field,
			Op:            op,
			MismatchClass: MismatchSemantic,
			Message:       "oracle rule missing value/ref",
		}
	}

	var best *Mismatch
	for _, expected := range expectedCandidates {
		ok, mm := evaluateSingleExpected(rule, expected, actual, policyMode)
		if ok {
			return true, nil
		}
		if best == nil || mismatchRank(*mm) < mismatchRank(*best) {
			best = mm
		}
	}
	if best == nil {
		return false, &Mismatch{
			Field:         field,
			Op:            op,
			MismatchClass: MismatchSemantic,
			Message:       "oracle rule failed",
		}
	}
	return false, best
}

func evaluateSingleExpected(rule RuleV1, expectedRaw any, actualRaw any, policyMode string) (bool, *Mismatch) {
	field := strings.TrimSpace(rule.Field)
	op := strings.TrimSpace(strings.ToLower(rule.Op))
	actual := actualRaw
	expected := expectedRaw
	var err error
	if len(rule.Normalize) > 0 {
		actual, err = applyNormalizers(actual, rule.Normalize)
		if err != nil {
			return false, &Mismatch{
				Field:         field,
				Op:            op,
				MismatchClass: MismatchType,
				Message:       "normalizer failed for actual: " + err.Error(),
				Expected:      expectedRaw,
				Actual:        actualRaw,
			}
		}
		expected, err = applyNormalizers(expected, rule.Normalize)
		if err != nil {
			return false, &Mismatch{
				Field:         field,
				Op:            op,
				MismatchClass: MismatchType,
				Message:       "normalizer failed for expected: " + err.Error(),
				Expected:      expectedRaw,
				Actual:        actualRaw,
			}
		}
	}

	switch op {
	case OpEQ, OpEQRef:
		if equalValues(actual, expected) {
			return true, nil
		}
		if policyMode == PolicyModeNormalized && normalizedEquivalent(expectedRaw, actualRaw) {
			return true, nil
		}
		if policyMode == PolicyModeSemantic && (normalizedEquivalent(expectedRaw, actualRaw) || phraseEquivalent(expectedRaw, actualRaw)) {
			return true, nil
		}
		msg := fmt.Sprintf("%s expected %s got %s", field, valueAsString(expectedRaw), valueAsString(actualRaw))
		return false, inferPairMismatch(field, op, expectedRaw, actualRaw, msg, policyMode, expected, actual)
	case OpStartsWith:
		a, aok := actual.(string)
		e, eok := expected.(string)
		if !aok || !eok {
			return false, inferPairMismatch(field, op, expectedRaw, actualRaw, field+" must be a string for starts_with", policyMode, expected, actual)
		}
		if strings.HasPrefix(a, e) {
			return true, nil
		}
		return false, inferPairMismatch(field, op, expectedRaw, actualRaw, fmt.Sprintf("%s must start with %s got %s", field, valueAsString(expectedRaw), valueAsString(actualRaw)), policyMode, expected, actual)
	case OpContains:
		a, aok := actual.(string)
		e, eok := expected.(string)
		if !aok || !eok {
			return false, inferPairMismatch(field, op, expectedRaw, actualRaw, field+" must be a string for contains", policyMode, expected, actual)
		}
		if strings.Contains(a, e) {
			return true, nil
		}
		return false, inferPairMismatch(field, op, expectedRaw, actualRaw, fmt.Sprintf("%s must contain %s got %s", field, valueAsString(expectedRaw), valueAsString(actualRaw)), policyMode, expected, actual)
	case OpContainsPhrase:
		a, aok := actual.(string)
		e, eok := expected.(string)
		if !aok || !eok {
			return false, inferPairMismatch(field, op, expectedRaw, actualRaw, field+" must be a string for contains_phrase", policyMode, expected, actual)
		}
		if strings.Contains(strings.ToLower(a), strings.ToLower(e)) {
			return true, nil
		}
		return false, inferPairMismatch(field, op, expectedRaw, actualRaw, fmt.Sprintf("%s must contain phrase %s got %s", field, valueAsString(expectedRaw), valueAsString(actualRaw)), policyMode, expected, actual)
	case OpGTE:
		av, aok := toFloat64(actual)
		ev, eok := toFloat64(expected)
		if !aok || !eok {
			return false, inferPairMismatch(field, op, expectedRaw, actualRaw, field+" must be a number for gte", policyMode, expected, actual)
		}
		if av >= ev {
			return true, nil
		}
		return false, inferPairMismatch(field, op, expectedRaw, actualRaw, fmt.Sprintf("%s must be >= %s got %s", field, valueAsString(expectedRaw), valueAsString(actualRaw)), policyMode, expected, actual)
	case OpNumEQ:
		av, aok := toFloat64(actual)
		ev, eok := toFloat64(expected)
		if !aok || !eok {
			return false, inferPairMismatch(field, op, expectedRaw, actualRaw, field+" must be numeric for num_eq", policyMode, expected, actual)
		}
		if numericEqual(av, ev) {
			return true, nil
		}
		return false, inferPairMismatch(field, op, expectedRaw, actualRaw, fmt.Sprintf("%s expected numeric %s got %s", field, valueAsString(expectedRaw), valueAsString(actualRaw)), policyMode, expected, actual)
	case OpURLEQLoose:
		a, aok := canonicalLooseURL(actual)
		e, eok := canonicalLooseURL(expected)
		if !aok || !eok {
			return false, inferPairMismatch(field, op, expectedRaw, actualRaw, field+" must be a url string for url_eq_loose", policyMode, expected, actual)
		}
		if a == e {
			return true, nil
		}
		return false, inferPairMismatch(field, op, expectedRaw, actualRaw, fmt.Sprintf("%s expected %s got %s", field, valueAsString(expectedRaw), valueAsString(actualRaw)), policyMode, e, a)
	case OpSetEQ:
		as, aok := toStringSet(actual)
		es, eok := toStringSet(expected)
		if !aok || !eok {
			return false, inferPairMismatch(field, op, expectedRaw, actualRaw, field+" must be csv or array for set_eq", policyMode, expected, actual)
		}
		if stringSetEqual(as, es) {
			return true, nil
		}
		return false, inferPairMismatch(field, op, expectedRaw, actualRaw, fmt.Sprintf("%s expected set %s got %s", field, valueAsString(expectedRaw), valueAsString(actualRaw)), policyMode, sortedKeys(es), sortedKeys(as))
	case OpCommandHeadEQ:
		ah, aok := commandHead(actual)
		eh, eok := commandHead(expected)
		if !aok || !eok {
			return false, inferPairMismatch(field, op, expectedRaw, actualRaw, field+" must be command-like for command_head_eq", policyMode, expected, actual)
		}
		if strings.EqualFold(ah, eh) {
			return true, nil
		}
		return false, inferPairMismatch(field, op, expectedRaw, actualRaw, fmt.Sprintf("%s expected command head %s got %s", field, valueAsString(expectedRaw), valueAsString(actualRaw)), policyMode, eh, ah)
	default:
		return false, &Mismatch{
			Field:              field,
			Op:                 op,
			MismatchClass:      MismatchSemantic,
			Message:            "unsupported oracle op: " + op,
			Expected:           expectedRaw,
			Actual:             actualRaw,
			NormalizedExpected: expected,
			NormalizedActual:   actual,
		}
	}
}

func inferPairMismatch(field string, op string, expected any, actual any, message string, policyMode string, normalized ...any) *Mismatch {
	var normExpected any = expected
	var normActual any = actual
	if len(normalized) >= 2 {
		normExpected = normalized[0]
		normActual = normalized[1]
	}
	class := classifyMismatch(expected, actual, normExpected, normActual)
	return &Mismatch{
		Field:              field,
		Op:                 op,
		MismatchClass:      class,
		Message:            message,
		Expected:           expected,
		Actual:             actual,
		NormalizedExpected: normExpected,
		NormalizedActual:   normActual,
	}
}

func classifyMismatch(expectedRaw any, actualRaw any, expectedNorm any, actualNorm any) string {
	if normalizedEquivalent(expectedRaw, actualRaw) || phraseEquivalent(expectedRaw, actualRaw) {
		return MismatchFormat
	}
	if equalValues(expectedNorm, actualNorm) && !equalValues(expectedRaw, actualRaw) {
		return MismatchFormat
	}
	et := normalizedType(expectedRaw)
	at := normalizedType(actualRaw)
	if et != "" && at != "" && et != at {
		return MismatchType
	}
	return MismatchSemantic
}

func mismatchRank(mm Mismatch) int {
	switch strings.TrimSpace(strings.ToLower(mm.MismatchClass)) {
	case MismatchSemantic:
		return 3
	case MismatchType:
		return 2
	case MismatchFormat:
		return 1
	default:
		return 4
	}
}

func expectedCandidates(rule RuleV1, oracleRoot map[string]any) []any {
	if len(rule.Values) > 0 {
		return append([]any{}, rule.Values...)
	}
	if strings.TrimSpace(rule.Ref) != "" {
		if oracleRoot == nil {
			return []any{nil}
		}
		return []any{oracleRoot[strings.TrimSpace(rule.Ref)]}
	}
	return []any{rule.Value}
}

func composeMessage(mismatches []Mismatch) string {
	if len(mismatches) == 0 {
		return ""
	}
	msgs := make([]string, 0, len(mismatches))
	for _, mm := range mismatches {
		m := strings.TrimSpace(mm.Message)
		if m == "" {
			continue
		}
		msgs = append(msgs, m)
	}
	if len(msgs) == 0 {
		return ""
	}
	out := strings.Join(msgs, "; ")
	if len(out) > 800 {
		return out[:799] + "…"
	}
	return out
}

func normalizeRule(rule *RuleV1) {
	if rule == nil {
		return
	}
	rule.Field = strings.TrimSpace(rule.Field)
	rule.Op = strings.TrimSpace(strings.ToLower(rule.Op))
	rule.Ref = strings.TrimSpace(rule.Ref)
	rule.Normalize = normalizeNonEmptyStrings(rule.Normalize, true)
	for i := range rule.AnyOf {
		normalizeRule(&rule.AnyOf[i])
	}
	for i := range rule.AllOf {
		normalizeRule(&rule.AllOf[i])
	}
}

func normalizeNonEmptyStrings(in []string, lower bool) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, part := range in {
		part = strings.TrimSpace(part)
		if lower {
			part = strings.ToLower(part)
		}
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func applyNormalizers(value any, names []string) (any, error) {
	out := value
	for _, raw := range names {
		switch strings.TrimSpace(strings.ToLower(raw)) {
		case NormalizerTrim:
			if s, ok := out.(string); ok {
				out = strings.TrimSpace(s)
			}
		case NormalizerLower:
			if s, ok := out.(string); ok {
				out = strings.ToLower(strings.TrimSpace(s))
			}
		case NormalizerStripTrailingSlash:
			if s, ok := out.(string); ok {
				out = stripTrailingSlashString(strings.TrimSpace(s))
			}
		case NormalizerCSSPXToNumber:
			if s, ok := out.(string); ok {
				if fv, ok := parseCSSPXNumber(s); ok {
					out = fv
				}
			}
		case NormalizerCSVToArray:
			if arr, ok := toStringSliceLoose(out); ok {
				out = arr
			}
		case NormalizerShellPromptStrip:
			if s, ok := out.(string); ok {
				out = stripShellPromptPrefix(strings.TrimSpace(s))
			}
		default:
			return nil, fmt.Errorf("unknown normalizer %q", raw)
		}
	}
	return out, nil
}

func equalValues(a any, b any) bool {
	if af, aok := toFloat64(a); aok {
		if bf, bok := toFloat64(b); bok {
			return numericEqual(af, bf)
		}
	}
	return reflect.DeepEqual(a, b)
}

func numericEqual(a float64, b float64) bool {
	return math.Abs(a-b) <= 1e-9
}

func normalizedEquivalent(expected any, actual any) bool {
	if equalValues(expected, actual) {
		return true
	}
	if ef, ok := toFloat64(expected); ok {
		if af, ok := toFloat64(actual); ok && numericEqual(ef, af) {
			return true
		}
	}
	es, eok := stringifyValue(expected)
	as, aok := stringifyValue(actual)
	if eok && aok {
		eTrim := strings.TrimSpace(es)
		aTrim := strings.TrimSpace(as)
		if eTrim == aTrim || strings.EqualFold(eTrim, aTrim) {
			return true
		}
		if stripTrailingSlashString(eTrim) == stripTrailingSlashString(aTrim) {
			return true
		}
		if eh, ok := commandHead(eTrim); ok {
			if ah, ok := commandHead(aTrim); ok && strings.EqualFold(eh, ah) {
				return true
			}
		}
	}
	if es, ok := toStringSet(expected); ok {
		if as, ok := toStringSet(actual); ok && stringSetEqual(es, as) {
			return true
		}
	}
	return false
}

func phraseEquivalent(expected any, actual any) bool {
	es, eok := stringifyValue(expected)
	as, aok := stringifyValue(actual)
	if !eok || !aok {
		return false
	}
	es = strings.TrimSpace(strings.ToLower(es))
	as = strings.TrimSpace(strings.ToLower(as))
	if es == "" || as == "" {
		return false
	}
	return strings.Contains(as, es)
}

func normalizedType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "bool"
	case float64, float32, int, int64, int32, uint, uint64:
		return "number"
	case []any, []string:
		return "array"
	case map[string]any:
		return "object"
	default:
		return ""
	}
}

func toFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint64:
		return float64(x), true
	case json.Number:
		if fv, err := x.Float64(); err == nil {
			return fv, true
		}
	case string:
		raw := strings.TrimSpace(x)
		if raw == "" {
			return 0, false
		}
		if fv, ok := parseCSSPXNumber(raw); ok {
			return fv, true
		}
		if fv, err := strconv.ParseFloat(raw, 64); err == nil {
			return fv, true
		}
	}
	return 0, false
}

func parseCSSPXNumber(raw string) (float64, bool) {
	m := cssPXNumberRE.FindStringSubmatch(strings.TrimSpace(raw))
	if len(m) != 2 {
		return 0, false
	}
	fv, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return fv, true
}

func stringifyValue(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case fmt.Stringer:
		return x.String(), true
	default:
		return "", false
	}
}

func canonicalLooseURL(v any) (string, bool) {
	raw, ok := stringifyValue(v)
	if !ok {
		return "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	u.Scheme = strings.ToLower(strings.TrimSpace(u.Scheme))
	u.Host = strings.ToLower(strings.TrimSpace(u.Host))
	u.Path = stripTrailingSlashString(u.Path)
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String(), true
}

func stripTrailingSlashString(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	for strings.HasSuffix(s, "/") && len(s) > 1 {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}

func toStringSliceLoose(v any) ([]string, bool) {
	switch x := v.(type) {
	case string:
		parts := strings.Split(x, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			out = append(out, p)
		}
		return out, true
	case []any:
		out := make([]string, 0, len(x))
		for _, part := range x {
			s, ok := stringifyValue(part)
			if !ok {
				s = fmt.Sprintf("%v", part)
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return out, true
	case []string:
		out := make([]string, 0, len(x))
		for _, part := range x {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, part)
		}
		return out, true
	default:
		return nil, false
	}
}

func toStringSet(v any) (map[string]bool, bool) {
	arr, ok := toStringSliceLoose(v)
	if !ok {
		return nil, false
	}
	set := map[string]bool{}
	for _, item := range arr {
		set[item] = true
	}
	return set, true
}

func stringSetEqual(a map[string]bool, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if !b[key] {
			return false
		}
	}
	return true
}

func sortedKeys(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func isNonEmpty(value any) bool {
	if value == nil {
		return false
	}
	switch x := value.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

func stripShellPromptPrefix(s string) string {
	s = strings.TrimSpace(s)
	for _, pref := range []string{"$ ", "# ", "% ", "> "} {
		if strings.HasPrefix(s, pref) {
			return strings.TrimSpace(strings.TrimPrefix(s, pref))
		}
	}
	return s
}

func commandHead(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		s := stripShellPromptPrefix(strings.TrimSpace(x))
		if s == "" {
			return "", false
		}
		parts := strings.Fields(s)
		if len(parts) == 0 {
			return "", false
		}
		return parts[0], true
	case []any:
		if len(x) == 0 {
			return "", false
		}
		first := fmt.Sprintf("%v", x[0])
		first = stripShellPromptPrefix(strings.TrimSpace(first))
		if first == "" {
			return "", false
		}
		return first, true
	case []string:
		if len(x) == 0 {
			return "", false
		}
		first := stripShellPromptPrefix(strings.TrimSpace(x[0]))
		if first == "" {
			return "", false
		}
		return first, true
	default:
		return "", false
	}
}

func valueAsString(value any) string {
	switch x := value.(type) {
	case string:
		return strconv.Quote(x)
	case nil:
		return "null"
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprintf("%v", x)
		}
		return string(b)
	}
}
