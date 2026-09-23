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

// Sink is where one invocation's progress goes: prose on stderr, or records on a
// [ReportWriter]. Build it once, where the output format is decided, with
// [Magus.TextSink] or [Magus.JSONLSink], and hand the same one to the run with
// [WithSink], so a header and the run it introduces cannot disagree about the format.
type Sink struct {
	m      *Magus
	report *report.Writer // nil for the text sink
	events eventSink
}

// TextSink renders progress as the prose a person reads, on stderr.
func (m *Magus) TextSink() *Sink { return &Sink{m: m, events: m.sinkFor(nil)} }

// JSONLSink records progress on rw, and through [WithSink] a run's per-target results
// too. It refuses a nil rw.
func (m *Magus) JSONLSink(rw *ReportWriter) (*Sink, error) {
	if rw == nil {
		return nil, errors.New("magus: a JSONL sink needs a ReportWriter")
	}
	return &Sink{m: m, report: rw.w, events: jsonlSink{w: rw.w}}, nil
}

// EmitScope emits the run's project selection, the "projects: ..." header.
func (s *Sink) EmitScope(ctx context.Context, label, source string) {
	s.events.emit(ctx, report.RunScope{Label: label, Source: source})
}

// EmitCharms emits the charms mixed into the run, the "charms: ..." header.
func (s *Sink) EmitCharms(ctx context.Context, charms string) {
	s.events.emit(ctx, report.RunCharms{Charms: charms})
}

// EmitCache emits which cache tiers the run can reach and whether it may write to
// them. An Inspect workspace has no cache and emits nothing.
func (s *Sink) EmitCache(ctx context.Context) {
	if s.m.cache == nil {
		return
	}
	tier, mode := s.m.cache.Description()
	s.events.emit(ctx, report.RunCache{Tier: tier, Mode: mode})
}

// EmitBase emits what an affected run's change set was compared against.
func (s *Sink) EmitBase(ctx context.Context, base string) {
	s.events.emit(ctx, report.RunBase{Base: base})
}

// EmitNotice emits an advisory line: a hint on stderr for text, a run.notice record for
// JSONL. code is the diagnostic it carries, if any, which message does not repeat.
func (s *Sink) EmitNotice(ctx context.Context, level slog.Level, code types.DiagnosticCode, message string) {
	s.events.emit(ctx, report.Notice{Level: level, Code: string(code), Message: message})
}

// EmitDiagnostic emits one coded diagnostic raised ABOUT a run rather than by an
// executed target, the run.diagnostic record the engine's own sink writes. unit is a
// project path or "<project>:<target>". Text renders it at debug: the prose a person
// reads for the same fact is the notice beside it.
func (s *Sink) EmitDiagnostic(ctx context.Context, unit string, code types.DiagnosticCode, message string) {
	s.events.emit(ctx, report.DiagnosticEmitted{Unit: unit, Code: string(code), Message: message})
}

// WithSink routes the run's progress through s, and for a JSONL sink its per-target
// results too. A nil s keeps the text sink.
func WithSink(s *Sink) RunOption {
	return func(o *run) {
		if s != nil {
			o.report = s.report
		}
	}
}

// eventSink is one invocation's progress output, chosen by whether it has a report
// writer. Every call site emits a report event through it rather than branching on the
// format itself.
type eventSink interface {
	emit(ctx context.Context, e any)
}

// sinkFor is the JSONL sink over w, or the text sink when w is nil.
func (m *Magus) sinkFor(w *report.Writer) eventSink {
	if w != nil {
		return jsonlSink{w: w}
	}
	return textSink{cache: m.cache, out: os.Stderr}
}

// jsonlSink records each event on the run's report stream.
type jsonlSink struct{ w *report.Writer }

func (s jsonlSink) emit(_ context.Context, e any) {
	if err := report.Record(s.w, e); err != nil {
		_ = report.Record(s.w, report.Notice{Level: slog.LevelError, Message: err.Error()})
	}
}

// textSink renders each event as the prose a person reads. Events carried by the cache
// logger go through cache; the rest are lines written to out, which -s cannot suppress:
// a run that fails a gate without saying why is the failure they exist to prevent.
type textSink struct {
	cache *cache.Cache // nil on an Inspect workspace, which drops the cache-logger events
	out   io.Writer
	log   *slog.Logger // debug-level detail a notice already summarizes; nil is slog.Default()
}

