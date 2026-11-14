package bindings

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/secret"
)

// emitMagusHint prints a script's msg through the shared hint channel. Advisory only,
// never fatal.
func emitMagusHint(ctx context.Context, msg string) {
	interactive.Hint(ctx, msg)
}

// emitMagusLog writes msg at level, fields sorted by key. A target's log goes
// to its captured stderr, kept under its ref, without a time, so reruns match.
// Elsewhere it goes to the process logger, whose level gates both.
func emitMagusLog(ctx context.Context, level slog.Level, msg string, fields map[string]string) {
	logger := slog.Default()
	if !logger.Enabled(ctx, level) {
		return
	}
	keys := slices.Sorted(maps.Keys(fields))
	attrs := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		attrs = append(attrs, k, fields[k])
	}
	if _, stderr, ok := spawn.CapturedOutput(ctx); ok {
		logger = slog.New(secret.NewRedactingHandler(slog.NewTextHandler(stderr, &slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: dropTime,
		})))
	}
	logger.Log(ctx, level, msg, attrs...)
}

func dropTime(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
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
