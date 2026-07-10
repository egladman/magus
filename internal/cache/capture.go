package cache

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"github.com/egladman/magus/internal/journal"
)

// outputCollector taps a step's subprocess output as it is written, WITHOUT changing
// what reaches the terminal or the raw log file. Each tapped writer passes bytes
// through verbatim (so the live view and the raw logF are unaffected), then splits
// them into lines and emits one structured output [journal.Event] per line - tagged
// with the owning project/target/stream. Each event is both collected (for the
// per-target output store) and emitted to the capture logger on ctx (for the
// invocation journal + live stream).
//
// stdout and stderr get separate taps (distinct line buffers) but share one event
// slice, guarded by a mutex since RunAll writes both concurrently.
type outputCollector struct {
	ctx     context.Context
	project string
	target  string

	mu     sync.Mutex
	events []journal.Event
}

func newOutputCollector(ctx context.Context, project, target string) *outputCollector {
	return &outputCollector{ctx: ctx, project: project, target: target}
}

// writer wraps dest with a line tap tagged as the given stream ("stdout"/"stderr").
func (c *outputCollector) writer(dest io.Writer, stream string) *lineTap {
	return &lineTap{c: c, dest: dest, stream: stream}
}

// emit builds one output event: appended to the collected slice (for the store) and
// emitted to the ctx capture logger (for the invocation journal / live stream).
func (c *outputCollector) emit(stream, text string) {
	ev := journal.Event{
		Ts:      time.Now().UnixMilli(),
		Project: c.project,
		Target:  c.target,
		Kind:    journal.KindOutput,
		Stream:  stream,
		Text:    text,
	}
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
	journal.Emit(c.ctx, ev)
}

// collected returns the output events gathered so far (a copy is not needed; callers
// use it after the run completes and no more writes occur).
func (c *outputCollector) collected() []journal.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.events
}

// lineTap passes writes through to dest verbatim, then buffers and splits them into
// newline-terminated lines, emitting one event per complete line. A trailing partial
// line is emitted by flush() after the run finishes.
type lineTap struct {
	c      *outputCollector
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
