package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestShouldBreakStaleLock_WithoutOwnerMetadata(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "x.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	old := time.Now().Add(-3 * time.Minute)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatalf("chtimes lock dir: %v", err)
	}
	if !shouldBreakStaleLock(lockDir, 2*time.Minute, time.Now()) {
		t.Fatalf("expected stale lock without owner metadata to be breakable")
	}
}

func TestShouldBreakStaleLock_WithAliveOwner(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "x.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}

	owner := lockOwnerV1{
		V:         1,
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(owner)
	if err != nil {
		t.Fatalf("marshal owner: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), b, 0o644); err != nil {
		t.Fatalf("write owner: %v", err)
	}
	old := time.Now().Add(-3 * time.Minute)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatalf("chtimes lock dir: %v", err)
	}

	got := shouldBreakStaleLock(lockDir, 2*time.Minute, time.Now())
	if runtime.GOOS == "windows" {
		if !got {
			t.Fatalf("expected windows fallback to keep stale-time behavior")
		}
		return
	}
	if got {
		t.Fatalf("expected stale lock with alive owner to not be breakable")
	}
}

func TestWithDirLock_TimeoutReturnsTypedError(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "x.lock")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	err := WithDirLock(lockDir, 20*time.Millisecond, func() error { return nil })
	if err == nil {
		t.Fatalf("expected lock timeout error")
	}
	if !IsLockTimeout(err) {
		t.Fatalf("expected typed lock timeout error, got %v", err)
	}
}
