package magus

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
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
			t.say(ctx, "remote: %d restored, %d missed, %d published, %d failed (%s down, %s up)\n",
				e.Hits, e.Misses, e.Published, e.Failures, cache.FormatBytes(int(e.DownBytes)), cache.FormatBytes(int(e.UpBytes)))
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
		t.say(ctx, "magus: the daemon is already running this exact command; not queued twice\n")
	case DetachQueued:
		t.say(ctx, "magus: detached as %s\n%s", e.Invocation, readIt)
	case DetachRunning:
		t.say(ctx, "magus: running as %s on the daemon\n", e.Invocation)
	case DetachUnwatched:
		// Ctrl-C detaches the watcher, not the run: say how to pick it up again rather
		// than implying it was cancelled.
		t.say(ctx, "\nmagus: stopped waiting; %s is still running on the daemon\n%s", e.Invocation, readIt)
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
