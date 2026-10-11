package testkit

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// CaptureStdout runs fn with os.Stdout redirected to a pipe and returns everything fn
// wrote to it.
//
// It swaps the os.Stdout variable, not file descriptor 1, so it sees what Go code
// writes through os.Stdout (fmt.Print, a command whose Stdout is os.Stdout) and not
// what a process already holding the descriptor writes. The swap is process-wide:
// never call it from a parallel test.
//
// The pipe is drained while fn runs, so output larger than the pipe buffer cannot
// block fn. os.Stdout is restored before CaptureStdout returns, including when fn ends
// the test with t.FailNow or runtime.Goexit.
func CaptureStdout(tb testing.TB, fn func()) string {
	tb.Helper()
	return capture(tb, &os.Stdout, fn)
}

// CaptureStderr is CaptureStdout for os.Stderr.
func CaptureStderr(tb testing.TB, fn func()) string {
	tb.Helper()
	return capture(tb, &os.Stderr, fn)
}

func capture(tb testing.TB, target **os.File, fn func()) (out string) {
	tb.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		tb.Fatalf("testkit: capture: %v", err)
	}
	var buf bytes.Buffer
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
		<-drained
		_ = r.Close()
		out = buf.String()
	}()
	fn()
	return ""
}
