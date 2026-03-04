package cli

import (
	"sync"
	"sync/atomic"
)

// tailBuffer keeps the last maxBytes written to it.
// It always reports success to callers so pipes keep draining.
type tailBuffer struct {
	mu sync.Mutex

	maxBytes  int64
	buf       []byte
	truncated bool

	seq uint64
}

func newTailBuffer(maxBytes int64) *tailBuffer {
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &tailBuffer{maxBytes: maxBytes}
}

func (tb *tailBuffer) Write(p []byte) (int, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.maxBytes <= 0 {
		tb.truncated = true
		atomic.AddUint64(&tb.seq, 1)
		return len(p), nil
	}
	if int64(len(p)) >= tb.maxBytes {
		// Keep only the last maxBytes of p.
		tb.buf = append(tb.buf[:0], p[int64(len(p))-tb.maxBytes:]...)
		tb.truncated = true
		atomic.AddUint64(&tb.seq, 1)
		return len(p), nil
	}
	// Append and drop from the head if we exceed the max.
	tb.buf = append(tb.buf, p...)
	if int64(len(tb.buf)) > tb.maxBytes {
		over := int64(len(tb.buf)) - tb.maxBytes
		tb.buf = append(tb.buf[:0], tb.buf[over:]...)
		tb.truncated = true
	}
	atomic.AddUint64(&tb.seq, 1)
	return len(p), nil
}

func (tb *tailBuffer) Snapshot() (b []byte, truncated bool, seq uint64) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if len(tb.buf) == 0 {
		return nil, tb.truncated, atomic.LoadUint64(&tb.seq)
	}
	out := make([]byte, len(tb.buf))
	copy(out, tb.buf)
	return out, tb.truncated, atomic.LoadUint64(&tb.seq)
}

func (tb *tailBuffer) Seq() uint64 { return atomic.LoadUint64(&tb.seq) }
