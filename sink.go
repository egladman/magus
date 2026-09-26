package magus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/types"
)

// Format is an output format an invocation asks for: the -o value.
type Format string

// The formats a [Sink] renders. Template is the -o template=... family; its body is
// the caller's to apply to the result and does not change how progress renders.
const (
	FormatText     Format = "text"
	FormatJSON     Format = "json"
	FormatYAML     Format = "yaml"
	FormatJSONL    Format = "jsonl"
	FormatName     Format = "name"
	FormatTemplate Format = "template"
)

// encoders is the one place a format is registered: each entry builds the encoder
// that renders every event a sink receives in that format. A document format keeps
// stdout for the single result its caller writes when the run ends, so its progress is
// the text encoder's, on stderr.
var encoders = map[Format]func(sinkEnv) encoder{
	FormatText:     newTextEncoder,
	FormatJSON:     newTextEncoder,
	FormatYAML:     newTextEncoder,
	FormatName:     newTextEncoder,
	FormatTemplate: newTextEncoder,
	FormatJSONL:    newJSONLEncoder,
}

// sinkEnv is what an encoder is built from.
type sinkEnv struct {
	stdout, stderr io.Writer
	level          slog.Level
	filter         *report.Filter // nil keeps every record type
}

// encoder renders events in one format. encode never fails its caller: an event it has
// no rendering for comes out marked, never dropped.
type encoder interface {
	encode(ctx context.Context, e any)
	close() error
}

// recorder is an encoder whose stream also carries the records the engine writes where
// they arise rather than through a sink: per-target results, lock decisions, race
// findings and graph telemetry. A format without one gets none of them.
type recorder interface {
	recordWriter() *report.Writer
}

// Sink carries one invocation's progress to the output format it asked for. Build it
// once with [NewSink], where the format is decided, emit the headers through it, and
// hand the same one to the run with [WithSink], so a header and the run it introduces
// cannot disagree about the format. Safe for concurrent use.
type Sink struct {
	enc     encoder
	records *report.Writer // the engine's own records; nil unless enc is a recorder
	// prose is set when records ride beside prose (WithSinkRecords), so a decision the
	// engine states in one place or the other is stated in prose.
	prose bool
}

// SinkOption configures [NewSink].
type SinkOption func(*sinkOptions)

type sinkOptions struct {
	level   slog.Level
	filter  []string
	records io.Writer
}

// WithSinkRecords also writes every event, and the engine's own records, to w as the
// JSONL records -o jsonl writes, beside a prose format's rendering. It is how a stage in
// a pipe of magus processes hands its records to the next while a person still reads
// the run on stderr. The prose keeps every line it prints without it, notices included,
// and w gets no notices. A format that already records refuses it.
func WithSinkRecords(w io.Writer) SinkOption {
	return func(o *sinkOptions) { o.records = w }
}

// WithSinkLevel sets the least severe progress a prose format prints: the run's log
// level, which -q and -s raise. Notices and diagnostics print at any level. A format
// that records ignores it. The default is info.
func WithSinkLevel(level slog.Level) SinkOption {
	return func(o *sinkOptions) { o.level = level }
}

// WithSinkFilter keeps only the record types terms name, in the report.filter config
// key's syntax. It applies to the records a format writes on stdout; notices on stderr
// always pass. A format that writes no records ignores it.
func WithSinkFilter(terms []string) SinkOption {
	return func(o *sinkOptions) { o.filter = terms }
}

