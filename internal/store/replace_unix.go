//go:build !windows

package store

import (
	"os"
	"path/filepath"
)

func replaceFile(tmpPath, finalPath string) error {
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return err
	}
	// Crash-consistency: ensure the directory entry is durable.
	if d, err := os.Open(filepath.Dir(finalPath)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
