// Package quiet filters a log display for -q and -s: a record keeps its facts and
// loses its reasoning and its short-wait notes.
package quiet

import (
	"context"
	"log/slog"
	"time"

	"github.com/egladman/magus/internal/log/attr"
)

// waitFloor is the shortest wait a quiet display reports. A shorter one is over before
// the reader could act on it.
const waitFloor = time.Minute

// Wrap returns h filtered for a quiet display. Below a warning, or below h's level, only
// an [attr.Hint], a wait h takes, and an info record carrying [attr.Next] pass, the last
// as its command alone. A wait ([attr.Elapsed]) under a minute is dropped, and every
// [attr.Why] is dropped unless keepWhy.
//
// Wrap a display, never a capture: a run log keeps both the reasoning and the short
// waits for whoever reads it later.
func Wrap(h slog.Handler, keepWhy bool) slog.Handler {
	return handler{next: h, keepWhy: keepWhy}
}

type handler struct {
	next    slog.Handler
	keepWhy bool
}

func (h handler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelInfo || h.next.Enabled(ctx, l)
}

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	var next, notice string
	waits, wait := false, time.Duration(0)
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case attr.ElapsedKey:
			if a.Value.Kind() == slog.KindDuration {
				waits, wait = true, a.Value.Duration()
			}
		case attr.NextKey:
			next = a.Value.String()
		case attr.NoticeKey:
			notice = a.Value.String()
		}
		return true
	})
	if waits && wait < waitFloor {
		return nil
	}
	if r.Level < slog.LevelWarn || !h.next.Enabled(ctx, r.Level) {
		switch {
		case notice == attr.HintLabel, waits && h.next.Enabled(ctx, r.Level):
		case next != "" && r.Level < slog.LevelWarn:
			r = commandOnly(r, next)
		default:
			return nil
		}
	}
	return h.next.Handle(ctx, h.withoutWhy(r))
}

// withoutWhy is r with its [attr.Why] dropped, unless the display keeps it.
func (h handler) withoutWhy(r slog.Record) slog.Record {
	if h.keepWhy {
		return r
	}
	kept := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != attr.WhyKey {
			kept.AddAttrs(a)
		}
		return true
	})
	return kept
}

// commandOnly is r reduced to its next command, keeping only the attributes that label
// the line.
func commandOnly(r slog.Record, cmd string) slog.Record {
	kept := slog.NewRecord(r.Time, r.Level, cmd, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case attr.ComponentKey, attr.NoticeKey:
			kept.AddAttrs(a)
		}
		return true
	})
	return kept
}

func (h handler) WithAttrs(as []slog.Attr) slog.Handler {
	if !h.keepWhy {
		kept := make([]slog.Attr, 0, len(as))
		for _, a := range as {
			if a.Key != attr.WhyKey {
				kept = append(kept, a)
			}
		}
		as = kept
	}
	return handler{next: h.next.WithAttrs(as), keepWhy: h.keepWhy}
}

func (h handler) WithGroup(name string) slog.Handler {
	return handler{next: h.next.WithGroup(name), keepWhy: h.keepWhy}
}