// NewSink builds the sink for format over the invocation's stdout and stderr. Text and
// the document formats (json, yaml, name, template) render progress as prose on stderr
// and write nothing to stdout, which stays the caller's for the result. JSONL writes
// every event as a record: notices on stderr, everything else on stdout.
//
// An unknown format, a nil writer or an invalid filter term is an error. The caller
// owns the writers and must Close the sink after the run to flush it.
func NewSink(format Format, stdout, stderr io.Writer, opts ...SinkOption) (*Sink, error) {
	build, ok := encoders[format]
	if !ok {
		return nil, fmt.Errorf("magus: no sink renders output format %q", format)
	}
	if stdout == nil || stderr == nil {
		return nil, errors.New("magus: a sink needs both a stdout and a stderr writer")
	}
	o := sinkOptions{level: slog.LevelInfo}
	for _, opt := range opts {
		opt(&o)
	}
	env := sinkEnv{stdout: stdout, stderr: stderr, level: o.level}
	if len(o.filter) > 0 {
		f, err := report.ParseFilter(o.filter)
		if err != nil {
			return nil, err
		}
		env.filter = f
	}
	enc := build(env)
	if o.records == nil {
		return sinkOver(enc), nil
	}
	if _, ok := enc.(recorder); ok {
		return nil, fmt.Errorf("magus: output format %q already writes records; it takes no second record stream", format)
	}
	recEnv := env
	recEnv.stdout, recEnv.stderr = o.records, o.records
	s := sinkOver(teeEncoder{prose: enc, records: newJSONL(recEnv)})
	s.prose = true
	return s, nil
}

func sinkOver(enc encoder) *Sink {
	s := &Sink{enc: enc}
	if r, ok := enc.(recorder); ok {
		s.records = r.recordWriter()
	}
	return s
}

// teeEncoder renders prose and writes records side by side. Notices stay prose.
type teeEncoder struct {
	prose   encoder
	records *jsonlEncoder
}

func (t teeEncoder) encode(ctx context.Context, ev any) {
	t.prose.encode(ctx, ev)
	if _, notice := ev.(report.Notice); !notice {
		t.records.encode(ctx, ev)
	}
}

func (t teeEncoder) close() error { return errors.Join(t.prose.close(), t.records.close()) }

func (t teeEncoder) recordWriter() *report.Writer { return t.records.recordWriter() }

// Close flushes what the sink holds back and waits for it to be written. It does not
// close the writers. Idempotent.
func (s *Sink) Close() error { return s.enc.close() }

// GraphObserver returns the observer that records graph-traversal events on this sink,
// for [Magus.SetGraphObserver]. It observes nothing for a format that writes no records.
func (s *Sink) GraphObserver() types.Observer { return report.GraphObserver(s.records) }

// EmitScope emits the run's project selection, the "projects: ..." header. It starts a
// run: a terminal's live band forgets the previous one here. projects are the selected
// projects' workspace paths, which the record carries for the stage downstream.
func (s *Sink) EmitScope(ctx context.Context, label, source string, projects []string) {
	s.emit(ctx, report.RunScope{Label: label, Source: source, Projects: projects})
}

// EmitCharms emits the charms mixed into the run, the "charms: ..." header.
func (s *Sink) EmitCharms(ctx context.Context, charms string) {
	s.emit(ctx, report.RunCharms{Charms: charms})
}

// EmitCache emits which cache tiers the run can reach and whether it may write to them,
// as [Magus.CacheDescription] reports them. Text prints the boring case too: a pull
// request reads the shared cache but must not publish to it, which is invisible unless
// something says so. An empty tier, a workspace with no cache, prints nothing.
func (s *Sink) EmitCache(ctx context.Context, tier, mode string) {
	s.emit(ctx, report.RunCache{Tier: tier, Mode: mode})
}

// EmitBase emits what an affected run's change set was compared against, the third
// input that decides what runs. base reads like "git diff vs origin/main", a ref a
// reader can paste back into their own VCS. An empty base prints nothing.
func (s *Sink) EmitBase(ctx context.Context, base string) {
	s.emit(ctx, report.RunBase{Base: base})
}

// EmitNotice emits an advisory line: a hint on stderr for prose, which -s does not
// suppress and hints.enabled turns off, and a run.notice record on stderr for JSONL.
// code is the diagnostic it carries, if any, which message does not repeat.
func (s *Sink) EmitNotice(ctx context.Context, level slog.Level, code types.DiagnosticCode, message string) {
	s.emit(ctx, report.Notice{Level: level, Code: string(code), Message: message})
}

