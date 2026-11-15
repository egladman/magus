package bindings

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/secret"
)

// emitMagusHint prints a script's msg through the shared hint channel. Advisory only,
// never fatal.
func emitMagusHint(ctx context.Context, msg string) {
	interactive.Hint(ctx, msg)
}

// emitMagusLog writes msg at level, fields sorted by key. A target step
// captures it at every level; the process logger shows it at its own. A
// record the capture refused carries a "capture" attr saying why.
func emitMagusLog(ctx context.Context, level slog.Level, msg string, fields map[string]string) {
	keys := slices.Sorted(maps.Keys(fields))
	attrs := make([]slog.Attr, 0, len(keys)+1)
	for _, k := range keys {
		// The redacting handler leaves keys alone because Go's are constants.
		// These come from a script, so one can hold a secret.
		attrs = append(attrs, slog.String(secret.RedactString(ctx, k), fields[k]))
	}
	r := slog.NewRecord(time.Now(), level, msg, 0)
	r.AddAttrs(attrs...)
	if _, err := cache.LogToStep(ctx, r); err != nil {
		attrs = append(attrs, slog.String("capture", err.Error()))
	}
	slog.Default().LogAttrs(ctx, level, msg, attrs...)
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
