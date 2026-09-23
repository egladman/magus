package report

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// NewNoticeHandler returns a slog.Handler that writes each record to w as a run.notice
// envelope, the shape every other line of a -o jsonl run has. It is the fallback for
// log records no typed event converts: a caller parsing the run meets one record shape
// on both streams instead of slog's {time,level,msg} beside {schema,type,...}.
//
// Each record is one synchronous Write of one line. The record's attributes land under
// "attrs", so an attribute named like an envelope field cannot collide with it. Handlers
// over the same w do not serialize with each other; use [NewStderrNoticeHandler] for
// stderr.
func NewNoticeHandler(w io.Writer, level slog.Leveler) slog.Handler {
	return &noticeHandler{out: &lockedWriter{w: w}, level: level}
}

// NewStderrNoticeHandler is [NewNoticeHandler] over os.Stderr, sharing one lock with
// every other handler it returns, so records from several loggers never interleave.
func NewStderrNoticeHandler(level slog.Leveler) slog.Handler {
	return &noticeHandler{out: stderr, level: level}
}

// stderr is the one writer every stderr notice handler in the process shares.
var stderr = &lockedWriter{w: os.Stderr}

// lockedWriter serializes whole lines onto w.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) writeLine(line []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.w.Write(line)
	return err
}

type noticeHandler struct {
	out   *lockedWriter
	level slog.Leveler
	attrs []slog.Attr // from WithAttrs, already nested under their groups
	group []string    // open groups, applied to the record's own attributes
}

func (h *noticeHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *noticeHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := map[string]any{}
	for _, a := range h.attrs {
		putAttr(attrs, a)
	}
	var own []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		own = append(own, a)
		return true
	})
	for _, a := range nestUnder(h.group, own) {
		putAttr(attrs, a)
	}
	n := Notice{Level: r.Level, Message: r.Message}
	if len(attrs) > 0 {
		n.Attrs = attrs
	}
	return h.out.writeLine(envelope{Type: TypeNotice, Body: n}.appendJSONL(nil))
}

func (h *noticeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	if len(as) == 0 {
		return h
	}
	c := *h
	c.attrs = append(append([]slog.Attr(nil), h.attrs...), nestUnder(h.group, as)...)
	return &c
}

func (h *noticeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.group = append(append([]string(nil), h.group...), name)
	return &c
}

// NewLineNotices returns a writer that hands each line written to it to h as an info
// record whose message is the line, with a "stream" attribute naming where it was
// written. It skips h's level check: a line of output is not a log message a level may
// filter out. ctx is what each record redacts against.
//
// A trailing line with no newline is held until one arrives; one longer than
// maxNoticeLine is handed over in pieces.
func NewLineNotices(ctx context.Context, h slog.Handler, stream string) io.Writer {
	return &lineNotices{ctx: ctx, h: h, stream: stream}
}

// maxNoticeLine bounds what a lineNotices holds waiting for a newline.
const maxNoticeLine = 64 << 10

type lineNotices struct {
	ctx    context.Context
	h      slog.Handler
	stream string

	mu      sync.Mutex
	pending []byte
}

func (w *lineNotices) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.pending = append(w.pending, p...)
			if len(w.pending) < maxNoticeLine {
				return n, nil
			}
			p = nil
		} else {
			w.pending = append(w.pending, p[:i]...)
			p = p[i+1:]
		}
		r := slog.NewRecord(time.Now(), slog.LevelInfo, string(w.pending), 0)
		r.AddAttrs(slog.String("stream", w.stream))
		w.pending = w.pending[:0]
		if err := w.h.Handle(w.ctx, r); err != nil {
			return n, err
		}
	}
	return n, nil
}

// nestUnder wraps as in the open groups, innermost last, so a grouped attribute lands at
// its path under "attrs".
func nestUnder(groups []string, as []slog.Attr) []slog.Attr {
	if len(groups) == 0 || len(as) == 0 {
		return as
	}
	for i := len(groups) - 1; i >= 0; i-- {
		as = []slog.Attr{{Key: groups[i], Value: slog.GroupValue(as...)}}
	}
	return as
}

// putAttr adds a to m, merging a group into any map already at its key and dropping an
// empty key, as slog's own handlers do.
func putAttr(m map[string]any, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() != slog.KindGroup {
		if a.Key != "" {
			m[a.Key] = attrValue(v)
		}
		return
	}
	target := m
	if a.Key != "" {
		sub, ok := m[a.Key].(map[string]any)
		if !ok {
			sub = map[string]any{}
			m[a.Key] = sub
		}
		target = sub
	}
	for _, g := range v.Group() {
		putAttr(target, g)
	}
}

// attrValue is v as the JSON encoder should see it: a duration in nanoseconds, as slog's
// JSON handler writes it, and an error as its message.
func attrValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return int64(v.Duration())
	case slog.KindTime:
		return v.Time().Format(time.RFC3339Nano)
	}
	if err, ok := v.Any().(error); ok {
		return err.Error()
	}
	return v.Any()
}
