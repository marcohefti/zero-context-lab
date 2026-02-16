package schema

// v1 size limits used across writers + validators.
// Keep these in sync with SCHEMAS.md.
const (
	PreviewMaxBytesV1    = 16 * 1024
	ToolInputMaxBytesV1  = 64 * 1024
	EnrichmentMaxBytesV1 = 64 * 1024

	FeedbackMaxBytesV1 = 64 * 1024

	NoteMessageMaxBytesV1 = 16 * 1024
	NoteDataMaxBytesV1    = 64 * 1024

	// CaptureMaxBytesV1 is the default cap for `zcl run --capture`.
	// Large outputs should go to dedicated artifacts, but still bounded by default.
	CaptureMaxBytesV1 = 4 * 1024 * 1024

	// Redaction metadata bounds (avoid unbounded arrays in traces/captures).
	RedactionsAppliedMaxCountV1 = 64
	RedactionNameMaxBytesV1     = 64
)
