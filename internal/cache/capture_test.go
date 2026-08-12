package cache

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCaptureRunFailsLoudlyWhenLogDirUnwritable verifies captureRun surfaces an
// error (and never calls fn) when the log directory cannot be created. Before
// the fix, this returned nil, fn(ctx): fn ran with no capture writers, no
// journal step tagging, and no failure-dump machinery, and the MkdirAll error
// itself was discarded.
func TestCaptureRunFailsLoudlyWhenLogDirUnwritable(t *testing.T) {
	c := &Cache{dir: t.TempDir(), log: slog.Default(), logLevel: slog.LevelInfo}

	// Put a plain FILE where the log directory needs to be created, so
	// MkdirAll(filepath.Dir(logPath)) fails with "not a directory".
	blockedDir := filepath.Join(c.dir, "logs", "svc-api")
	require.NoError(t, os.MkdirAll(filepath.Dir(blockedDir), 0o755))
	require.NoError(t, os.WriteFile(blockedDir, []byte("not a directory"), 0o644))

	lp := filepath.Join(blockedDir, "deadbeef.log")

	fnCalled := false
	_, err := c.captureRun(context.Background(), lp, "svc/api", "test", func(context.Context) error {
		fnCalled = true
		return nil
	})

	require.Error(t, err, "captureRun must report the MkdirAll failure instead of swallowing it")
	assert.False(t, fnCalled, "fn must not run without capture writers, journal tagging, or failure-dump machinery")
}

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

// TestLineTapRedactsResolvedSecrets pins the guarantee the secret provider exists to
// make: a value resolved through magus\secret.read never reaches dest. dest here
// stands in for the terminal AND the raw log file, which captureRun feeds from this
// one writer, so covering it covers the durable output store too.
//
// The resolver rides on the emitter ctx, so this needs no global cleanup.
func TestLineTapRedactsSecrets(t *testing.T) {
	ctx := secret.ContextWithResolver(t.Context(), secret.New())
	t.Setenv("MAGUS_TEST_CAPTURE_TOKEN", "ghp_do_not_log_me")
	_, err := secret.ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_CAPTURE_TOKEN")
	require.NoError(t, err)

	var sink strings.Builder
	tap := newLineEmitter(ctx, "proj", "publish").
		newLineTap(&sink, "stdout")

	// A child that echoes the credential it was handed - a debug dump, a curl trace.
	n, err := tap.Write([]byte("auth: password=ghp_do_not_log_me ok\n"))
	require.NoError(t, err)
	tap.flush()

	assert.Equal(t, "auth: password=*** ok\n", sink.String())
	assert.NotContains(t, sink.String(), "ghp_do_not_log_me")
	// The count reported is what was ACCEPTED, not what was emitted: os/exec is
	// copying a pipe and treats a short write as a failure to drain it.
	assert.Equal(t, len("auth: password=ghp_do_not_log_me ok\n"), n)
}
