package native

import "testing"

func TestProtocolContractValidate(t *testing.T) {
	contract := ProtocolContract{
		RuntimeName:           "codex_app_server",
		MinimumProtocolMajor:  2,
		MinimumProtocolMinor:  0,
		MinimumRuntimeVersion: "1.2.3",
	}
	if err := contract.Validate("2.1", "1.2.3"); err != nil {
		t.Fatalf("expected compatible: %v", err)
	}
	if err := contract.Validate("2.0", "1.3.0"); err != nil {
		t.Fatalf("expected compatible: %v", err)
	}
}

func TestProtocolContractRejectsOldProtocol(t *testing.T) {
	contract := ProtocolContract{RuntimeName: "codex", MinimumProtocolMajor: 2, MinimumProtocolMinor: 0}
	err := contract.Validate("1.9", "")
	if err == nil {
		t.Fatalf("expected error")
	}
	nerr, ok := AsError(err)
	if !ok {
		t.Fatalf("expected native error, got %T", err)
	}
	if nerr.Kind != ErrorCompatibility {
		t.Fatalf("expected compatibility error, got %q", nerr.Kind)
	}
}

func TestProtocolContractRejectsOldRuntimeVersion(t *testing.T) {
	contract := ProtocolContract{RuntimeName: "codex", MinimumProtocolMajor: 2, MinimumProtocolMinor: 0, MinimumRuntimeVersion: "1.4.0"}
	err := contract.Validate("2.0", "1.3.9")
	if err == nil {
		t.Fatalf("expected error")
	}
	nerr, ok := AsError(err)
	if !ok {
		t.Fatalf("expected native error, got %T", err)
	}
	if nerr.Kind != ErrorCompatibility {
		t.Fatalf("expected compatibility error, got %q", nerr.Kind)
	}
}
