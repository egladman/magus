package cache

import (
	"log/slog"
	"time"
)

// LogScope emits a [scope] header through the cache logger so all
// output formats (pretty/text/JSON) receive the same event.
func (c *Cache) LogScope(label, source string) {
	c.log.Info(
		"cache.scope",
		slog.String("label", label),
		slog.String("source", source),
	)
}

// LogStage emits a per-stage progress event for one magus.needs sub-target that ran
// under a project, routed through the cache logger like the other events. In collapse
// mode (where a project's subprocess output is withheld) these lines give the reader a
// checklist of what ran and whether it passed; runErr is nil on success.
func (c *Cache) LogStage(label, target string, elapsed time.Duration, runErr error) {
	attrs := []any{
		slog.String("label", label),
		slog.String("target", target),
		slog.Int64("duration", int64(elapsed)),
	}
	if runErr != nil {
		attrs = append(attrs, slog.String("error", runErr.Error()))
	}
	c.log.Info("cache.stage", attrs...)
}

// Collapsing reports whether the cache is withholding per-project subprocess output
// until failure (collapse-on-success). Callers use it to decide whether to attach a
// stage observer that prints progress lines for the otherwise-hidden work.
func (c *Cache) Collapsing() bool { return c.collapse }

// LogDryBanner emits the one-time dry-run banner through the cache logger.
func (c *Cache) LogDryBanner() {
	c.log.Info("cache.dry.banner")
}

// LogDry emits a per-target dry-run line ("[dry] <label> <target>") through the cache
// logger. Used by the dry-run path in place of the executed pass/fail lines.
func (c *Cache) LogDry(label, target string) {
	c.log.Info("cache.dry", slog.String("label", label), slog.String("target", target))
}

// LogSummary emits an end-of-run [summary] footer through the cache logger, drawn
// from the cache's own hit/miss/error counters. Like LogScope it routes through the
// logger so every output format receives the same event.
func (c *Cache) LogSummary(elapsed time.Duration) {
	s := c.Stats()
	c.log.Info(
		"cache.summary",
		slog.Int("hits", s.Hit),
		slog.Int("misses", s.Miss),
		slog.Int("errors", s.Error),
		slog.Int64("elapsed", int64(elapsed)),
	)
}
