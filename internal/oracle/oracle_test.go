package oracle

import "testing"

func TestEvaluateProof_FormatToleranceByPolicy(t *testing.T) {
	file := FileV1{
		CollectFields: []string{"blogUrl"},
		Rules: []RuleV1{
			{Field: "blogUrl", Op: OpEQ, Value: "https://blog.heftiweb.ch"},
		},
		root: map[string]any{"startUrl": "https://example.com"},
	}
	proof := map[string]any{"blogUrl": "https://blog.heftiweb.ch/"}

	strict := EvaluateProof(file, proof, PolicyModeStrict)
	if strict.OK || len(strict.Mismatches) != 1 || strict.Mismatches[0].MismatchClass != MismatchFormat {
		t.Fatalf("expected strict format mismatch, got %+v", strict)
	}

	norm := EvaluateProof(file, proof, PolicyModeNormalized)
	if !norm.OK {
		t.Fatalf("expected normalized mode pass, got %+v", norm)
	}
}

func TestEvaluateProof_SemanticContainsPhrase(t *testing.T) {
	file := FileV1{
		Rules: []RuleV1{
			{Field: "message", Op: OpEQ, Value: "It's enabled!"},
		},
	}
	proof := map[string]any{
		"message": `Done: clicked Enable, waited for "It's enabled!", verified input.`,
	}
	strict := EvaluateProof(file, proof, PolicyModeStrict)
	if strict.OK {
		t.Fatalf("strict mode should fail")
	}
	if got := strict.Mismatches[0].MismatchClass; got != MismatchFormat {
		t.Fatalf("expected format class, got %q", got)
	}
	semantic := EvaluateProof(file, proof, PolicyModeSemantic)
	if !semantic.OK {
		t.Fatalf("semantic mode should pass: %+v", semantic)
	}
}

func TestEvaluateProof_NewOps(t *testing.T) {
	file := FileV1{
		root: map[string]any{"startUrl": "https://heftiweb.ch"},
		Rules: []RuleV1{
			{Field: "downloadsItems", Op: OpSetEQ, Value: "PDF, CSV, Excel"},
			{Field: "paddingTopPx", Op: OpNumEQ, Value: 6},
			{Field: "firstCommand", Op: OpCommandHeadEQ, Value: "curl"},
			{Field: "blogUrl", Op: OpURLEQLoose, Value: "https://blog.heftiweb.ch"},
			{Field: "message", Op: OpContainsPhrase, Value: "Hello World!"},
		},
	}
	proof := map[string]any{
		"downloadsItems": []any{"PDF", "CSV", "Excel"},
		"paddingTopPx":   "6px",
		"firstCommand":   "$ curl -LsSf https://astral.sh/uv/install.sh | sh",
		"blogUrl":        "https://blog.heftiweb.ch/",
		"message":        "Dynamic load completed successfully: Hello World! appears in #finish.",
	}
	got := EvaluateProof(file, proof, PolicyModeStrict)
	if !got.OK {
		t.Fatalf("expected all tolerant ops to pass, got %+v", got)
	}
}

func TestEvaluateProof_LogicalComposition(t *testing.T) {
	file := FileV1{
		Rules: []RuleV1{
			{
				AnyOf: []RuleV1{
					{Field: "status", Op: OpEQ, Value: "ok"},
					{Field: "status", Op: OpEQ, Value: "success"},
				},
			},
			{
				AllOf: []RuleV1{
					{Field: "count", Op: OpGTE, Value: 10},
					{Field: "firstCommand", Op: OpCommandHeadEQ, Value: "curl"},
				},
			},
		},
	}
	proof := map[string]any{
		"status":       "success",
		"count":        29,
		"firstCommand": "$ curl -LsSf https://astral.sh/uv/install.sh | sh",
	}
	got := EvaluateProof(file, proof, PolicyModeStrict)
	if !got.OK {
		t.Fatalf("expected logical composition pass, got %+v", got)
	}
}

func TestEvaluateProof_AdversarialNoOverPermissivePass(t *testing.T) {
	file := FileV1{
		Rules: []RuleV1{
			{Field: "imageSrc", Op: OpEQ, Value: "https://the-internet.herokuapp.com/img/avatar.jpg"},
			{Field: "navItemsCount", Op: OpEQ, Value: 9},
		},
	}
	proof := map[string]any{
		"imageSrc":      "https://the-internet.herokuapp.com/img/forkme_right_green_007200.png",
		"navItemsCount": 1,
	}
	semantic := EvaluateProof(file, proof, PolicyModeSemantic)
	if semantic.OK {
		t.Fatalf("expected semantic negatives to fail, got %+v", semantic)
	}
	for _, mm := range semantic.Mismatches {
		if mm.MismatchClass == MismatchFormat {
			t.Fatalf("expected semantic mismatches only, got %+v", semantic.Mismatches)
		}
	}
}
