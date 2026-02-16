package cli

// boundedBuffer captures up to maxBytes in memory while always reporting success
// to callers so pipes keep draining.
type boundedBuffer struct {
	maxBytes  int64
	buf       []byte
	truncated bool
}

func newBoundedBuffer(maxBytes int64) *boundedBuffer {
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &boundedBuffer{maxBytes: maxBytes}
}

func (bb *boundedBuffer) Write(p []byte) (int, error) {
	if bb.maxBytes <= 0 {
		bb.truncated = true
		return len(p), nil
	}
	remaining := bb.maxBytes - int64(len(bb.buf))
	if remaining <= 0 {
		bb.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		bb.buf = append(bb.buf, p[:remaining]...)
		bb.truncated = true
		return len(p), nil
	}
	bb.buf = append(bb.buf, p...)
	return len(p), nil
}

func (bb *boundedBuffer) Bytes() []byte {
	if len(bb.buf) == 0 {
		return nil
	}
	out := make([]byte, len(bb.buf))
	copy(out, bb.buf)
	return out
}

func (bb *boundedBuffer) Truncated() bool { return bb.truncated }
