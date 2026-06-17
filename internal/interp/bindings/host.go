package bindings

import (
	"context"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/egladman/magus/internal/interactive"
)

// emitMagusHint prints msg through the shared hint channel, honoring the global
// hints toggle. Advisory only, never fatal. No dedup — it would mean
// process-global state that leaks across runs in the daemon.
func emitMagusHint(msg string) {
	if !interactive.Enabled() {
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

// compileTargetPatterns turns target match patterns into anchored regexps,
// shared by the Buzz dispatch matchers. A pattern with no "*" is suffix
// shorthand ("build" → names ending in "-build"); a pattern with "*" is a glob
// ("*" → ".*"). Both forms are QuoteMeta'd first, so the result is always a valid
// regexp and MustCompile never panics.
func compileTargetPatterns(patterns []string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, 0, len(patterns))
	for _, pat := range patterns {
		if !strings.Contains(pat, "*") {
			res = append(res, regexp.MustCompile(`^.*-`+regexp.QuoteMeta(pat)+`$`))
			continue
		}
		res = append(res, regexp.MustCompile("^"+strings.ReplaceAll(regexp.QuoteMeta(pat), `\*`, `.*`)+"$"))
	}
	return res
}

// dedupStrings returns names with duplicates removed, preserving first-occurrence
// order. magus.needs uses it so a target listed manually *and* matched by a
// magus.target.expand_globs glob in the same list runs once, not twice. Names are
// already lowercased by the callers, so the dedup is case-insensitive.
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
