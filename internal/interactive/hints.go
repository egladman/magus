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

// emitted is the set of hint texts already shown, so a fact repeated across
// many targets/charms/calls in one run (or one long-lived server) teaches
// once instead of nagging on every occurrence.
var (
	emittedMu sync.Mutex
	emitted   = make(map[string]struct{})
)

// maxEmittedDedupe bounds the dedupe set. The server hosting Hint calls never
// restarts to clear it, so an unbounded map would be a slow leak; once full it
// resets and hints start teaching again rather than growing forever.
const maxEmittedDedupe = 4096

// Hint logs msg as an [attr.Hint] record when hints are enabled, once per distinct msg;
// see emitted. The display prints it "hint: <msg>", and keeps it under -q and -s.
func Hint(ctx context.Context, msg string, attrs ...slog.Attr) {
	h := slog.Default().Handler()
	if !h.Enabled(ctx, slog.LevelInfo) {
		return
	}
	if r, ok := TakeHint(slog.LevelInfo, msg, attrs...); ok {
		_ = h.Handle(ctx, r)
	}
}

// TakeHint spends msg's one showing and returns it as an [attr.Hint] record at level, for
// a caller that hands records to a handler of its own. It returns false when hints are off
// or msg was already shown.
func TakeHint(level slog.Level, msg string, attrs ...slog.Attr) (slog.Record, bool) {
	if !firstShowing(msg) {
		return slog.Record{}, false
	}
	r := slog.NewRecord(time.Now(), level, msg, 0)
	r.AddAttrs(attrs...)
	r.AddAttrs(attr.Hint())
	return r, true
}

// firstShowing reports whether msg is a hint to show now: hints are on and msg has not
// been shown before. It records the showing.
func firstShowing(msg string) bool {
	if !HintsEnabled() {
		return false
	}
	emittedMu.Lock()
	defer emittedMu.Unlock()
	if _, ok := emitted[msg]; ok {
		return false
	}
	if len(emitted) >= maxEmittedDedupe {
		emitted = make(map[string]struct{})
	}
	emitted[msg] = struct{}{}
	return true
}