// EmitDiagnostic emits one coded diagnostic raised ABOUT a run rather than by an
// executed target, the run.diagnostic record the engine's own diagnostics produce. unit
// is a project path or "<project>:<target>". Prose renders it at debug: what a person
// reads for the same fact is the notice beside it.
func (s *Sink) EmitDiagnostic(ctx context.Context, unit string, code types.DiagnosticCode, message string) {
	s.emit(ctx, report.DiagnosticEmitted{Unit: unit, Code: string(code), Message: message})
}

// EmitShardTotal emits a CI shard's wall-clock time, job start to last project end, for
// adaptive shard forecasting. Prose renders it at debug.
//
// Written, not yet read: nothing ingests shard.total back into a forecast.History, so it
// does not (yet) feed the fit described at forecast.DefaultSetupMs.
func (s *Sink) EmitShardTotal(ctx context.Context, shard string, nShards int, elapsed time.Duration) {
	s.emit(ctx, report.ShardTotal{Shard: shard, NShards: nShards, DurationMs: elapsed.Milliseconds()})
}

// DetachState is where an invocation handed to the server with --detach stands.
type DetachState string

// The states [Sink.EmitDetach] reports.
const (
	DetachCoalesced DetachState = "coalesced" // an identical invocation was already running; none was queued
	DetachQueued    DetachState = "queued"    // handed to the server, not waited on
	DetachRunning   DetachState = "running"   // handed to the server and waited on
	DetachUnwatched DetachState = "unwatched" // the wait stopped; the run continues on the server
	DetachPassed    DetachState = "passed"
	DetachFailed    DetachState = "failed"
)

// EmitDetach emits where a detached invocation stands. Prose prints it at any level,
// with the command that reads the invocation back. elapsed is the run's duration and
// counts only for passed and failed.
func (s *Sink) EmitDetach(ctx context.Context, invocation string, state DetachState, elapsed time.Duration) {
	s.emit(ctx, report.RunDetach{Invocation: invocation, State: string(state), DurationMs: elapsed.Milliseconds()})
}

// RecordValues records what target returned on each project, for a format that
// records; a person reads a returned value through -o json instead, so prose gets none.
func (s *Sink) RecordValues(target string, returns types.Returns) {
	for _, project := range slices.Sorted(maps.Keys(returns)) {
		_ = report.Record(s.records, report.TargetValue{Project: project, Target: target, Value: returns[project]})
	}
}

func (s *Sink) emit(ctx context.Context, e any) { s.enc.encode(ctx, e) }

// WithSink routes the run's progress through s, and for a format that records, the
// engine's own records too. A nil s reports through a text sink on stderr.
func WithSink(s *Sink) RunOption {
	return func(o *run) {
		o.sink = s
		o.report, o.lockReport = nil, nil
		if s != nil {
			o.report = s.records
			if !s.prose {
				o.lockReport = s.records
			}
		}
	}
}

// jsonlEncoder writes every event as one record. Notices go to stderr, where a text run
// prints them and where log records reach the same encoder through
// report.NewNoticeHandler; stdout carries what the run did.
type jsonlEncoder struct {
	records *report.Writer
	notices *report.LineEncoder // nil when stdout and stderr are one writer
}

func newJSONLEncoder(env sinkEnv) encoder { return newJSONL(env) }

func newJSONL(env sinkEnv) *jsonlEncoder {
	opts := []report.Option{report.WithBlockOnFull()}
	if env.filter != nil {
		opts = append(opts, report.WithFilter(env.filter))
	}
	e := &jsonlEncoder{records: report.NewWriter(env.stdout, opts...)}
	// One writer under both would take writes from the drain goroutine and the caller's
	// at once, so its notices ride the record stream instead.
	if !sameWriter(env.stdout, env.stderr) {
		e.notices = report.NewLineEncoder(env.stderr)
	}
	return e
}

