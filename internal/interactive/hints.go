package interactive

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/egladman/magus/internal/log/attr"
)

// hintsEnabled controls actionable hints; it is unrelated to TTY detection.
var hintsEnabled = true

// SetHintsEnabled sets whether actionable hints are emitted.
func SetHintsEnabled(on bool) {
	hintsEnabled = on
}

// HintsEnabled reports whether actionable hints are active.
func HintsEnabled() bool {
	return hintsEnabled
}

// shownHints is the set of hint texts one invocation has shown, so a fact
// repeated across its targets teaches once instead of nagging.
type shownHints struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

// maxShownHints bounds a set. A server's own context lives as long as the
// daemon, so once full the set resets rather than growing forever.
const maxShownHints = 4096

type shownHintsKey struct{}

// WithHints returns ctx carrying a fresh set of shown hints. Each entry point
// calls it once per invocation, so a server shows a hint once per command it
// serves, not once per daemon. Without a set, every hint is shown each time.
func WithHints(ctx context.Context) context.Context {
	return context.WithValue(ctx, shownHintsKey{}, &shownHints{seen: map[string]struct{}{}})
}

// Hint logs msg as an [attr.Hint] record when hints are enabled, once per
// distinct msg under ctx's [WithHints] set. The display prints it
// "hint: <msg>", and keeps it under -q and -s.
func Hint(ctx context.Context, msg string, attrs ...slog.Attr) {
	h := slog.Default().Handler()
	if !h.Enabled(ctx, slog.LevelInfo) {
		return
	}
	if r, ok := TakeHint(ctx, slog.LevelInfo, msg, attrs...); ok {
		_ = h.Handle(ctx, r)
	}
}

// TakeHint spends msg's one showing under ctx and returns it as an
// [attr.Hint] record at level, for a caller that hands records to a handler
// of its own. It returns false when hints are off or msg was already shown.
func TakeHint(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) (slog.Record, bool) {
	if !firstShowing(ctx, msg) {
		return slog.Record{}, false
	}
	r := slog.NewRecord(time.Now(), level, msg, 0)
	r.AddAttrs(attrs...)
	r.AddAttrs(attr.Hint())
	return r, true
}

// firstShowing reports whether msg is a hint to show now, and records the
// showing in ctx's set.
func firstShowing(ctx context.Context, msg string) bool {
	if !HintsEnabled() {
		return false
	}
	s, ok := ctx.Value(shownHintsKey{}).(*shownHints)
	if !ok {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[msg]; ok {
		return false
	}
	if len(s.seen) >= maxShownHints {
		s.seen = map[string]struct{}{}
	}
	s.seen[msg] = struct{}{}
	return true
}
