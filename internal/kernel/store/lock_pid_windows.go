//go:build windows

package store

// Windows liveness probing without extra platform dependencies is not reliable.
// Keep stale-lock behavior time-based on Windows.
func processAlive(pid int) bool {
	_ = pid
	return false
}
