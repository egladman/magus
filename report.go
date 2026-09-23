package magus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

// ReportWriter is an async JSONL event sink for run telemetry.
// Create one with [NewReportWriter], pass it to Run via [WithReport], and close
// it after the run completes.
type ReportWriter struct{ w *report.Writer }

// NewReportWriter constructs a ReportWriter that writes JSONL events to dst.
// filter is an optional list of event-type terms; an empty or nil slice disables
// filtering (all events pass through).
func NewReportWriter(dst io.Writer, filter []string) (*ReportWriter, error) {
	var opts []report.Option
	if len(filter) > 0 {
		f, err := report.ParseFilter(filter)
		if err != nil {
			return nil, err
		}
		if f != nil {
			opts = append(opts, report.WithFilter(f))
		}
	}
	return &ReportWriter{w: report.NewWriter(dst, opts...)}, nil
}

// Close flushes and closes the writer. Must be called after the run finishes.
func (rw *ReportWriter) Close() error { return rw.w.Close() }

// GraphObserver returns an [types.Observer] that records graph-traversal events
// to this writer. Pass the result to [Magus.SetGraphObserver].
func (rw *ReportWriter) GraphObserver() types.Observer { return report.GraphObserver(rw.w) }

// RecordShardTotal appends a shard-level wall-clock observation (job start → last
// project end) for adaptive CI forecast. Call after the run completes when running
// in a CI matrix; shardID and nShards come from --shard / --n-shards.
//
// Written, not yet read: nothing ingests the shard.total JSONL line back into a
// forecast.History, so it does not (yet) feed the SetupP50Ms/AlphaMs fit described
// at forecast.DefaultSetupMs.
func (rw *ReportWriter) RecordShardTotal(shardID string, nShards int, duration time.Duration) error {
	return report.Record(rw.w, report.ShardTotal{
		Shard:      shardID,
		NShards:    nShards,
		DurationMs: duration.Milliseconds(),
	})
}

// RecordDiagnostic appends one coded diagnostic raised ABOUT a run rather than by an
// executed target, so a consumer reads it as the same run.diagnostic event the engine's
// own sink emits. unit is a project path or "<project>:<target>".
func (rw *ReportWriter) RecordDiagnostic(unit string, code types.DiagnosticCode, message string) error {
	return report.Record(rw.w, report.DiagnosticEmitted{Unit: unit, Code: string(code), Message: message})
}

// RecordNotice appends a free-form advisory line (a hint, warning, or one-time
// banner) that has no dedicated event type of its own. level is "info" or "warn";
// code is the diagnostic code (e.g. an MGS####) when the notice carries one.
func (rw *ReportWriter) RecordNotice(level string, code types.DiagnosticCode, message string) error {
	return report.Record(rw.w, report.Notice{Level: level, Code: string(code), Message: message})
}

// WithReport attaches rw to receive one JSONL event per executed target, and routes
// the run's progress there as typed events rather than prose. See [WithSink] for a
// caller that also emits its own header.
func WithReport(rw *ReportWriter) RunOption {
	return func(o *run) { o.Report = rw.w }
}

// Sink is where one invocation's progress goes: prose on stderr through the cache
// logger, or typed events on a [ReportWriter]. Build it once, where the output format
// is decided, with [Magus.Sink], and hand the same one to the run with [WithSink], so
// a header and the run it introduces cannot disagree about the format.
type Sink struct {
	m      *Magus
	events eventSink
	report *report.Writer // nil for the text sink
}

// Sink returns the text sink when rw is nil and the JSONL sink over rw otherwise.
func (m *Magus) Sink(rw *ReportWriter) *Sink {
	var w *report.Writer
	if rw != nil {
		w = rw.w
	}
	return &Sink{m: m, events: m.newSink(w), report: w}
}

// Scope emits the run's project selection, the "projects: ..." header.
func (s *Sink) Scope(ctx context.Context, label, source string) {
	s.events.emit(ctx, report.RunScope{Label: label, Source: source})
}

// Charms emits the charms mixed into the run, the "charms: ..." header.
func (s *Sink) Charms(ctx context.Context, charms string) {
	s.events.emit(ctx, report.RunCharms{Charms: charms})
}

