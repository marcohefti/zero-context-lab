package schema

// v1 size limits used across writers + validators.
// Keep these in sync with SCHEMAS.md.
const (
	PreviewMaxBytesV1   = 16 * 1024
	ToolInputMaxBytesV1 = 64 * 1024

	FeedbackMaxBytesV1 = 64 * 1024

	NoteMessageMaxBytesV1 = 16 * 1024
	NoteDataMaxBytesV1    = 64 * 1024
)
