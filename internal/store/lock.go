package store

import (
	"fmt"
	"os"
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

func acquireDirLock(lockDir string, wait time.Duration) (func() error, error) {
	deadline := time.Now().Add(wait)
	for {
		if err := os.Mkdir(lockDir, 0o755); err == nil {
			return func() error { return os.Remove(lockDir) }, nil
		} else if !os.IsExist(err) {
			return nil, err
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout acquiring lock: %s", lockDir)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