// Cache emits which cache tiers the run can reach and whether it may write to them.
func (s *Sink) Cache(ctx context.Context) {
	tier, mode := s.m.CacheDescription()
	s.events.emit(ctx, report.RunCache{Tier: tier, Mode: mode})
}

// Base emits what an affected run's change set was compared against.
func (s *Sink) Base(ctx context.Context, base string) {
	s.events.emit(ctx, report.RunBase{Base: base})
}

// Notice emits an advisory line: a hint on stderr for text, a run.notice event for
// JSONL. level is "info" or "warn"; code is the diagnostic it carries, if any.
func (s *Sink) Notice(ctx context.Context, level string, code types.DiagnosticCode, message string) {
	s.events.emit(ctx, report.Notice{Level: level, Code: string(code), Message: message})
}

// WithSink routes the run's progress through s, and for a JSONL sink its per-target
// results too. It replaces [WithReport] for a caller that already emitted a header
// through s.
func WithSink(s *Sink) RunOption {
	return func(o *run) {
		o.sink = s.events
		o.Report = s.report
	}
}

// eventSink is one invocation's progress output. The run chooses exactly one, by
// whether it has a report writer, and every call site emits a report event through it
// rather than branching on the format itself.
type eventSink interface {
	emit(ctx context.Context, e any)
}

// newSink is the JSONL sink over w, or the text sink when w is nil.
func (m *Magus) newSink(w *report.Writer) eventSink {
	if w != nil {
		return jsonlSink{w: w}
	}
	return textSink{cache: m.cache, out: os.Stderr}
}

// jsonlSink records each event on the run's report stream.
type jsonlSink struct{ w *report.Writer }

func (s jsonlSink) emit(_ context.Context, e any) { _ = report.Record(s.w, e) }

// dryRunBanner is the dry-run notice. The text sink renders it through the cache
// logger's own banner rather than as a hint.
const dryRunBanner = "dry run: commands shown, not executed"

// textSink renders each event as the prose a person reads. Events carried by the cache
// logger go through cache; the rest are decision lines written to out, which -s cannot
// suppress: a run that stalls, yields or fails a gate without saying why is the failure
// they exist to prevent.
type textSink struct {
	cache *cache.Cache // nil on an Inspect workspace, which drops the cache-logger events
	out   io.Writer
}

func (s textSink) emit(ctx context.Context, e any) {
	switch e := e.(type) {
	case report.RunScope:
		s.logged(func(c *cache.Cache) { c.LogScope(ctx, e.Label, e.Source) })
	case report.RunCharms:
		s.logged(func(c *cache.Cache) { c.LogCharms(ctx, e.Charms) })
	case report.RunCache:
		s.logged(func(c *cache.Cache) { c.LogCache(ctx) })
	case report.RunBase:
		s.logged(func(c *cache.Cache) { c.LogBase(ctx, e.Base) })
	case report.RunStep:
		if !s.logged(func(c *cache.Cache) { logStep(ctx, c, e) }) && e.Status == "dry" {
			fmt.Fprintf(s.out, "[dry] %s\n", e.Label)
		}
	case report.RunSummary:
		elapsed := time.Duration(e.DurationMs) * time.Millisecond
		s.logged(func(c *cache.Cache) {
			if e.Dry {
				c.LogDrySummary(ctx, e.Planned, elapsed)
				return
			}
			c.LogSummary(ctx, elapsed)
		})
	case report.RunRemote:
		s.logged(func(c *cache.Cache) { c.LogRemoteSummary(ctx, cache.RemoteSummary(e)) })
	case report.Notice:
		if e.Message == dryRunBanner {
			if !s.logged(func(c *cache.Cache) { c.LogDryBanner(ctx) }) {
				fmt.Fprintln(s.out, dryRunBanner)
			}
			return
		}
		interactive.Emit(s.out, e.Message)
	case report.DeterminismMismatch:
		fmt.Fprintln(s.out, types.FormatDiagnostic(types.NondeterministicOutput, determinismProse(e)))
	case report.LockWait:
		s.lockWait(ctx, e)
	case report.LockReleased:
		fmt.Fprintf(s.out, "magus: lock on project %s released; starting.\n", e.Project)
		slog.InfoContext(ctx, "lock.acquired", slog.String("project", e.Project))
	case report.LockSuperseded:
		if e.TimedOut {
			fmt.Fprintf(s.out, "magus: the earlier gate on project %s did not stop within %s; waiting for it instead.\n",
				e.Project, time.Duration(e.BoundMs)*time.Millisecond)
			return
		}
		fmt.Fprintf(s.out, "magus: superseded the earlier gate on project %s%s; its verdict would have described a tree that has since changed.\n",
			e.Project, heldBy(e.Holder))
		slog.InfoContext(ctx, "lock.superseded",
			slog.String("project", e.Project),
			slog.Int("holder_pid", e.HolderPID),
			slog.String("holder_command", e.Command))
	}
}