func (e *jsonlEncoder) encode(_ context.Context, ev any) {
	if n, ok := ev.(report.Notice); ok && e.notices != nil {
		if err := e.notices.Encode(n); err == nil {
			return
		}
		// A notice stderr refused still reaches the reader, on the record stream.
	}
	if err := report.Record(e.records, ev); err != nil {
		_ = report.Record(e.records, report.Notice{Level: slog.LevelError, Message: err.Error()})
	}
}

func (e *jsonlEncoder) close() error { return e.records.Close() }

func (e *jsonlEncoder) recordWriter() *report.Writer { return e.records }

// sameWriter reports whether a and b are one pointer. Only pointers are compared: ==
// on two interfaces holding the same uncomparable type panics.
func sameWriter(a, b io.Writer) bool {
	ta := reflect.TypeOf(a)
	return ta == reflect.TypeOf(b) && ta.Kind() == reflect.Pointer && a == b
}

// terminal is where the text encoder draws. A person's terminal is the process's
// *cache.PrettyHandler, which owns the live band the lines scroll above and the
// failures pinned in it; any other writer is a plainTerminal.
type terminal interface {
	WantsColor() bool
	Print(ctx context.Context, text string) error
	BeginRun()
	EndRun(ctx context.Context, footer string) error
}

// terminalFor draws through the process's display handler when w is standard error and
// one paints there: writing past it would scroll lines through its band.
func terminalFor(w io.Writer) terminal {
	if w == os.Stderr {
		if h := cache.StderrHandler(); h != nil {
			return h
		}
	}
	return &plainTerminal{w: w}
}

// textSink is the sink a run reports through when its caller passed none.
func (m *Magus) textSink() *Sink {
	return sinkOver(textEncoder{term: terminalFor(os.Stderr), level: m.cfg.Log.SlogLevel()})
}

// plainTerminal writes uncolored lines to w.
type plainTerminal struct {
	mu sync.Mutex
	w  io.Writer
}

func (t *plainTerminal) WantsColor() bool { return false }

func (t *plainTerminal) Print(ctx context.Context, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := io.WriteString(t.w, secret.RedactString(ctx, text))
	return err
}

func (t *plainTerminal) BeginRun() {}

func (t *plainTerminal) EndRun(ctx context.Context, footer string) error { return t.Print(ctx, footer) }

// textEncoder renders each event as the prose a person reads, from the event alone.
type textEncoder struct {
	term  terminal
	level slog.Level // the least severe progress printed
}

func newTextEncoder(env sinkEnv) encoder {
	return textEncoder{term: terminalFor(env.stderr), level: env.level}
}

func (t textEncoder) enabled(level slog.Level) bool { return level >= t.level }

func (textEncoder) close() error { return nil }

// unrenderedEvent marks the line the text encoder writes for an event it has no prose
// for, so the event is visible rather than dropped. sink_test fails for any real event
// that lands there.
const unrenderedEvent = "unrendered event"

