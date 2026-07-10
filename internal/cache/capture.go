package cache

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"github.com/egladman/magus/internal/journal"
)

// lineEmitter taps a step's subprocess output as it is written, WITHOUT changing what
// reaches the terminal or the raw log file. Each tapped writer passes bytes through verbatim
// (so the live view and the raw logF are unaffected), then splits them into lines and emits one
// structured output [journal.Event] per line - tagged with the owning project/target/stream -
// to the capture logger on ctx (the invocation journal + live stream). The per-ref output store
// keeps the raw bytes verbatim, NOT these events, so nothing is stored twice.
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

// lineTap passes writes through to dest verbatim, then buffers and splits them into
// newline-terminated lines, emitting one event per complete line. A trailing partial
// line is emitted by flush() after the run finishes.
type lineTap struct {
	c      *lineEmitter
	dest   io.Writer
	stream string
	buf    []byte
}

func (t *lineTap) Write(p []byte) (int, error) {
	n, err := t.dest.Write(p) // verbatim passthrough first - terminal/logF unchanged
	t.buf = append(t.buf, p[:n]...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			break
		}
		t.c.emit(t.stream, string(t.buf[:i]))
		t.buf = t.buf[i+1:]
	}
	return n, err
}

// flush emits any buffered bytes not terminated by a newline (a final partial line).
func (t *lineTap) flush() {
	if len(t.buf) > 0 {
		t.c.emit(t.stream, string(t.buf))
		t.buf = nil
	}
}