func (s textSink) emit(ctx context.Context, e any) {
	c := s.cache
	switch e := e.(type) {
	case report.RunScope:
		if c != nil {
			c.LogScope(ctx, e.Label, e.Source)
		}
	case report.RunCharms:
		if c != nil {
			c.LogCharms(ctx, e.Charms)
		}
	case report.RunCache:
		if c != nil {
			c.LogCache(ctx)
		}
	case report.RunBase:
		if c != nil {
			c.LogBase(ctx, e.Base)
		}
	case report.RunDry:
		if c == nil {
			fmt.Fprintln(s.out, "dry run: commands shown, not executed")
			return
		}
		c.LogDryBanner(ctx)
	case report.RunStep:
		s.step(ctx, e)
	case report.RunSummary:
		if c == nil {
			return
		}
		elapsed := time.Duration(e.DurationMs) * time.Millisecond
		if e.Dry {
			c.LogDrySummary(ctx, e.Planned, elapsed)
			return
		}
		c.LogSummary(ctx, elapsed)
	case report.RunRemote:
		if c != nil {
			c.LogRemoteSummary(ctx, e)
		}
	case report.Notice:
		msg := e.Message
		if e.Code != "" {
			msg = "[" + e.Code + "] " + msg
		}
		interactive.Emit(s.out, msg)
	case report.DiagnosticEmitted:
		log := s.log
		if log == nil {
			log = slog.Default()
		}
		log.DebugContext(ctx, "run.diagnostic",
			slog.String("unit", e.Unit), slog.String("code", e.Code), slog.String("message", e.Message))
	case report.DeterminismMismatch:
		s.diagnostic(types.NondeterministicOutput, fmt.Sprintf("non-deterministic output\n  project=%s target=%s differing_paths=%v",
			e.Project, e.Target, e.DifferingPaths))
	case report.DeterminismUnchecked:
		if e.Error != "" {
			s.diagnostic(types.NondeterministicOutput, fmt.Sprintf("cannot check byte-stability\n  project=%s target=%s err=%s",
				e.Project, e.Target, e.Error))
			return
		}
		s.diagnostic(types.NondeterministicOutput, fmt.Sprintf("declared outputs matched nothing, so byte-stability was not checked\n  project=%s target=%s globs=%s",
			e.Project, e.Target, e.Globs))
	case report.MissingDependency:
		s.diagnostic(types.MissingDependencyDetected, fmt.Sprintf("potential undeclared dependency\n  consumer=%s producer=%s path=%s scope=%s",
			e.Consumer, e.Producer, e.Path, e.Target))
	case report.OutputOverlapDetected:
		s.diagnostic(types.OutputOverlapDetected, fmt.Sprintf("declared output overlap\n  projects=[%s,%s] target=%s overlapping=%v",
			e.ProjectA, e.ProjectB, e.Target, e.Overlapping))
	default:
		// Visible rather than dropped; sink_test fails for any event that lands here.
		fmt.Fprintf(s.out, "magus: %s %T %+v\n", unrenderedEvent, e, e)
	}
}

// unrenderedEvent marks the line textSink writes for an event it has no prose for.
const unrenderedEvent = "unrendered event"

func (s textSink) diagnostic(code types.DiagnosticCode, body string) {
	fmt.Fprintln(s.out, types.FormatDiagnostic(code, body))
}

// step renders a run.step as the dry-run line or the collapsed stage row.
func (s textSink) step(ctx context.Context, e report.RunStep) {
	if s.cache == nil {
		if e.Status == "dry" {
			fmt.Fprintf(s.out, "[dry] %s\n", e.Label)
		}
		return
	}
	if e.Status == "dry" {
		s.cache.LogDry(ctx, e.Project, e.Label, e.Target)
		return
	}
	var err error
	if e.Status != "pass" {
		err = errors.New(e.Error)
	}
	s.cache.LogStage(ctx, e.Label, e.Target, time.Duration(e.DurationMs)*time.Millisecond, err, e.Status == "advisory")
}
