package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/log/audience"
	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCtxAttrHandlerInjectsDir verifies the working directory carried on the
// context is attached to records, an explicit "dir" is not clobbered, and a
// context without a cwd is left untouched.
func TestCtxAttrHandlerInjectsDir(t *testing.T) {
	newLogger := func(buf *bytes.Buffer) *slog.Logger {
		return slog.New(dirHandler{slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})})
	}

	t.Run("injects ctx cwd", func(t *testing.T) {
		var buf bytes.Buffer
		ctx := std.WithCwd(context.Background(), "/ws/api")
		newLogger(&buf).InfoContext(ctx, "build")
		if got := buf.String(); !strings.Contains(got, `dir=/ws/api`) {
			t.Fatalf("expected dir attr, got: %s", got)
		}
	})

	t.Run("explicit dir wins", func(t *testing.T) {
		var buf bytes.Buffer
		ctx := std.WithCwd(context.Background(), "/ws/api")
		newLogger(&buf).InfoContext(ctx, "exec", "dir", "/ws/api/sub")
		got := buf.String()
		if !strings.Contains(got, `dir=/ws/api/sub`) {
			t.Fatalf("explicit dir should be kept, got: %s", got)
		}
		if strings.Count(got, "dir=") != 1 {
			t.Fatalf("expected exactly one dir attr, got: %s", got)
		}
	})

	t.Run("no cwd is a no-op", func(t *testing.T) {
		var buf bytes.Buffer
		newLogger(&buf).InfoContext(context.Background(), "build")
		if strings.Contains(buf.String(), "dir=") {
			t.Fatalf("did not expect a dir attr, got: %s", buf.String())
		}
	})
}

// TestDirHandlerRendersComponentByFormat pins how a record's component reaches
// each default handler the CLI installs: pretty leads the message with it, as
// the tag in the message text used to, and text keeps it an attribute.
func TestDirHandlerRendersComponentByFormat(t *testing.T) {
	ctx := std.WithCwd(context.Background(), "/ws/api")

	var pretty bytes.Buffer
	slog.New(dirHandler{cache.NewPrettyHandler(&pretty, slog.LevelInfo)}).
		With(attr.ComponentKey, "knowledge").
		WarnContext(ctx, "cannot decode symbol index", "index", "a.scip")
	assert.Equal(t, "[warn] knowledge: cannot decode symbol index index=a.scip\n", pretty.String())

	var text bytes.Buffer
	slog.New(dirHandler{slog.NewTextHandler(&text, nil)}).
		With(attr.ComponentKey, "knowledge").
		WarnContext(ctx, "cannot decode symbol index", "index", "a.scip")
	assert.Contains(t, text.String(), `msg="cannot decode symbol index" component=knowledge index=a.scip dir=/ws/api`)
}

// TestExpandVerbosityArgsStopsAtSeparator pins the transformer half of the "--"
// guard: expandVerbosityArgs builds the master argv startup() parses (main.go's
// startup calls extractVerbosityCount(args), which itself calls this), so a token
// meant verbatim for a forwarded tool (e.g. `-vvv` as a literal positional the tool
// expects) must survive past "--" unchanged rather than being expanded into a run
// of "-v" tokens or dropped.
func TestExpandVerbosityArgsStopsAtSeparator(t *testing.T) {
	got := expandVerbosityArgs([]string{"run", "-vv", "build", "--", "-vvv", "--other"})
	want := []string{"run", "-v", "-v", "build", "--", "-vvv", "--other"}
	if len(got) != len(want) {
		t.Fatalf("expandVerbosityArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expandVerbosityArgs = %v, want %v", got, want)
		}
	}
}

// TestExpandVerbosityArgsNoSeparator confirms the ordinary expansion still works
// when there is no "--" at all.
func TestExpandVerbosityArgsNoSeparator(t *testing.T) {
	got := expandVerbosityArgs([]string{"run", "-vvv", "build"})
	want := []string{"run", "-v", "-v", "-v", "build"}
	if len(got) != len(want) {
		t.Fatalf("expandVerbosityArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expandVerbosityArgs = %v, want %v", got, want)
		}
	}
}

func TestEffectiveLevelQuietWinsOverVerbose(t *testing.T) {
	assert.Equal(t, slog.LevelError, effectiveLevel(0, true))
	assert.Equal(t, slog.LevelError, effectiveLevel(3, true))
	assert.Equal(t, slog.LevelInfo, effectiveLevel(0, false))
	assert.Equal(t, slog.LevelDebug, effectiveLevel(1, false))
	assert.Equal(t, slog.LevelDebug, effectiveLevel(2, false))
	assert.Equal(t, config.LevelTrace, effectiveLevel(3, false))
}

