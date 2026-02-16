//go:build !windows

package store

import "os"

func replaceFile(tmpPath, finalPath string) error {
	return os.Rename(tmpPath, finalPath)
}
