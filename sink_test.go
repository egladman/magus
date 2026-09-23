package magus

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sinkEvents is one sample of every event an invocation emits through its sink.
var sinkEvents = []any{
	report.RunScope{Label: "api", Source: "magusfile"},
	report.RunCharms{Charms: "rw"},
	report.RunCache{Tier: "local", Mode: "read+write"},
	report.RunBase{Base: "git diff vs main"},
	report.RunDry{},
	report.RunStep{Label: "api", Project: "api", Target: "build", Status: "dry"},
	report.RunSummary{Hits: 1, Misses: 1, DurationMs: 20},
	report.RunRemote{Hits: 1, Failures: 1},
	report.RunDetach{Invocation: "inv1", State: string(DetachPassed), DurationMs: 1200},
	report.ShardTotal{Shard: "1", NShards: 4, DurationMs: 1500},
	report.Notice{Level: slog.LevelWarn, Code: "MGS1028", Message: "every-event notice"},
	report.DiagnosticEmitted{Unit: "api", Code: "MGS1028", Message: "seeded by LICENSE"},
	report.DeterminismMismatch{Project: "api", Target: "build", DifferingPaths: []string{"dist/a"}},
	report.DeterminismUnchecked{Project: "api", Target: "build", Globs: "dist/**"},
	report.MissingDependency{Consumer: "web", Producer: "api", Path: "web/gen.go", Target: "."},
	report.OutputOverlapDetected{ProjectA: "api", ProjectB: "web", Target: "build", Overlapping: []string{"dist/**"}},
}

// recordedOutsideTheSink are the registered events the engine records straight onto a
// recording format's stream where they arise, never through a sink: per-target
// results, graph and volatility telemetry, race findings, and the lock's decisions.
var recordedOutsideTheSink = []any{
	report.TargetResult{}, report.GraphBuild{}, report.GraphQuery{}, report.GraphError{},
	report.VolatilityCall{}, report.RaceDetected{},
	report.LockSuperseded{}, report.LockSupersedeRefused{},
}

// Every event type is either rendered by every format or recorded outside the sink, so
// a new report type cannot reach the text encoder's fallback and print as a raw struct.
func TestEveryRegisteredEventIsClassified(t *testing.T) {
	classified := map[reflect.Type]bool{}
	for _, e := range append(append([]any(nil), sinkEvents...), recordedOutsideTheSink...) {
		classified[reflect.TypeOf(e)] = true
	}
	for _, rt := range report.RegisteredTypes() {
		assert.True(t, classified[rt], "%s is neither in sinkEvents nor recordedOutsideTheSink", rt)
	}
}

// Every registered format renders every sink event from the event alone. A prose format
// writes it to stderr and leaves stdout to the caller's result; JSONL writes exactly one
// record of the event's own type, notices on stderr and the rest on stdout.
func TestEveryFormatRendersEverySinkEvent(t *testing.T) {
	t.Cleanup(func() { interactive.SetHintsEnabled(true) })
	interactive.SetHintsEnabled(true)
	for _, format := range slices.Sorted(maps.Keys(encoders)) {
		for _, e := range sinkEvents {
			t.Run(string(format)+"/"+reflect.TypeOf(e).Name(), func(t *testing.T) {
				if n, ok := e.(report.Notice); ok {
					// Hints print once per process; each case needs its own.
					n.Message += " " + string(format)
					e = n
				}
				var stdout, stderr bytes.Buffer
				s, err := NewSink(format, &stdout, &stderr, WithSinkLevel(slog.LevelDebug))
				require.NoError(t, err)
				s.emit(t.Context(), e)
				require.NoError(t, s.Close())

				if format != FormatJSONL {
					assert.Empty(t, stdout.String(), "stdout is the caller's result")
					assert.NotEmpty(t, stderr.String(), "rendered nothing")
					assert.NotContains(t, stderr.String(), unrenderedEvent)
					return
				}
				want, onStderr := &stdout, false
				if _, ok := e.(report.Notice); ok {
					want, onStderr = &stderr, true
				}
				lines := strings.Split(strings.TrimSpace(want.String()), "\n")
				require.Len(t, lines, 1, "stdout=%q stderr=%q", stdout.String(), stderr.String())
				var head struct {
					Type string `json:"type"`
				}
				require.NoError(t, json.Unmarshal([]byte(lines[0]), &head))
				assert.Equal(t, report.TypeOf(e), head.Type)
				if onStderr {
					assert.Empty(t, stdout.String())
				} else {
					assert.Empty(t, stderr.String())
				}
			})
		}
	}
}

// fakeEncoder stands in for a format that does not exist yet.
type fakeEncoder struct {
	got    []any
	closed bool
}

