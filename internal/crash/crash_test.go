package crash

import (
	"io"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })
	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func resetForTest(t *testing.T) {
	t.Helper()
	old := hint.Load()
	hint.Store(nil)
	printed = sync.Once{}
	t.Cleanup(func() {
		hint.Store(old)
		printed = sync.Once{}
	})
}

// Report prints the hint and re-panics with the original value, so the process still
// crashes with Go's trace for the real panic.
func TestReportPrintsHintAndRepanics(t *testing.T) {
	resetForTest(t)
	SetHint(func() string { return "hint\n" })

	var repanicked any
	out := captureStderr(t, func() {
		func() {
			defer func() { repanicked = recover() }()
			func() {
				defer Report()
				panic("boom")
			}()
		}()
	})
	assert.Equal(t, "hint\n", out)
	assert.Equal(t, "boom", repanicked)
}

// Two goroutines panicking together would otherwise print the hint twice ahead of one
// trace.
func TestReportPrintsOnce(t *testing.T) {
	resetForTest(t)
	SetHint(func() string { return "hint\n" })

	out := captureStderr(t, func() {
		for range 2 {
			func() {
				defer func() { _ = recover() }()
				func() {
					defer Report()
					panic("boom")
				}()
			}()
		}
	})
	assert.Equal(t, "hint\n", out)
}

func TestReportWithoutPanicIsSilent(t *testing.T) {
	resetForTest(t)
	SetHint(func() string { return "hint\n" })

	out := captureStderr(t, func() {
		func() { defer Report() }()
	})
	assert.Empty(t, out)
}

// Before main installs a hint there is nothing to say, and the panic must still
// propagate.
func TestReportWithoutHintRepanics(t *testing.T) {
	resetForTest(t)

	var repanicked any
	out := captureStderr(t, func() {
		func() {
			defer func() { repanicked = recover() }()
			func() {
				defer Report()
				panic("boom")
			}()
		}()
	})
	assert.Empty(t, out)
	assert.Equal(t, "boom", repanicked)
}

func TestGoRunsFn(t *testing.T) {
	done := make(chan struct{})
	Go(func() { close(done) })
	<-done
}
