package report

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/marcohefti/zero-context-lab/internal/schema"
	"github.com/marcohefti/zero-context-lab/internal/store"
)

type CliError struct {
	Code    string
	Message string
}

func (e *CliError) Error() string { return e.Message }

func BuildAttemptReport(now time.Time, attemptDir string, strict bool) (schema.AttemptReportJSONV1, error) {
	attemptPath := filepath.Join(attemptDir, "attempt.json")
	tracePath := filepath.Join(attemptDir, "tool.calls.jsonl")
	feedbackPath := filepath.Join(attemptDir, "feedback.json")

	attemptBytes, err := os.ReadFile(attemptPath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			return schema.AttemptReportJSONV1{}, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing attempt.json"}
		}
		return schema.AttemptReportJSONV1{}, err
	}
	var attempt schema.AttemptJSONV1
	if err := json.Unmarshal(attemptBytes, &attempt); err != nil {
		return schema.AttemptReportJSONV1{}, err
	}

	var okPtr *bool
	if fbBytes, err := os.ReadFile(feedbackPath); err == nil {
		var fb schema.FeedbackJSONV1
		if err := json.Unmarshal(fbBytes, &fb); err != nil {
			return schema.AttemptReportJSONV1{}, err
		}
		ok := fb.OK
		okPtr = &ok
	} else if strict && os.IsNotExist(err) {
		return schema.AttemptReportJSONV1{}, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing feedback.json"}
	} else if err != nil && !os.IsNotExist(err) {
		return schema.AttemptReportJSONV1{}, err
	}

	metrics, err := computeMetrics(tracePath, strict)
	if err != nil {
		return schema.AttemptReportJSONV1{}, err
	}

	return schema.AttemptReportJSONV1{
		SchemaVersion: schema.ArtifactSchemaV1,
		RunID:         attempt.RunID,
		SuiteID:       attempt.SuiteID,
		MissionID:     attempt.MissionID,
		AttemptID:     attempt.AttemptID,
		ComputedAt:    now.UTC().Format(time.RFC3339Nano),
		OK:            okPtr,
		Metrics:       metrics,
	}, nil
}

func WriteAttemptReportAtomic(path string, report schema.AttemptReportJSONV1) error {
	return store.WriteJSONAtomic(path, report)
}

func computeMetrics(tracePath string, strict bool) (schema.AttemptMetricsV1, error) {
	f, err := os.Open(tracePath)
	if err != nil {
		if strict && os.IsNotExist(err) {
			return schema.AttemptMetricsV1{}, &CliError{Code: "ZCL_E_MISSING_ARTIFACT", Message: "missing tool.calls.jsonl"}
		}
		if os.IsNotExist(err) {
			// Best-effort: missing trace yields zero metrics.
			return schema.AttemptMetricsV1{}, nil
		}
		return schema.AttemptMetricsV1{}, err
	}
	defer func() { _ = f.Close() }()

	var (
		sc      = bufio.NewScanner(f)
		metrics schema.AttemptMetricsV1
		minTS   time.Time
		maxTS   time.Time
	)
	metrics.FailuresByCode = map[string]int64{}

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev schema.TraceEventV1
		if err := json.Unmarshal(line, &ev); err != nil {
			return schema.AttemptMetricsV1{}, err
		}
		metrics.ToolCallsTotal++

		if !ev.Result.OK {
			metrics.FailuresTotal++
			code := ev.Result.Code
			if code == "" {
				code = "UNKNOWN"
			}
			metrics.FailuresByCode[code]++
			if code == "ZCL_E_TIMEOUT" {
				metrics.TimeoutsTotal++
			}
		}

		if ev.Integrity != nil && ev.Integrity.Truncated {
			// We only know "truncated happened"; attribute it to both streams for now.
			// Once we emit per-stream truncation, we can split precisely.
			if ev.IO.OutPreview != "" {
				metrics.OutPreviewTruncations++
			}
			if ev.IO.ErrPreview != "" {
				metrics.ErrPreviewTruncations++
			}
		}

		metrics.OutBytesTotal += ev.IO.OutBytes
		metrics.ErrBytesTotal += ev.IO.ErrBytes

		if ts, err := time.Parse(time.RFC3339Nano, ev.TS); err == nil {
			if minTS.IsZero() || ts.Before(minTS) {
				minTS = ts
			}
			if maxTS.IsZero() || ts.After(maxTS) {
				maxTS = ts
			}
		} else if strict {
			return schema.AttemptMetricsV1{}, err
		}
	}
	if err := sc.Err(); err != nil {
		return schema.AttemptMetricsV1{}, err
	}

	if metrics.ToolCallsTotal == 0 {
		if strict {
			return schema.AttemptMetricsV1{}, &CliError{Code: "ZCL_E_MISSING_EVIDENCE", Message: "tool.calls.jsonl is empty"}
		}
		return metrics, nil
	}

	if !minTS.IsZero() && !maxTS.IsZero() {
		metrics.WallTimeMs = maxTS.Sub(minTS).Milliseconds()
	}
	if len(metrics.FailuresByCode) == 0 {
		metrics.FailuresByCode = nil
	}
	return metrics, nil
}

func IsCliError(err error, code string) bool {
	var e *CliError
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
}
