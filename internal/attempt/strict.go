package attempt

// EffectiveStrict returns whether an operation should behave in strict mode.
// CI attempts are strict by default to make enforcement hard to accidentally bypass.
func EffectiveStrict(attemptDir string, strictFlag bool) bool {
	if strictFlag {
		return true
	}
	a, err := ReadAttempt(attemptDir)
	if err != nil {
		return false
	}
	return a.Mode == "ci"
}