// levelName writes back into log.level, so it spells levels the way that setting's
// validator accepts them.
func TestLevelNameSpellsTheConfigVocabulary(t *testing.T) {
	assert.Equal(t, "trace", levelName(config.LevelTrace))
	assert.Equal(t, "debug", levelName(slog.LevelDebug))
	assert.Equal(t, "info", levelName(slog.LevelInfo))
	assert.Equal(t, "error", levelName(slog.LevelError))
}

// log.level from yaml, env or --log-level governs the process logger; only a verbosity
// flag overrides it.
func TestApplyDisplayHonorsConfiguredLevel(t *testing.T) {
	savedCfg, savedGlobal, savedLogger := globalCfg, global, slog.Default()
	t.Cleanup(func() {
		globalCfg, global = savedCfg, savedGlobal
		slog.SetDefault(savedLogger)
	})

	globalCfg, global = config.Config{Log: config.Log{Level: "debug", Format: "text"}}, globalFlags{}
	applyDisplay()
	assert.Equal(t, "debug", globalCfg.Log.Level)
	assert.True(t, slog.Default().Enabled(context.Background(), slog.LevelDebug))

	global.quiet = true
	applyDisplay()
	assert.Equal(t, "error", globalCfg.Log.Level)
	assert.False(t, slog.Default().Enabled(context.Background(), slog.LevelWarn))
}

// The audience filter sits on the display alone: the run log's capture chain keeps the
// reasoning and the short wait an agent's display drops.
func TestApplyDisplayAudienceFiltersTheDisplayNotTheCapture(t *testing.T) {
	savedCfg, savedGlobal, savedLogger, savedStderr := globalCfg, global, slog.Default(), os.Stderr
	t.Cleanup(func() {
		globalCfg, global = savedCfg, savedGlobal
		slog.SetDefault(savedLogger)
		os.Stderr = savedStderr
	})
	display, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = display.Close() })
	os.Stderr = display

	globalCfg, global = config.Config{Log: config.Log{Format: "text", Audience: "agent"}}, globalFlags{}
	applyDisplay()

	ctx := context.Background()
	reasoned := slog.NewRecord(time.Now(), slog.LevelWarn, "index stale", 0)
	reasoned.AddAttrs(attr.Why("stale symbols mislead"))
	wait := slog.NewRecord(time.Now(), slog.LevelInfo, "waiting for the broker", 0)
	wait.AddAttrs(attr.Elapsed(5 * time.Second))

	capture := &journalRecordSink{}
	for _, r := range []slog.Record{reasoned, wait} {
		require.NoError(t, slog.Default().Handler().Handle(ctx, r))
		require.NoError(t, journal.NewLogger(capture).Handler().Handle(ctx, r))
	}

	shown, err := os.ReadFile(display.Name())
	require.NoError(t, err)
	assert.Contains(t, string(shown), `msg="index stale"`)
	assert.NotContains(t, string(shown), "why=")
	assert.NotContains(t, string(shown), "waiting for the broker")

	require.Len(t, capture.records, 2)
	var kept []string
	for _, r := range capture.records {
		r.Attrs(func(a slog.Attr) bool {
			kept = append(kept, a.Key)
			return true
		})
	}
	assert.Equal(t, []string{attr.WhyKey, attr.ElapsedKey}, kept)
}

// -o jsonl hands a parser every notice whole, so no audience filter applies. Its notices
// go through an encoder bound to the process's stderr at init, which a test cannot
// redirect, so this pins the installed chain instead of the bytes.
func TestApplyDisplayAudienceLeavesJSONLNoticesWhole(t *testing.T) {
	savedCfg, savedGlobal, savedLogger := globalCfg, global, slog.Default()
	t.Cleanup(func() {
		globalCfg, global = savedCfg, savedGlobal
		slog.SetDefault(savedLogger)
	})
	filtered := reflect.TypeOf(audience.Wrap(slog.DiscardHandler, audience.Agent, false))

	globalCfg, global = config.Config{Log: config.Log{Format: "text", Audience: "agent"}}, globalFlags{}
	applyDisplay()
	require.IsType(t, dirHandler{}, slog.Default().Handler())
	assert.Equal(t, filtered, reflect.TypeOf(slog.Default().Handler().(dirHandler).Handler), "text display")

	global.output = string(FormatJSONL)
	applyDisplay()
	assert.NotEqual(t, filtered, reflect.TypeOf(slog.Default().Handler().(dirHandler).Handler), "-o jsonl notices")
}