// logged runs fn against the cache logger and reports whether there was one.
func (s textSink) logged(fn func(*cache.Cache)) bool {
	if s.cache == nil {
		return false
	}
	fn(s.cache)
	return true
}

// logStep renders a run.step as the dry-run line or the collapsed stage row.
func logStep(ctx context.Context, c *cache.Cache, e report.RunStep) {
	if e.Status == "dry" {
		c.LogDry(ctx, e.Project, e.Label, e.Target)
		return
	}
	var err error
	if e.Status != "pass" {
		err = errors.New(e.Error)
	}
	c.LogStage(ctx, e.Label, e.Target, time.Duration(e.DurationMs)*time.Millisecond, err, e.Status == "advisory")
}

// lockWait is the first wait line, or on a heartbeat (ElapsedMs set) the reminder that
// the run is still waiting and not hung.
func (s textSink) lockWait(ctx context.Context, e report.LockWait) {
	if e.ElapsedMs > 0 {
		elapsed := time.Duration(e.ElapsedMs) * time.Millisecond
		fmt.Fprintf(s.out,
			"magus: still waiting for the lock on project %s (%s elapsed); this run is NOT hung. Set MAGUS_NO_WAIT=1 to fail fast instead.%s\n",
			e.Project, elapsed.Round(time.Second), orphanHint(elapsed, e.Holder))
		return
	}
	fmt.Fprintf(s.out, "magus: project %s is being changed by another magus process%s; waiting for it to finish. This run starts automatically once it does; set MAGUS_NO_WAIT=1 to fail fast instead.\n", e.Project, heldBy(e.Holder))
	// What is SAFE while you wait, which is the question a blocked caller actually has and
	// the one the line above leaves open.
	//
	// Measured on one session: ten gate runs in two hours, thirty-eight minutes of wall
	// clock, and most of the waiting was a caller who had stopped working entirely because
	// it could not tell which edits would spoil the run. Nothing said. The cost of that
	// silence is a person or an agent idling for the length of a full gate, repeatedly,
	// and it is worse than the cost this message was written to explain.
	//
	// The rule is narrow enough to state: a run reads the locked project's files, so
	// editing THOSE makes its verdict describe a tree that no longer exists. Everything
	// else in the workspace is untouched by it.
	fmt.Fprintf(s.out, "magus: while it runs, editing files OUTSIDE %s is safe; editing files INSIDE it makes this run's verdict describe a tree that no longer exists.\n", e.Project)
	// Also as a record, so the sticky terminal region can PIN the wait. The stderr
	// line above announces the event and then scrolls away; a run that is blocked
	// needs the state to stay on screen, because the alternative a reader sees is
	// silence.
	// Structured, not the rendered line above: that string is this package's own
	// stderr output, and handing it to another package to display verbatim would put
	// presentation for a surface magus cannot see inside magus. The region composes
	// its own from these fields.
	slog.InfoContext(ctx, "lock.waiting",
		slog.String("project", e.Project),
		slog.Int("holder_pid", e.HolderPID),
		slog.String("holder_command", e.Command))
}

// determinismProse is the MGS4003 body for one project: why its byte-stability failed,
// or could not be checked.
func determinismProse(e report.DeterminismMismatch) string {
	switch {
	case e.Error != "":
		return fmt.Sprintf("cannot check byte-stability\n  project=%s target=%s err=%s", e.Project, e.Target, e.Error)
	case e.Globs != "":
		return fmt.Sprintf("declared outputs matched nothing, so byte-stability was not checked\n  project=%s target=%s globs=%s",
			e.Project, e.Target, e.Globs)
	}
	return fmt.Sprintf("non-deterministic output\n  project=%s target=%s differing_paths=%v", e.Project, e.Target, e.DifferingPaths)
}
