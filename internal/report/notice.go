package report

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// NewNoticeHandler returns a slog.Handler that writes each record to w as a run.notice
// envelope, the shape every other line of a -o jsonl run has. It is the fallback for
// log records no typed event converts: a caller parsing the run meets one record shape
// on both streams instead of slog's {time,level,msg} beside {schema,type,...}.
//
// Writes are synchronous and serialized, one line per record. The record's attributes
// land under "attrs", so an attribute named like an envelope field cannot collide with it.
func NewNoticeHandler(w io.Writer, level slog.Leveler) slog.Handler {
	return &noticeHandler{out: &lockedWriter{w: bufio.NewWriter(w)}, level: level}
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
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
	n := Notice{Level: strings.ToLower(r.Level.String()), Message: r.Message}
	if len(attrs) > 0 {
		n.Attrs = attrs
	}
	h.out.mu.Lock()
	defer h.out.mu.Unlock()
	if err := (envelope{Type: TypeNotice, Body: n}).writeJSONL(h.out.w); err != nil {
		return err
	}
	return h.out.w.Flush()
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
