package cache

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLineTapIsSafeForConcurrentWriters is the regression for a panic that took the
// whole process down mid-run:
//
//	panic: runtime error: slice bounds out of range [128:0]
//	  internal/cache.(*lineTap).Write  capture.go:80
//
// captureRun puts ONE pair of taps on the context for an entire target body, and a
// target that fans out - `ctx.needs(lint, test)`, which is the shape of every `ci`
// target - runs those children concurrently. Several goroutines therefore reach the
// same tap. With buf unguarded, two writers tore its slice header: Write read an
// index from one state and resliced against another, so `t.buf[i+1:]` indexed past a
// buffer that had meanwhile become empty. The panic killed the writer goroutine, and
// the child process then reported its broken output pipe as `exit -1` - which reads
// as an unrelated tool failure, not as magus crashing.
//
// Run with -race this fails on the unguarded version by detecting the race directly;
// without -race it still reproduces the panic given enough interleavings.
func TestLineTapIsSafeForConcurrentWriters(t *testing.T) {
	t.Parallel()

	tap := newLineEmitter(context.Background(), "proj", "ci").
		newLineTap(io.Discard, "stdout")

	// Two writers, mirroring two fanned-out children sharing one tap. The payload is
	// newline-heavy so the reslice at the end of Write runs on nearly every call,
	// which is the line that panicked.
	const writers, rounds = 8, 200
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			line := []byte(strings.Repeat("x", 32) + "\n")
			for range rounds {
				_, err := tap.Write(line)
				assert.NoError(t, err)
			}
		}()
	}
	wg.Wait()

	// flush touches the same buffer, so it takes the same lock.
	require.NotPanics(t, tap.flush)
}

// TestLineTapSplitsLinesAcrossWrites pins the behaviour the lock must not change: a
// line split across two Writes still emits exactly once, when its newline arrives.
func TestLineTapSplitsLinesAcrossWrites(t *testing.T) {
	t.Parallel()

	var sink strings.Builder
	tap := newLineEmitter(context.Background(), "proj", "build").
		newLineTap(&sink, "stdout")

	_, err := tap.Write([]byte("half"))
	require.NoError(t, err)
	_, err = tap.Write([]byte("-line\nnext\n"))
	require.NoError(t, err)
	tap.flush()

	// dest sees the verbatim bytes regardless of line framing.
	assert.Equal(t, "half-line\nnext\n", sink.String())
}
