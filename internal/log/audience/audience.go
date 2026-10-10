// Package audience filters a log display for who reads it.
package audience

import (
	"context"
	"log/slog"
	"time"

	"github.com/egladman/magus/internal/log/attr"
)

// Audience is who reads a display: a person at a terminal or a program that pays for
// every line it reads. One record serves both; the audience decides which parts of it
// are shown.
type Audience string

const (
	// Human sees every record and every attribute.
	Human Audience = "human"
	// Agent sees the facts of a record without its reasoning, and no progress note
	// for a wait too short to matter.
	Agent Audience = "agent"
)

// agentWaitFloor is the shortest wait an [Agent] display reports. A shorter one is
// over before the reader could act on it.
const agentWaitFloor = time.Minute

// Wrap returns h filtered for a. For [Human] it returns h itself. For [Agent] it drops
// [attr.Why] attributes, whether on the record or added through WithAttrs, unless
// verbose, and drops any record whose [attr.Elapsed] is under a minute. Everything else
// reaches h unchanged, and Enabled is h's.
//
// Wrap a display, never a capture: a run log keeps both the reasoning and the short
// waits for whoever reads it later.
func Wrap(h slog.Handler, a Audience, verbose bool) slog.Handler {
	if a != Agent {
		return h
	}
	return agentHandler{next: h, keepWhy: verbose}
}

type agentHandler struct {
	next    slog.Handler
	keepWhy bool
}

func (h agentHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h agentHandler) Handle(ctx context.Context, r slog.Record) error {
	drop, hasWhy := false, false
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case attr.ElapsedKey:
			if a.Value.Kind() == slog.KindDuration && a.Value.Duration() < agentWaitFloor {
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

func (h agentHandler) WithAttrs(as []slog.Attr) slog.Handler {
	if !h.keepWhy {
		kept := make([]slog.Attr, 0, len(as))
		for _, a := range as {
			if a.Key != attr.WhyKey {
				kept = append(kept, a)
			}
		}
		as = kept
	}
	return agentHandler{next: h.next.WithAttrs(as), keepWhy: h.keepWhy}
}

func (h agentHandler) WithGroup(name string) slog.Handler {
	return agentHandler{next: h.next.WithGroup(name), keepWhy: h.keepWhy}
}
