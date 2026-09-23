package magus

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sinkEvents is one sample of every event a run emits through its sink.
var sinkEvents = []any{
	report.RunScope{Label: "api", Source: "magusfile"},
	report.RunCharms{Charms: "rw"},
	report.RunCache{Tier: "local", Mode: "read+write"},
	report.RunBase{Base: "git diff vs main"},
	report.RunDry{},
	report.RunStep{Label: "api", Project: "api", Target: "build", Status: "dry"},
	report.RunSummary{Hits: 1, Misses: 1, DurationMs: 20},
	report.RunRemote{Hits: 1, Failures: 1},
	report.Notice{Level: slog.LevelWarn, Code: "MGS1028", Message: "every-event notice"},
	report.DiagnosticEmitted{Unit: "api", Code: "MGS1028", Message: "seeded by LICENSE"},
	report.DeterminismMismatch{Project: "api", Target: "build", DifferingPaths: []string{"dist/a"}},
	report.DeterminismUnchecked{Project: "api", Target: "build", Globs: "dist/**"},
	report.MissingDependency{Consumer: "web", Producer: "api", Path: "web/gen.go", Target: "."},
	report.OutputOverlapDetected{ProjectA: "api", ProjectB: "web", Target: "build", Overlapping: []string{"dist/**"}},
}

// recordedOutsideTheSink are the registered events a run records straight onto its
// report writer, never through a sink: per-target results, graph and volatility
// telemetry, race findings, and the lock's decisions, which are made before the run's
// context exists.
var recordedOutsideTheSink = []any{
	report.TargetResult{}, report.GraphBuild{}, report.GraphQuery{}, report.GraphError{},
	report.VolatilityCall{}, report.ShardTotal{}, report.RaceDetected{},
	report.LockWait{}, report.LockReleased{}, report.LockSuperseded{}, report.LockSupersedeUnanswered{},
}

// Every event type is either rendered by both sinks or recorded outside them, so a new
// report type cannot reach textSink's default and be printed as a raw struct.
func TestEveryRegisteredEventIsClassified(t *testing.T) {
	classified := map[reflect.Type]bool{}
	for _, e := range append(append([]any(nil), sinkEvents...), recordedOutsideTheSink...) {
		classified[reflect.TypeOf(e)] = true
	}
	for _, rt := range report.RegisteredTypes() {
		assert.True(t, classified[rt], "%s is neither in sinkEvents nor recordedOutsideTheSink", rt)
	}
}

// Each sink event renders in text as prose or a debug record, never as the unrendered
// fallback, and in JSONL as exactly one record of its own type.
func TestEverySinkEventRendersInBothSinks(t *testing.T) {
	t.Cleanup(func() { interactive.SetHintsEnabled(true) })
	interactive.SetHintsEnabled(true)
	var logged bytes.Buffer
	c, err := cache.Open(t.Context(), t.TempDir(),
		cache.WithLogger(slog.New(cache.NewPrettyHandler(&logged, slog.LevelDebug))))
	require.NoError(t, err)

	for _, e := range sinkEvents {
		t.Run(reflect.TypeOf(e).Name(), func(t *testing.T) {
			logged.Reset()
			var out, debug bytes.Buffer
			text := textSink{cache: c, out: &out, log: slog.New(slog.NewTextHandler(&debug, &slog.HandlerOptions{Level: slog.LevelDebug}))}
			text.emit(t.Context(), e)
			rendered := logged.String() + out.String() + debug.String()
			assert.NotEmpty(t, rendered, "text rendered nothing")
			assert.NotContains(t, rendered, unrenderedEvent)

			var buf bytes.Buffer
			w := report.NewWriter(&buf, report.WithBlockOnFull())
			jsonlSink{w: w}.emit(t.Context(), e)
			require.NoError(t, w.Close())
			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			require.Len(t, lines, 1)
			assert.NotContains(t, lines[0], `"type":"`+report.TypeNotice+`","level":"error"`, "recorded as a fallback notice")
		})
	}
}

// An event textSink has no prose for still reaches the reader, marked, rather than
// vanishing: the classification test is what keeps real events off this path.
func TestTextSinkPrintsAnUnrenderedEvent(t *testing.T) {
	var out bytes.Buffer
	textSink{out: &out}.emit(t.Context(), report.ShardTotal{Shard: "1"})
	assert.Contains(t, out.String(), unrenderedEvent)
}