func (f *fakeEncoder) encode(_ context.Context, e any) { f.got = append(f.got, e) }
func (f *fakeEncoder) close() error                    { f.closed = true; return nil }

// Adding a format is adding an encoder to the registry: nothing else learns its name,
// and every event a sink carries reaches it.
func TestAFormatIsOneEncoder(t *testing.T) {
	const fake Format = "fake"
	enc := &fakeEncoder{}
	encoders[fake] = func(sinkEnv) encoder { return enc }
	t.Cleanup(func() { delete(encoders, fake) })

	s, err := NewSink(fake, io.Discard, io.Discard)
	require.NoError(t, err)
	ctx := t.Context()
	s.EmitScope(ctx, "api", "magusfile")
	s.EmitCharms(ctx, "rw")
	s.EmitCache(ctx, "local", "read+write")
	s.EmitBase(ctx, "git diff vs main")
	s.EmitNotice(ctx, slog.LevelWarn, types.AffectedSetUncomputable, "full build")
	s.EmitDiagnostic(ctx, "api", types.UndeclaredSeedingFile, "seeded by LICENSE")
	s.EmitShardTotal(ctx, "1", 4, time.Second)
	s.EmitDetach(ctx, "inv1", DetachQueued, 0)
	checkOutputOverlap(ctx, []cache.Step{
		{ProjectPath: "a", Target: "build", Outputs: []string{"dist/**"}},
		{ProjectPath: "b", Target: "build", Outputs: []string{"dist/**"}},
	}, s)
	require.NoError(t, s.Close())

	assert.Equal(t, []any{
		report.RunScope{Label: "api", Source: "magusfile"},
		report.RunCharms{Charms: "rw"},
		report.RunCache{Tier: "local", Mode: "read+write"},
		report.RunBase{Base: "git diff vs main"},
		report.Notice{Level: slog.LevelWarn, Code: string(types.AffectedSetUncomputable), Message: "full build"},
		report.DiagnosticEmitted{Unit: "api", Code: string(types.UndeclaredSeedingFile), Message: "seeded by LICENSE"},
		report.ShardTotal{Shard: "1", NShards: 4, DurationMs: 1000},
		report.RunDetach{Invocation: "inv1", State: string(DetachQueued)},
		report.OutputOverlapDetected{ProjectA: "a", ProjectB: "b", Target: "build", Overlapping: []string{"dist/**"}},
	}, enc.got)
	assert.True(t, enc.closed)
	assert.Equal(t, types.NoopObserver{}, s.GraphObserver(), "a format that records nothing observes nothing")
}

// Misconfiguration is refused where the sink is built, not discovered as a run that
// reported nothing.
func TestNewSinkRefusesMisconfiguration(t *testing.T) {
	_, err := NewSink("bogus", io.Discard, io.Discard)
	assert.ErrorContains(t, err, `"bogus"`)
	_, err = NewSink(FormatJSONL, nil, io.Discard)
	assert.Error(t, err)
	_, err = NewSink(FormatText, io.Discard, nil)
	assert.Error(t, err)
	_, err = NewSink(FormatJSONL, io.Discard, io.Discard, WithSinkFilter([]string{"+"}))
	assert.Error(t, err)
}

// An event the text encoder has no prose for still reaches the reader, marked, rather
// than vanishing: the classification test keeps real events off this path.
func TestTextSinkPrintsAnUnrenderedEvent(t *testing.T) {
	var out bytes.Buffer
	textEncoder{term: &plainTerminal{w: &out}}.encode(t.Context(), report.TargetResult{Project: "api"})
	assert.Contains(t, out.String(), unrenderedEvent)
}

