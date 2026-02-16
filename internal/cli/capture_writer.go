package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
)

// boundedHashWriter writes up to maxBytes to the underlying writer, while
// continuing to report success to callers so pipes keep draining.
// It computes sha256 over the bytes actually written.
type boundedHashWriter struct {
	w         io.Writer
	maxBytes  int64
	written   int64
	truncated bool
	h         hash.Hash
}

func newBoundedHashWriter(w io.Writer, maxBytes int64) *boundedHashWriter {
	return &boundedHashWriter{
		w:        w,
		maxBytes: maxBytes,
		h:        sha256.New(),
	}
}

func (bw *boundedHashWriter) Write(p []byte) (int, error) {
	if bw.maxBytes <= 0 {
		// Hard-bounded: write nothing, but still drain.
		bw.truncated = true
		return len(p), nil
	}

	remaining := bw.maxBytes - bw.written
	if remaining <= 0 {
		bw.truncated = true
		return len(p), nil
	}

	toWrite := p
	if int64(len(toWrite)) > remaining {
		toWrite = toWrite[:remaining]
		bw.truncated = true
	}

	if len(toWrite) > 0 {
		// Best-effort: ignore underlying errors to avoid deadlocks.
		n, _ := bw.w.Write(toWrite)
		bw.written += int64(n)
		_, _ = bw.h.Write(toWrite[:n])
		if n < len(toWrite) {
			bw.truncated = true
		}
	}

	return len(p), nil
}

func (bw *boundedHashWriter) SumHex() string {
	return hex.EncodeToString(bw.h.Sum(nil))
}

func (bw *boundedHashWriter) WrittenBytes() int64 { return bw.written }

func (bw *boundedHashWriter) Truncated() bool { return bw.truncated }
