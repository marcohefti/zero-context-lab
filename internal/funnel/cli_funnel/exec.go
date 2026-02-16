package clifunnel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Result struct {
	ExitCode   int
	DurationMs int64

	OutBytes   int64
	ErrBytes   int64
	OutPreview string
	ErrPreview string

	OutTruncated bool
	ErrTruncated bool
}

type boundedCapture struct {
	max int
	mu  sync.Mutex
	buf bytes.Buffer

	total     int64
	truncated bool
}

func (c *boundedCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.total += int64(len(p))

	remaining := c.max - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}

	if len(p) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}

	_, _ = c.buf.Write(p)
	return len(p), nil
}

func (c *boundedCapture) snapshot() (preview string, bytesTotal int64, truncated bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String(), c.total, c.truncated
}

func Run(ctx context.Context, argv []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, outFull io.Writer, errFull io.Writer, maxPreviewBytes int) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("missing command argv")
	}
	if maxPreviewBytes < 0 {
		maxPreviewBytes = 0
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin == nil {
		cmd.Stdin = os.Stdin
	} else {
		cmd.Stdin = stdin
	}

	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	errPipe, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, err
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}

	var outCap boundedCapture
	outCap.max = maxPreviewBytes
	var errCap boundedCapture
	errCap.max = maxPreviewBytes

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(multiWriter(stdout, outFull, &outCap), outPipe)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(multiWriter(stderr, errFull, &errCap), errPipe)
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	exitCode := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			return Result{}, waitErr
		}
	}

	outPreview, outBytes, outTrunc := outCap.snapshot()
	errPreview, errBytes, errTrunc := errCap.snapshot()

	return Result{
		ExitCode:     exitCode,
		DurationMs:   time.Since(start).Milliseconds(),
		OutBytes:     outBytes,
		ErrBytes:     errBytes,
		OutPreview:   outPreview,
		ErrPreview:   errPreview,
		OutTruncated: outTrunc,
		ErrTruncated: errTrunc,
	}, nil
}

func multiWriter(a io.Writer, b io.Writer, c io.Writer) io.Writer {
	if b == nil {
		return io.MultiWriter(a, c)
	}
	return io.MultiWriter(a, b, c)
}
