package bindings

import (
	"context"
	"log/slog"
	"os"

	"github.com/egladman/magus/internal/interactive"
)

// emitMagusHint prints msg through the shared hint channel, honoring the user
// hints preference. Advisory only, never fatal. No dedup — it would mean
// process-global state that leaks across runs in the daemon.
func emitMagusHint(msg string) {
	if !interactive.HintsEnabled() {
		return
	}
	interactive.Emit(os.Stderr, msg)
}

// emitMagusLog writes msg at level into the process logger with optional fields.
// Shared by the Buzz magus.<level> trampolines.
func emitMagusLog(ctx context.Context, level slog.Level, msg string, fields map[string]string) {
	if len(fields) == 0 {
		slog.Default().Log(ctx, level, msg)
		return
	}
	attrs := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		attrs = append(attrs, k, v)
	}
	slog.Default().Log(ctx, level, msg, attrs...)
}

// dedupStrings returns names with duplicates removed, preserving first-occurrence
// order. The needs dispatch path uses it so a target listed twice (or listed
// manually and matched by a ctx.needs(ctx.glob(...)) pattern in the same dispatch) runs
// once, not twice. Names are already lowercased by the callers, so the dedup is
// case-insensitive.
func dedupStrings(names []string) []string {
	if len(names) < 2 {
		return names
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}