// The notice channel is the hint one in text: the run that pays for a notice is often a
// gate run with -s, and hints are what -s still bubbles up; hints.enabled turns them off.
// The code is the notice's field, prefixed in text.
func TestTextSinkRendersANoticeAsAHint(t *testing.T) {
	t.Cleanup(func() { interactive.SetHintsEnabled(true) })
	interactive.SetHintsEnabled(true)
	var buf bytes.Buffer
	s := textSink{out: &buf}
	s.emit(t.Context(), report.Notice{Level: slog.LevelWarn, Code: "MGS1028", Message: "notice-one"})
	assert.Equal(t, "hint: [MGS1028] notice-one\n", buf.String())

	buf.Reset()
	interactive.SetHintsEnabled(false)
	s.emit(t.Context(), report.Notice{Level: slog.LevelWarn, Message: "notice-two"})
	assert.Empty(t, buf.String())
}

// TestTextSinkHeadersOnAnOpenWorkspace: the text sink's header emitters are no-ops on an
// Inspect workspace and reach the cache logger on an opened one. Nothing observable
// comes back, so what this pins is that the live-cache path runs at all: the
// Inspect path is covered by TestCacheOperationsWithoutOpenCache.
func TestTextSinkHeadersOnAnOpenWorkspace(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)
	ctx := t.Context()
	s := m.TextSink()

	assert.NotPanics(t, func() {
		s.EmitScope(ctx, "api", "magusfile")
		s.EmitCharms(ctx, "rw")
		s.EmitCache(ctx)
		s.EmitBase(ctx, "git diff vs main")
	})
}

// A JSONL sink records the same headers as typed events, and nothing reaches the cache
// logger: the caller parsing the stream meets one record per header.
func TestJSONLSinkRecordsHeaders(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)
	var buf bytes.Buffer
	rw, err := NewReportWriter(&buf, nil)
	require.NoError(t, err)
	s, err := m.JSONLSink(rw)
	require.NoError(t, err)
	s.EmitScope(t.Context(), "api", "magusfile")
	s.EmitBase(t.Context(), "git diff vs main")
	s.EmitNotice(t.Context(), slog.LevelWarn, types.AffectedSetUncomputable, "full build")
	s.EmitDiagnostic(t.Context(), "api", types.UndeclaredSeedingFile, "seeded by LICENSE")
	require.NoError(t, rw.Close())

	assert.Equal(t, `{"schema":5,"type":"run.scope","label":"api","source":"magusfile"}
{"schema":5,"type":"run.base","base":"git diff vs main"}
{"schema":5,"type":"run.notice","level":"warn","code":"`+string(types.AffectedSetUncomputable)+`","msg":"full build"}
{"schema":5,"type":"run.diagnostic","unit":"api","code":"`+string(types.UndeclaredSeedingFile)+`","message":"seeded by LICENSE"}
`, buf.String())
}

// A JSONL sink with no writer is a misconfiguration, refused where it is built rather
// than discovered as a run that recorded nothing.
func TestJSONLSinkRefusesANilWriter(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)
	_, err := m.JSONLSink(nil)
	assert.Error(t, err)
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
	checkOutputOverlap(t.Context(), steps, textSink{out: &text})
	checkMissingDependencies(t.Context(), []*types.Project{consumer}, nil, written, ".", textSink{out: &text})
	assert.Contains(t, text.String(), "["+string(types.OutputOverlapDetected)+"] declared output overlap")
	assert.Contains(t, text.String(), "["+string(types.MissingDependencyDetected)+"] potential undeclared dependency")

	var buf bytes.Buffer
	w := report.NewWriter(&buf, report.WithBlockOnFull())
	checkOutputOverlap(t.Context(), steps, jsonlSink{w: w})
	checkMissingDependencies(t.Context(), []*types.Project{consumer}, nil, written, ".", jsonlSink{w: w})
	require.NoError(t, w.Close())
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], `"type":"`+report.TypeOutputOverlapDetected+`"`)
	assert.Contains(t, lines[1], `"type":"`+report.TypeMissingDependency+`"`)
}