func (t textEncoder) encode(ctx context.Context, e any) {
	switch e := e.(type) {
	case report.RunScope:
		if !t.enabled(slog.LevelInfo) {
			return
		}
		t.term.BeginRun()
		if e.Source != "" {
			t.say(ctx, "projects: %s (%s)\n", e.Label, e.Source)
			return
		}
		t.say(ctx, "projects: %s\n", e.Label)
	case report.RunCharms:
		charms := e.Charms
		if charms == "" {
			charms = "(none)"
		}
		t.info(ctx, "charms: %s\n", charms)
	case report.RunCache:
		if e.Tier != "" {
			t.info(ctx, "cache: %s (%s)\n", e.Tier, e.Mode)
		}
	case report.RunBase:
		if e.Base != "" {
			t.info(ctx, "base: %s\n", e.Base)
		}
	case report.RunDry:
		t.info(ctx, "%s\n", t.dim("dry run: commands shown, not executed"))
	case report.RunStep:
		t.step(ctx, e)
	case report.RunSummary:
		t.summary(ctx, e)
	case report.RunRemote:
		// Warn so a run whose remote degraded cannot end on a line that reads like
		// success. The zero case prints too: silence is what made a configured remote
		// that did nothing indistinguishable from one that worked.
		level := slog.LevelInfo
		if e.Failures > 0 {
			level = slog.LevelWarn
		}
		if t.enabled(level) {
			t.say(ctx, "remote: %d restored, %d missed, %d stored, %d failed (%s down, %s up)\n",
				e.Hits, e.Misses, e.Stored, e.Failures, cache.FormatBytes(int(e.DownBytes)), cache.FormatBytes(int(e.UpBytes)))
		}
	case report.ShardTotal:
		if t.enabled(slog.LevelDebug) {
			t.say(ctx, "shard %s of %d: %s\n", e.Shard, e.NShards, cache.FormatDuration(time.Duration(e.DurationMs)*time.Millisecond))
		}
	case report.RunDetach:
		t.detach(ctx, e)
	case report.Notice:
		t.notice(ctx, e)
	case report.DiagnosticEmitted:
		if t.enabled(slog.LevelDebug) {
			t.say(ctx, "[%s] %s: %s\n", e.Code, e.Unit, e.Message)
		}
	case report.DeterminismMismatch:
		t.diagnostic(ctx, types.NondeterministicOutput, fmt.Sprintf("non-deterministic output\n  project=%s target=%s differing_paths=%v",
			e.Project, e.Target, e.DifferingPaths))
	case report.DeterminismUnchecked:
		if e.Error != "" {
			t.diagnostic(ctx, types.NondeterministicOutput, fmt.Sprintf("cannot check byte-stability\n  project=%s target=%s err=%s",
				e.Project, e.Target, e.Error))
			return
		}
		t.diagnostic(ctx, types.NondeterministicOutput, fmt.Sprintf("declared outputs matched nothing, so byte-stability was not checked\n  project=%s target=%s globs=%s",
			e.Project, e.Target, e.Globs))
	case report.MissingDependency:
		t.diagnostic(ctx, types.MissingDependencyDetected, fmt.Sprintf("potential undeclared dependency\n  consumer=%s producer=%s path=%s scope=%s",
			e.Consumer, e.Producer, e.Path, e.Target))
	case report.OutputOverlapDetected:
		t.diagnostic(ctx, types.OutputOverlapDetected, fmt.Sprintf("declared output overlap\n  projects=[%s,%s] target=%s overlapping=%v",
			e.ProjectA, e.ProjectB, e.Target, e.Overlapping))
	default:
		t.say(ctx, "magus: %s %T %+v\n", unrenderedEvent, e, e)
	}
}

// say prints whatever the level: notices and diagnostics reach a -s run, because a gate
// that fails without saying why is the failure they exist to prevent.
func (t textEncoder) say(ctx context.Context, format string, args ...any) {
	_ = t.term.Print(ctx, fmt.Sprintf(format, args...))
}

// info prints progress, which -q and -s suppress.
func (t textEncoder) info(ctx context.Context, format string, args ...any) {
	if t.enabled(slog.LevelInfo) {
		t.say(ctx, format, args...)
	}
}

func (t textEncoder) dim(s string) string {
	if t.term.WantsColor() {
		return tty.Colorize(s, tty.SGRDim)
	}
	return s
}

