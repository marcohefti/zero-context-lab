package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestContractSnapshot(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	snapshotPath := filepath.Join(repoRoot, "test", "fixtures", "contract", "contract.snapshot.json")

	wantBytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}

	got := Build("0.0.0-dev")
	var gotBuf bytes.Buffer
	enc := json.NewEncoder(&gotBuf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(got); err != nil {
		t.Fatalf("encode contract: %v", err)
	}

	wantContract, err := decodeContractStrict(wantBytes)
	if err != nil {
		t.Fatalf("decode snapshot strict: %v", err)
	}
	gotContract, err := decodeContractStrict(gotBuf.Bytes())
	if err != nil {
		t.Fatalf("decode current strict: %v", err)
	}

	if !reflect.DeepEqual(wantContract, gotContract) {
		t.Fatalf("contract snapshot mismatch; run:\n  scripts/contract-snapshot.sh --update --snapshot %s", snapshotPath)
	}
}

func decodeContractStrict(b []byte) (Contract, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c Contract
	if err := dec.Decode(&c); err != nil {
		return Contract{}, err
	}
	return c, nil
}