// Each fact has one wording, pinned here where it is rendered.
func TestTextSinkProse(t *testing.T) {
	render := func(e any) string {
		var out bytes.Buffer
		s, err := NewSink(FormatText, io.Discard, &out)
		require.NoError(t, err)
		s.emit(t.Context(), e)
		return out.String()
	}
	for _, c := range []struct {
		event any
		want  string
	}{
		{report.RunScope{Label: "api", Source: "cwd"}, "projects: api (cwd)\n"},
		{report.RunScope{Label: "api"}, "projects: api\n"},
		{report.RunCharms{Charms: "rw"}, "charms: rw\n"},
		{report.RunCharms{}, "charms: (none)\n"},
		{report.RunCache{Tier: "gha + local", Mode: "read-only"}, "cache: gha + local (read-only)\n"},
		{report.RunCache{}, ""},
		{report.RunBase{Base: "git diff vs main"}, "base: git diff vs main\n"},
		{report.RunBase{}, ""},
		{report.RunDry{}, "dry run: commands shown, not executed\n"},
		// A planned step carries the same repro command an executed one does.
		{report.RunStep{Label: "magus", Project: ".", Target: "ci", Status: "dry"}, "[dry] magus\nmagus run ci .\n"},
		{report.RunStep{Label: "magus", Target: "lint", Status: "pass", DurationMs: 3100}, "  [pass] magus lint (3.1s)\n"},
		{report.RunStep{Label: "magus", Target: "test", Status: "fail", DurationMs: 5000}, "  [fail] magus test (5.0s)\n"},
		// An advisory member's failure is not the composite's.
		{report.RunStep{Label: "magus", Target: "security", Status: "advisory", DurationMs: 5000}, "  [advisory] magus security (5.0s)\n"},
		{report.RunSummary{Hits: 3, Misses: 1, DurationMs: 2000}, "summary: 3 cached, 1 ran, 0 failed (2.0s)\n"},
		// Nothing executed, so the dry footer says what would run; the plural is real.
		{report.RunSummary{Dry: true, Planned: 3, DurationMs: 2}, "summary: dry run, 3 targets would run (2ms)\n"},
		{report.RunSummary{Dry: true, Planned: 1, DurationMs: 1}, "summary: dry run, 1 target would run (1ms)\n"},
		// The zero case is the point: a configured remote that did nothing says so.
		{report.RunRemote{}, "remote: 0 restored, 0 published, 0 failed (0 B down, 0 B up)\n"},
		{report.RunDetach{Invocation: "inv1", State: string(DetachQueued)}, "magus: detached as inv1\n  read it with: magus query invocation inv1\n"},
		{report.RunDetach{State: string(DetachCoalesced)}, "magus: the daemon is already running this exact command; not queued twice\n"},
		// Debug detail stays out of a default run.
		{report.DiagnosticEmitted{Unit: "api", Code: "MGS1028"}, ""},
		{report.ShardTotal{Shard: "1", NShards: 2}, ""},
	} {
		assert.Equal(t, c.want, render(c.event), "%#v", c.event)
	}
}

// -q and -s raise the level past progress, never past what explains a failure: notices,
// diagnostics and a detached run's outcome still print.
func TestTextSinkSilentKeepsWhatExplainsAFailure(t *testing.T) {
	t.Cleanup(func() { interactive.SetHintsEnabled(true) })
	interactive.SetHintsEnabled(true)
	var out bytes.Buffer
	s, err := NewSink(FormatText, io.Discard, &out, WithSinkLevel(slog.LevelError))
	require.NoError(t, err)
	ctx := t.Context()
	s.EmitScope(ctx, "api", "")
	s.emit(ctx, report.RunSummary{Hits: 1})
	s.EmitNotice(ctx, slog.LevelWarn, "", "silent-run notice")
	s.emit(ctx, report.DeterminismMismatch{Project: "api", Target: "build"})
	s.EmitDetach(ctx, "inv1", DetachFailed, time.Second)

	got := out.String()
	assert.NotContains(t, got, "projects:")
	assert.NotContains(t, got, "summary:")
	assert.Contains(t, got, "hint: silent-run notice\n")
	assert.Contains(t, got, "["+string(types.NondeterministicOutput)+"] non-deterministic output")
	assert.Contains(t, got, "magus: inv1 failed (1.0s)\n")
}

// The notice channel is the hint one in text: the run that pays for a notice is often a
// gate run with -s, and hints are what -s still bubbles up; hints.enabled turns them off.
// The code is the notice's field, prefixed in text.
func TestTextSinkRendersANoticeAsAHint(t *testing.T) {
	t.Cleanup(func() { interactive.SetHintsEnabled(true) })
	interactive.SetHintsEnabled(true)
	var buf bytes.Buffer
	s, err := NewSink(FormatText, io.Discard, &buf)
	require.NoError(t, err)
	s.EmitNotice(t.Context(), slog.LevelWarn, "MGS1028", "notice-one")
	assert.Equal(t, "hint: [MGS1028] notice-one\n", buf.String())

	buf.Reset()
	interactive.SetHintsEnabled(false)
	s.EmitNotice(t.Context(), slog.LevelWarn, "", "notice-two")
	assert.Empty(t, buf.String())
}

// A JSONL sink records headers on stdout and notices on stderr, one record each, and
// never as prose.
func TestJSONLSinkRecordsHeaders(t *testing.T) {
	var stdout, stderr bytes.Buffer
	s, err := NewSink(FormatJSONL, &stdout, &stderr)
	require.NoError(t, err)
	s.EmitScope(t.Context(), "api", "magusfile")
	s.EmitBase(t.Context(), "git diff vs main")
	s.EmitNotice(t.Context(), slog.LevelWarn, types.AffectedSetUncomputable, "full build")
	s.EmitDiagnostic(t.Context(), "api", types.UndeclaredSeedingFile, "seeded by LICENSE")
	require.NoError(t, s.Close())

	assert.Equal(t, `{"schema":5,"type":"run.scope","label":"api","source":"magusfile"}
{"schema":5,"type":"run.base","base":"git diff vs main"}
{"schema":5,"type":"run.diagnostic","unit":"api","code":"`+string(types.UndeclaredSeedingFile)+`","message":"seeded by LICENSE"}
`, stdout.String())
	assert.Equal(t, `{"schema":5,"type":"run.notice","level":"warn","code":"`+string(types.AffectedSetUncomputable)+`","msg":"full build"}
`, stderr.String())
}