// step renders a run.step. A planned step carries no outcome and no duration, but the
// same repro command an executed one does, so a plan and a run read alike. An executed
// one is indented under its project, which stages of concurrent projects interleave
// with, and says advisory where the composite carries a failure on past: a gate reader
// meeting [fail] on a row nothing failed on has to go looking for the real answer.
func (t textEncoder) step(ctx context.Context, e report.RunStep) {
	colorize := t.term.WantsColor()
	if e.Status == "dry" {
		line := cache.Glyph(colorize, "dry", tty.SGRDim) + " " + e.Label + "\n"
		if e.Project != "" && e.Target != "" {
			line += hint.Run.With(e.Target, e.Project) + "\n"
		}
		t.info(ctx, "%s", line)
		return
	}
	name, color := "pass", tty.SGRGreen
	switch e.Status {
	case "pass":
	case "advisory":
		name, color = "advisory", tty.SGRYellow
	default:
		name, color = "fail", tty.SGRRed
	}
	t.info(ctx, "  %s %s %s (%s)\n", cache.Glyph(colorize, name, color), e.Label, e.Target,
		cache.FormatDuration(time.Duration(e.DurationMs)*time.Millisecond))
}

// summary renders the footer. A dry run ends with one too, since it is the line a
// reader looks for at the bottom, but in its own words: nothing executed, so cached,
// ran and failed would all read 0 for a plan that intends to run plenty.
func (t textEncoder) summary(ctx context.Context, e report.RunSummary) {
	if !t.enabled(slog.LevelInfo) {
		return
	}
	lead := "summary: "
	if t.term.WantsColor() {
		lead = "\nSummary: "
	}
	elapsed := cache.FormatDuration(time.Duration(e.DurationMs) * time.Millisecond)
	footer := fmt.Sprintf("%s%d cached, %d ran, %d failed (%s)\n", lead, e.Hits, e.Misses, e.Errors, elapsed)
	if e.Dry {
		footer = fmt.Sprintf("%sdry run, %s would run (%s)\n", lead, plural(e.Planned, "target"), elapsed)
	}
	_ = t.term.EndRun(ctx, footer)
}

// detach hands back the invocation and the command that reads it, never a dashboard to
// poll: the id is addressable, and its journal holds the outcome, timings and output
// refs whenever the reader wants them.
func (t textEncoder) detach(ctx context.Context, e report.RunDetach) {
	readIt := "  read it with: " + hint.QueryInvocation.With(e.Invocation) + "\n"
	took := cache.FormatDuration(time.Duration(e.DurationMs) * time.Millisecond)
	switch DetachState(e.State) {
	case DetachCoalesced:
		t.say(ctx, "magus: the server is already running this exact command; not queued twice\n")
	case DetachQueued:
		t.say(ctx, "magus: detached as %s\n%s", e.Invocation, readIt)
	case DetachRunning:
		t.say(ctx, "magus: running as %s on the server\n", e.Invocation)
	case DetachUnwatched:
		// Ctrl-C detaches the watcher, not the run: say how to pick it up again rather
		// than implying it was cancelled.
		t.say(ctx, "\nmagus: stopped waiting; %s is still running on the server\n%s", e.Invocation, readIt)
	case DetachPassed:
		t.say(ctx, "magus: %s passed (%s)\n%s", e.Invocation, took, readIt)
	case DetachFailed:
		t.say(ctx, "magus: %s failed (%s)\n%s", e.Invocation, took, readIt)
	default:
		t.say(ctx, "magus: %s %T %+v\n", unrenderedEvent, e, e)
	}
}

// notice rides the hint channel: the run that pays for a notice is often a gate run
// with -s, and hints are what -s still bubbles up. hints.enabled turns them off, and
// one already shown in this process is not repeated.
func (t textEncoder) notice(ctx context.Context, e report.Notice) {
	msg := e.Message
	if e.Code != "" {
		msg = "[" + e.Code + "] " + msg
	}
	var b strings.Builder
	interactive.Emit(&b, msg)
	if b.Len() > 0 {
		_ = t.term.Print(ctx, b.String())
	}
}

func (t textEncoder) diagnostic(ctx context.Context, code types.DiagnosticCode, body string) {
	t.say(ctx, "%s\n", types.FormatDiagnostic(code, body))
}

// plural writes the noun out rather than "target(s)", which reads as a writer refusing
// to pick in output a reader is already scanning under pressure.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
