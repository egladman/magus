package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
)

// TestExpandVerbosityArgs pins the clustered -vv rewrite and its "--" terminator:
// a child's own -vv must reach the child spelled the way it was typed, not split
// into two -v by magus on the way through.
func TestExpandVerbosityArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"cluster expands", []string{"-vv", "build"}, []string{"-v", "-v", "build"}},
		{"single -v untouched", []string{"-v"}, []string{"-v"}},
		{"long flag untouched", []string{"--verbose"}, []string{"--verbose"}},
		{"non-v cluster untouched", []string{"-abc"}, []string{"-abc"}},
		{"passthrough tail untouched", []string{"run", "t", "--", "-vv"}, []string{"run", "t", "--", "-vv"}},
		{"expands before the marker only", []string{"-vv", "run", "t", "--", "-vvv"}, []string{"-v", "-v", "run", "t", "--", "-vvv"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, expandVerbosityArgs(tc.in))
		})
	}
}

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
