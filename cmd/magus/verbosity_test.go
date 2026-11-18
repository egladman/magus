package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/log/attr"
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
		ctx := std.WithCurrentWorkingDirectory(context.Background(), "/ws/api")
		newLogger(&buf).InfoContext(ctx, "build")
		if got := buf.String(); !strings.Contains(got, `dir=/ws/api`) {
			t.Fatalf("expected dir attr, got: %s", got)
		}
	})

	t.Run("explicit dir wins", func(t *testing.T) {
		var buf bytes.Buffer
		ctx := std.WithCurrentWorkingDirectory(context.Background(), "/ws/api")
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
	ctx := std.WithCurrentWorkingDirectory(withSecrets(context.Background()), "/ws/api")

	var pretty bytes.Buffer
	slog.New(dirHandler{cache.NewLogHandler(&pretty, "pretty", &slog.HandlerOptions{Level: slog.LevelInfo})}).
		With(attr.ComponentKey, "knowledge").
		WarnContext(ctx, "cannot decode symbol index", "index", "a.scip")
	assert.Equal(t, "[warn] knowledge: cannot decode symbol index index=a.scip\n", pretty.String())

	var text bytes.Buffer
	slog.New(dirHandler{slog.NewTextHandler(&text, nil)}).
		With(attr.ComponentKey, "knowledge").
		WarnContext(ctx, "cannot decode symbol index", "index", "a.scip")
	assert.Contains(t, text.String(), `msg="cannot decode symbol index" component=knowledge index=a.scip dir=/ws/api`)
}
