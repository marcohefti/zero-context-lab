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

		// Lock exists: if it looks stale, break it (crash resilience).
		if info, err := os.Stat(lockDir); err == nil {
			if time.Since(info.ModTime()) > staleAfter {
				_ = os.RemoveAll(lockDir)
				continue
			}
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
