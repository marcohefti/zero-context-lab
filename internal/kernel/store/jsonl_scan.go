package store

import (
	"bufio"
	"os"
	"strings"
)

// JSONLHasNonEmptyLine returns true if the file contains at least one non-empty line.
func JSONLHasNonEmptyLine(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if strings.TrimSpace(string(sc.Bytes())) != "" {
			return true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, err
	}
	return false, nil
}
