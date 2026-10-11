package testkit

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCaptureStdout(t *testing.T) {
	orig := os.Stdout
	got := CaptureStdout(t, func() { fmt.Print("hello\n") })

	assert.Equal(t, "hello\n", got)
	assert.Same(t, orig, os.Stdout)
}

func TestCaptureStderr(t *testing.T) {
	orig := os.Stderr
	got := CaptureStderr(t, func() { fmt.Fprint(os.Stderr, "oops") })

	assert.Equal(t, "oops", got)
	assert.Same(t, orig, os.Stderr)
}

// TestCaptureStdoutDrainsWhileFnRuns: a pipe holds 64 KiB on most systems, so a
// capture that reads only after fn returns blocks fn forever on larger output.
func TestCaptureStdoutDrainsWhileFnRuns(t *testing.T) {
	want := strings.Repeat("x", 1<<20)
	got := CaptureStdout(t, func() { fmt.Print(want) })
	assert.Len(t, got, len(want))
}

// TestCaptureRestoresWhenFnDoesNotReturn: t.FailNow inside fn is runtime.Goexit, and a
// failed require is the usual way a capture's fn ends early. Either way the next test
// must not inherit a swapped os.Stdout.
func TestCaptureRestoresWhenFnDoesNotReturn(t *testing.T) {
	orig := os.Stdout
	t.Run("goexit", func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			CaptureStdout(t, runtime.Goexit)
		}()
		<-done
		assert.Same(t, orig, os.Stdout)
	})
	t.Run("panic", func(t *testing.T) {
		assert.Panics(t, func() { CaptureStdout(t, func() { panic("fn") }) })
		assert.Same(t, orig, os.Stdout)
	})
}
