package schema

// Artifact schema versions are per-artifact on purpose, even if they happen to
// be the same number today. This lets us evolve (for example) attempt.report.json
// without forcing a breaking change to run.json/attempt.json/feedback.json.
const (
	RunSchemaV1           = 1
	AttemptSchemaV1       = 1
	FeedbackSchemaV1      = 1
	AttemptReportSchemaV1 = 1
)
