package cache

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/secret"
)

// lineEmitter taps a step's subprocess output as it is written. Each tapped writer
// redacts any resolved secret (see [lineTap.Write]) and otherwise passes bytes through
// unchanged, then splits them into lines and emits one structured output
// [journal.Event] per line - tagged with the owning project/target/stream - to the
// capture logger on ctx (the invocation journal + live stream). The per-ref output
// store keeps the raw bytes, NOT these events, so nothing is stored twice.
//
// Redaction is the ONLY thing that alters what reaches the terminal and the raw log,
// and it applies to both plus the events, because all three descend from the single
// Write below.
type lineEmitter struct {
	ctx     context.Context
	project string
	target  string
}

func newLineEmitter(ctx context.Context, project, target string) *lineEmitter {
	return &lineEmitter{ctx: ctx, project: project, target: target}
}

// newLineTap wraps dest with a line tap tagged as the given stream ("stdout"/"stderr").
func (c *lineEmitter) newLineTap(dest io.Writer, stream string) *lineTap {
	return &lineTap{c: c, dest: dest, stream: stream}
}

// syncWriter serializes concurrent writes to an underlying writer with a mutex. captureRun
// shares one across the stdout and stderr taps, which os/exec drives from separate goroutines,
// so lines in the durable log never interleave mid-write.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// emit builds one output event and emits it to the ctx capture logger (for the invocation
// journal / live stream). journal.Emit is concurrency-safe, so the two taps need no extra lock.
func (c *lineEmitter) emit(stream, text string) {
	journal.Emit(c.ctx, journal.Event{
		Ts:      time.Now().UnixMilli(),
		Project: c.project,
		Target:  c.target,
		Kind:    journal.KindOutput,
		Stream:  stream,
		Text:    text,
	})
}

// lineTap redacts resolved secrets from each write, passes the result to dest, then
// buffers and splits it into newline-terminated lines, emitting one event per complete
// line. A trailing partial line is emitted by flush() after the run finishes.
//
// Safe for concurrent use, which is a requirement rather than a courtesy: ONE pair of
// taps is put on the context for a whole target body (captureRun), and a target that
// fans out - `ctx.needs(lint, test)`, the shape every `ci` target has - runs those
// children concurrently, so several goroutines reach the same tap. Unguarded, two
// writers tore the buf slice header and magus PANICKED mid-run with
// "slice bounds out of range [128:0]" at the reslice below, killing the writer
// goroutine and leaving the child process reporting a broken pipe as `exit -1`.
// (The shared log sink beside this already had the same guard via syncWriter; the
// per-stream buffer here was missed.)
type lineTap struct {
	c      *lineEmitter
	dest   io.Writer
	stream string

	mu  sync.Mutex
	buf []byte
}

func (t *lineTap) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Redact any value read through magus\secret.read before ANYTHING sees it: dest is
	// the terminal and the raw log file, and t.buf becomes the emitted events. The
	// subprocess writers in run.Exec are wrapped too (secret.NewWriter); this covers the
	// taps themselves, which a target's own output also reaches.
	//
	// The write is reported as len(p) rather than the redacted length: the caller is
	// os/exec copying a pipe, and a short write makes it stop. Redaction changes how
	// many bytes we emit, never how many we accepted.
	scrubbed := secret.Redact(t.c.ctx, p)
	if _, err := t.dest.Write(scrubbed); err != nil {
		return 0, err
	}
	t.buf = append(t.buf, scrubbed...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		t.c.emit(t.stream, string(t.buf[:i]))
		t.buf = t.buf[i+1:]
	}
	return len(p), nil
}

// flush emits any buffered bytes not terminated by a newline (a final partial line).
func (t *lineTap) flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) > 0 {
		t.c.emit(t.stream, string(t.buf))
		t.buf = nil
	}
}
