package testkit

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureStdoutReturnsWhatFnPrinted(t *testing.T) {
	orig := os.Stdout
	got := CaptureStdout(t, func() { fmt.Print("hello\n") })

	assert.Equal(t, "hello\n", got)
	assert.Same(t, orig, os.Stdout)
}

func TestCaptureStderrReturnsWhatFnPrinted(t *testing.T) {
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

// TestCaptureStopsWaitingOnAChildThatKeepsThePipe: a child handed os.Stdout that outlives
// fn holds the write end open, so the pipe never reaches end of file.
func TestCaptureStopsWaitingOnAChildThatKeepsThePipe(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep on PATH")
	}
	old := drainGrace
	drainGrace = 100 * time.Millisecond
	t.Cleanup(func() { drainGrace = old })

	var child *exec.Cmd
	t.Cleanup(func() {
		if child != nil && child.Process != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	start := time.Now()
	got := CaptureStdout(t, func() {
		fmt.Print("before\n")
		child = exec.Command(sleep, "30")
		child.Stdout = os.Stdout
		require.NoError(t, child.Start())
	})

	assert.Equal(t, "before\n", got)
	assert.Less(t, time.Since(start), 10*time.Second)
}

// TestCaptureRefusesAParallelTest: the os.Stdout swap is process-wide, so a parallel test
// would capture its neighbours' output.
func TestCaptureRefusesAParallelTest(t *testing.T) {
	t.Run("parallel", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { CaptureStdout(t, func() {}) })
	})
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