// One writer under both streams takes every record on the record stream, rather than
// from two goroutines at once.
func TestJSONLSinkOverOneWriter(t *testing.T) {
	var both bytes.Buffer
	s, err := NewSink(FormatJSONL, &both, &both)
	require.NoError(t, err)
	s.EmitScope(t.Context(), "api", "")
	s.EmitNotice(t.Context(), slog.LevelInfo, "", "same stream")
	require.NoError(t, s.Close())
	assert.Equal(t, `{"schema":5,"type":"run.scope","label":"api"}
{"schema":5,"type":"run.notice","level":"info","msg":"same stream"}
`, both.String())
}

// report.filter narrows the record stream; the notices on stderr are not records a
// consumer asked to filter.
func TestJSONLSinkFilter(t *testing.T) {
	var stdout, stderr bytes.Buffer
	s, err := NewSink(FormatJSONL, &stdout, &stderr, WithSinkFilter([]string{report.TypeShardTotal}))
	require.NoError(t, err)
	s.EmitScope(t.Context(), "api", "")
	s.EmitShardTotal(t.Context(), "2", 4, 1500*time.Millisecond)
	s.EmitNotice(t.Context(), slog.LevelInfo, "", "kept")
	require.NoError(t, s.Close())
	assert.Equal(t, `{"schema":5,"type":"shard.total","shard":"2","n_shards":4,"duration_ms":1500}
`, stdout.String())
	assert.Contains(t, stderr.String(), `"msg":"kept"`)
}

// gatedWriter blocks every Write until open is closed, standing in for a reader that
// falls behind.
type gatedWriter struct {
	open chan struct{}
	buf  bytes.Buffer
}

func (g *gatedWriter) Write(p []byte) (int, error) {
	<-g.open
	return g.buf.Write(p)
}

// A reader that falls behind slows the run down; it never loses a record. Past the
// queue's 4096 slots a dropping writer would lose events, and a failed target's only
// run.target.result could be one of them.
func TestJSONLSinkNeverDropsARecord(t *testing.T) {
	dst := &gatedWriter{open: make(chan struct{})}
	s, err := NewSink(FormatJSONL, dst, io.Discard)
	require.NoError(t, err)
	const n = 10_000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range n {
			s.emit(context.Background(), report.RunStep{Label: "x", Status: "pass"})
		}
	}()
	time.Sleep(50 * time.Millisecond)
	close(dst.open)
	<-done
	require.NoError(t, s.Close())
	assert.Equal(t, n, strings.Count(dst.buf.String(), "\n"))
	assert.NotContains(t, dst.buf.String(), "dropped")
}

// MGS4002 and MGS4004 reach a text reader as the coded prose and a JSONL reader as one
// record each, and neither ever as the other.
func TestRaceDiagnosticsFollowTheSink(t *testing.T) {
	steps := []cache.Step{
		{ProjectPath: "a", Target: "build", Outputs: []string{"dist/**"}},
		{ProjectPath: "b", Target: "build", Outputs: []string{"dist/**"}},
	}
	consumer := &types.Project{Path: "consumer", Dir: "/ws/consumer", Sources: []string{"**/*.go"}}
	written := map[string][]string{"producer": {"/ws/consumer/generated.go"}}

	var text bytes.Buffer
	ts, err := NewSink(FormatText, io.Discard, &text)
	require.NoError(t, err)
	checkOutputOverlap(t.Context(), steps, ts)
	checkMissingDependencies(t.Context(), []*types.Project{consumer}, nil, written, ".", ts)
	assert.Contains(t, text.String(), "["+string(types.OutputOverlapDetected)+"] declared output overlap")
	assert.Contains(t, text.String(), "["+string(types.MissingDependencyDetected)+"] potential undeclared dependency")

	var buf bytes.Buffer
	js, err := NewSink(FormatJSONL, &buf, io.Discard)
	require.NoError(t, err)
	checkOutputOverlap(t.Context(), steps, js)
	checkMissingDependencies(t.Context(), []*types.Project{consumer}, nil, written, ".", js)
	require.NoError(t, js.Close())
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], `"type":"`+report.TypeOutputOverlapDetected+`"`)
	assert.Contains(t, lines[1], `"type":"`+report.TypeMissingDependency+`"`)
}
