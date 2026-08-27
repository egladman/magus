package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
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

func TestLevelNameNamesTraceItself(t *testing.T) {
	assert.Equal(t, "trace", levelName(config.LevelTrace))
	assert.Equal(t, "DEBUG", levelName(slog.LevelDebug))
	assert.Equal(t, "INFO", levelName(slog.LevelInfo))
	assert.Equal(t, "ERROR", levelName(slog.LevelError))
}
