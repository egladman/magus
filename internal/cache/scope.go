package cache

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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

// LogCache emits the cache header: which tiers this run can reach and whether it may
// write to them.
//
// Always printed, including the boring case, because the value is in never having to
// wonder. "Why was nothing cached" and "am I even talking to the remote" were previously
// answerable only by reading config and env, and the answer differs per event - a pull
// request reads the shared cache but must not publish to it, which is invisible unless
// something says so.
//
// It names the backend rather than probing it. Active() is a spell op on the real
// implementation - arbitrary Buzz, with the whole host surface - and calling it here put
// that on the path before the first line of output, where a slow probe stalls the run
// with nothing on screen to explain the pause. Presence and name are known without
// asking; whether the backend engages is the run's business, not the header's.
func (c *Cache) LogCache(ctx context.Context) {
	tier := "local"
	if c.remote != nil {
		name := c.remote.Name()
		if name == "" {
			name = "remote"
		}
		tier = name + " + local"
	}
	mode := "read-only"
	if c.mutable {
		mode = "read+write"
	}
	c.log.InfoContext(ctx, "cache.backend",
		slog.String("tier", tier), slog.String("mode", mode))
}

// LogBase emits what a run's affected set was compared against, beside the projects and
// charms headers, because it is the third input that decides what runs: the same command
// against a different base is a different build.
//
// base already reads "git diff vs origin/main" - the VCS name in front of the ref, rather
// than a git:// URI, because no such scheme is standard across git, Mercurial and jj and
// inventing one would put a magus-only string where a reader expects a ref they can paste
// straight back into their own VCS. vcs is accepted for a caller that has the two apart.
func (c *Cache) LogBase(ctx context.Context, base, vcs string) {
	if base == "" {
		return
	}
	c.log.InfoContext(ctx, "cache.base",
		slog.String("base", base), slog.String("vcs", vcs))
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

// MemoryPressure is what the run-time watchdog observed. Data, not a sentence:
// every other Log* method on Cache takes the facts and builds the record here, and
// a warning assembled across two packages has no single owner for its wording.
type MemoryPressure struct {
	AvailableBytes int64
	TotalBytes     int64
	// SwapUsedBytes and SwapGrowthBytes are the machine's swap and how much of it
	// this run added. Growth is the attributable half: a machine up for weeks
	// carries swap that predates the run, and reporting the level alone would
	// blame this run for it. Both 0 where the platform cannot report swap.
	SwapUsedBytes   int64
	SwapGrowthBytes int64
	// SwapTriggered reports that swap growth is why the watchdog spoke, as opposed to
	// falling headroom. See mem.Reading.
	SwapTriggered bool
	// BuzzObjects and BuzzPeak are the script VM's heap counts. Its heap never
	// frees, so a magusfile can consume the machine with no subprocess looking
	// guilty; 0 means the caller did not measure.
	BuzzObjects int
	BuzzPeak    int
	// BuzzHotSite is the source position responsible for the most heap growth,
	// as "source:line", or "" when nothing was sampled.
	BuzzHotSite string
}

// LogMemoryPressure warns that the host is running out of memory, routed through
// the cache logger like every other header.
//
// Warn rather than Info because a killed runner never lets magus reach its
// summary. Only what magus already streamed survives, and this is the line that
// explains an otherwise unattributable shutdown signal.
func (c *Cache) LogMemoryPressure(ctx context.Context, p MemoryPressure) {
	attrs := []any{
		slog.Int64("available_mb", p.AvailableBytes>>20),
		slog.Int64("total_mb", p.TotalBytes>>20),
	}
	msg := fmt.Sprintf("memory headroom low: %dMB available of %dMB total; a target here is close to taking the machine down",
		p.AvailableBytes>>20, p.TotalBytes>>20)

	// Swap leads when swap growth is what the watchdog fired on, because on darwin it
	// is the reading that moves: free, inactive and speculative pages do not fall
	// while the compressor and the swap file absorb the pressure, so headroom can
	// still look survivable on a machine that is already thrashing.
	//
	// Gated on the watchdog's own verdict rather than on the figure being non-zero. A
	// headroom warning routinely carries a few megabytes of growth, and switching on
	// that would replace the headroom sentence with one announcing 0MB paged out.
	if p.SwapTriggered {
		attrs = append(attrs,
			slog.Int64("swap_used_mb", p.SwapUsedBytes>>20),
			slog.Int64("swap_growth_mb", p.SwapGrowthBytes>>20))
		msg = fmt.Sprintf("this run has pushed %dMB into swap (%dMB used in total, %dMB of %dMB memory available); paging is what makes a machine stop responding rather than a target fail",
			p.SwapGrowthBytes>>20, p.SwapUsedBytes>>20, p.AvailableBytes>>20, p.TotalBytes>>20)
	}

	// Name what is running. The inflight registry already tracks this and already
	// survives a SIGKILL, so a warning that made the reader guess was withholding
	// an answer magus had on hand.
	if running := c.inflight.Running(); len(running) > 0 {
		names := make([]string, 0, len(running))
		for _, t := range running {
			names = append(names, t.Project+":"+t.Target)
		}
		attrs = append(attrs, slog.String("running", strings.Join(names, ", ")))
		msg += "; running: " + strings.Join(names, ", ")
	}
	if p.BuzzPeak > buzzHeapNoteworthy {
		attrs = append(attrs,
			slog.Int("buzz_objects", p.BuzzObjects),
			slog.Int("buzz_peak", p.BuzzPeak))
		msg += fmt.Sprintf("; buzz heap: %d objects (peak %d)", p.BuzzObjects, p.BuzzPeak)
		if p.BuzzHotSite != "" {
			attrs = append(attrs, slog.String("buzz_hot_site", p.BuzzHotSite))
			msg += ", most of it from " + p.BuzzHotSite
		} else {
			msg += " - an append-only heap this large usually means a magusfile is building a value in a loop"
		}
	}
	c.log.WarnContext(ctx, "cache.memory", append([]any{slog.String("msg", msg)}, attrs...)...)
}

// buzzHeapNoteworthy is the object count past which the Buzz heap is worth
// naming. Measured, not chosen: the case that motivated this reached tens of
// millions, while a healthy run of this repo stays orders of magnitude below.
const buzzHeapNoteworthy = 1_000_000
