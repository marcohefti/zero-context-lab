package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func WithDirLock(lockDir string, wait time.Duration, fn func() error) error {
	release, err := acquireDirLock(lockDir, wait)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	return fn()
}

type lockOwnerV1 struct {
	V         int    `json:"v"`
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"`
}

func readLockOwner(lockDir string) (lockOwnerV1, bool) {
	raw, err := os.ReadFile(filepath.Join(lockDir, "owner.json"))
	if err != nil {
		return lockOwnerV1{}, false
	}
	var owner lockOwnerV1
	if err := json.Unmarshal(raw, &owner); err != nil {
		return lockOwnerV1{}, false
	}
	if owner.PID <= 0 {
		return lockOwnerV1{}, false
	}
	return owner, true
}

func shouldBreakStaleLock(lockDir string, staleAfter time.Duration, now time.Time) bool {
	info, err := os.Stat(lockDir)
	if err != nil {
		return false
	}
	if now.Sub(info.ModTime()) <= staleAfter {
		return false
	}
	if owner, ok := readLockOwner(lockDir); ok {
		if processAlive(owner.PID) {
			return false
		}
	}
	return true
}

func acquireDirLock(lockDir string, wait time.Duration) (func() error, error) {
	deadline := time.Now().Add(wait)
	staleAfter := 2 * time.Minute
	for {
		if err := os.Mkdir(lockDir, 0o755); err == nil {
			// Best-effort owner metadata for debugging and stale-lock cleanup.
			owner := lockOwnerV1{V: 1, PID: os.Getpid(), StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			if b, err := json.Marshal(owner); err == nil {
				_ = os.WriteFile(filepath.Join(lockDir, "owner.json"), b, 0o644)
			}
			return func() error { return os.RemoveAll(lockDir) }, nil
		} else if !os.IsExist(err) {
			return nil, err
		}

		// Lock exists: if it looks stale and owner is not alive, break it (crash resilience).
		if shouldBreakStaleLock(lockDir, staleAfter, time.Now()) {
			_ = os.RemoveAll(lockDir)
			continue
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout acquiring lock: %s", lockDir)
		}
		// Avoid thundering herd in concurrent writers.
		if runtime.GOOS == "windows" {
			time.Sleep(35 * time.Millisecond)
		} else {
			time.Sleep(25 * time.Millisecond)
		}
	}
}
