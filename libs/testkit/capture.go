package testkit

import (
	"bytes"
	"io"
	"os"
	"sync"
	"time"
)

// drainGrace bounds how long capture waits, after fn returns, for the pipe to reach
// end of file. A variable so a test can shorten it.
var drainGrace = 2 * time.Second

// CaptureStdout runs fn with os.Stdout redirected to a pipe and returns everything fn
// wrote to it.
//
// It swaps the os.Stdout variable, not file descriptor 1, so it sees what Go code
// writes through os.Stdout (fmt.Print, a command whose Stdout is os.Stdout) and not
// what a process already holding the descriptor writes. The swap is process-wide, so
// it fails a test that is, or later becomes, parallel, the way tb.Setenv does.
//
// The pipe is drained while fn runs, so output larger than the pipe buffer cannot
// block fn. os.Stdout is restored before CaptureStdout returns, including when fn ends
// the test with t.FailNow or runtime.Goexit.
//
// A child process that was handed os.Stdout and outlives fn keeps the pipe open, so
// the capture stops waiting two seconds after fn returns and drops whatever the child
// writes later.
func CaptureStdout(tb TB, fn func()) string {
	tb.Helper()
	return capture(tb, &os.Stdout, fn)
}

// CaptureStderr is CaptureStdout for os.Stderr.
func CaptureStderr(tb TB, fn func()) string {
	tb.Helper()
	return capture(tb, &os.Stderr, fn)
}

func capture(tb TB, target **os.File, fn func()) (out string) {
	tb.Helper()
	// A throwaway key: Setenv's own refusal under t.Parallel is the check, and the value
	// is restored when the test ends.
	tb.Setenv("TESTKIT_CAPTURE_NOT_PARALLEL", "1")
	r, w, err := os.Pipe()
	if err != nil {
		tb.Fatalf("testkit: capture: %v", err)
	}
	var buf lockedBuffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&buf, r)
	}()

	orig := *target
	*target = w
	// Deferred rather than sequenced after fn: a Goexit in fn runs defers and nothing
	// else.
	defer func() {
		*target = orig
		_ = w.Close()
		select {
		case <-drained:
		case <-time.After(drainGrace):
			// Closing r ends the copy, but a blocked read on a pipe the platform cannot
			// interrupt would keep it running, so the buffer is read under its lock
			// instead of after the goroutine.
		}
		_ = r.Close()
		out = buf.String()
	}()
	fn()
	return ""
}

// lockedBuffer is a bytes.Buffer the drain goroutine can still be writing while the
// capture reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
