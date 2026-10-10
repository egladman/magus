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

// Wrap returns h filtered for a quiet display. It drops [attr.Why] attributes, whether on
// the record or added through WithAttrs, unless keepWhy, and drops any record whose
// [attr.Elapsed] is under a minute. Everything else reaches h unchanged, and Enabled is
// h's.
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
	return h.next.Enabled(ctx, l)
}

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	drop, hasWhy := false, false
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case attr.ElapsedKey:
			if a.Value.Kind() == slog.KindDuration && a.Value.Duration() < waitFloor {
				drop = true
				return false
			}
		case attr.WhyKey:
			hasWhy = true
		}
		return true
	})
	if drop {
		return nil
	}
	if !hasWhy || h.keepWhy {
		return h.next.Handle(ctx, r)
	}
	kept := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != attr.WhyKey {
			kept.AddAttrs(a)
		}
		return true
	})
	return h.next.Handle(ctx, kept)
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
