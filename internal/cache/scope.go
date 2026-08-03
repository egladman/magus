package cache

import (
	"context"
	"log/slog"
	"time"
)

// LogScope emits the projects header through the cache logger so all
// output formats (pretty/text/JSON) receive the same event.
func (c *Cache) LogScope(ctx context.Context, label, source string) {
	c.log.InfoContext(ctx,
		"cache.scope",
		slog.String("label", label),
		slog.String("source", source),
	)
}

// LogCharms emits the active-charm header (the charms mixed into this run, e.g. the
// magus.yaml default_charms like `rw`) so the reader sees up front what state the run
// executes under - and can tell at a glance whether a default charm actually took
// effect. Routed through the cache logger like LogScope so every format receives it.
func (c *Cache) LogCharms(ctx context.Context, charms string) {
	c.log.InfoContext(ctx, "cache.charms", slog.String("charms", charms))
}

// LogStage emits a per-stage progress event for one magus.needs sub-target that ran
// under a project, routed through the cache logger like the other events. In collapse
// mode (where a project's subprocess output is withheld) these lines give the reader a
// checklist of what ran and whether it passed; runErr is nil on success.
func (c *Cache) LogStage(ctx context.Context, label, target string, elapsed time.Duration, runErr error) {
	attrs := []any{
		slog.String("label", label),
		slog.String("target", target),
		slog.Int64("duration", int64(elapsed)),
	}
	if runErr != nil {
		attrs = append(attrs, slog.String("error", runErr.Error()))
	}
	c.log.InfoContext(ctx, "cache.stage", attrs...)
}

// Collapsing reports whether the cache is withholding per-project subprocess output
// until failure (collapse-on-success). Callers use it to decide whether to attach a
// stage observer that prints progress lines for the otherwise-hidden work.
func (c *Cache) Collapsing() bool { return c.collapse }

// LogDryBanner emits the one-time dry-run banner through the cache logger.
func (c *Cache) LogDryBanner(ctx context.Context) {
	c.log.InfoContext(ctx, "cache.dry.banner")
}

// LogDry emits a per-target dry-run line through the cache logger, in place of the
// executed pass/fail line. project is the workspace-relative path, carried so the
// line can print the same repro command an executed one does.
func (c *Cache) LogDry(ctx context.Context, project, label, target string) {
	c.log.InfoContext(ctx, "cache.dry",
		slog.String("project", project),
		slog.String("label", label),
		slog.String("target", target))
}

// LogDrySummary emits the end-of-run footer for a dry run: the same cache.summary
// event a real run ends with, marked dry and carrying what WOULD have run.
//
// It is the same event on purpose. A dry run previously just stopped after its last
// [dry] line, so the one shape a reader had learned to look for at the bottom of a
// run - the summary - was missing exactly when they were checking a plan. Reusing
// cache.summary also means every output format (json, jsonl, template) keeps
// reporting a footer rather than only the text renderer growing one.
//
// The cache's own counters are not consulted: nothing executed, so they are all
// zero and would report "0 ran" for a plan that intends to run plenty.
func (c *Cache) LogDrySummary(ctx context.Context, planned int, elapsed time.Duration) {
	c.log.InfoContext(ctx,
		"cache.summary",
		slog.Bool("dry", true),
		slog.Int("planned", planned),
		slog.Int64("elapsed", int64(elapsed)),
	)
}

// LogSummary emits an end-of-run [summary] footer through the cache logger, drawn
// from the cache's own hit/miss/error counters. Like LogScope it routes through the
// logger so every output format receives the same event.
func (c *Cache) LogSummary(ctx context.Context, elapsed time.Duration) {
	s := c.Stats()
	c.log.InfoContext(ctx,
		"cache.summary",
		slog.Int("hits", s.Hit),
		slog.Int("misses", s.Miss),
		slog.Int("errors", s.Error),
		slog.Int64("elapsed", int64(elapsed)),
	)
}
