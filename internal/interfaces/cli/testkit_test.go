package cli

import (
	"bytes"
	"testing"
	"time"
)

type runnerHarness struct {
	Runner Runner
	Stdout *bytes.Buffer
	Stderr *bytes.Buffer
}

func newRunnerHarnessNowFunc(t *testing.T, now func() time.Time) runnerHarness {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	return runnerHarness{
		Runner: Runner{
			Version: "0.0.0-dev",
			Now:     now,
			Stdout:  &stdout,
			Stderr:  &stderr,
		},
		Stdout: &stdout,
		Stderr: &stderr,
	}
}

func newRunnerHarness(t *testing.T, now time.Time) runnerHarness {
	t.Helper()
	return newRunnerHarnessNowFunc(t, func() time.Time { return now })
}
