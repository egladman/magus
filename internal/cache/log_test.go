package cache

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/interactive"
)

// buildRecord creates a slog.Record with the given message and attrs.
func buildRecord(msg string, attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Now(), slog.LevelInfo, msg, 0)
	r.AddAttrs(attrs...)
	return r
}

// newTestHandler returns a prettyHandler that writes to a bytes.Buffer
// in non-TTY (plain) mode — suitable for output assertions in tests.
func newTestHandler(buf *bytes.Buffer) *prettyHandler {
	return &prettyHandler{w: buf} // fd nil → non-TTY
}

// TestPrettyHandlerPlainOutput verifies the plain ([cache] prefix) output
// for every recognised message key.
func TestPrettyHandlerPlainOutput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		record      slog.Record
		mustContain string
	}{
		{
			buildRecord(
				"cache.hit",
				slog.String("project", "api"),
				slog.Int64("duration", int64(42*time.Millisecond)),
				slog.String("hash", "abc123"),
			),
			"[cache] hit  api",
		},
		{
			buildRecord(
				"cache.miss",
				slog.String("project", "web/studio"),
				slog.Int64("duration", int64(80*time.Millisecond)),
			),
			"[cache] miss web/studio",
		},
		{
			buildRecord(
				"cache.error",
				slog.String("project", "api"),
				slog.Int64("duration", int64(5*time.Millisecond)),
				slog.String("error", "build failed"),
			),
			"[cache] error api",
		},
		{
			buildRecord(
				"cache.summary",
				slog.Int("hits", 3),
				slog.Int("misses", 1),
				slog.Int("errors", 0),
				slog.Int64("elapsed", int64(2*time.Second)),
			),
			"3 hit, 1 miss, 0 error",
		},
		{
			buildRecord(
				"cache.scope",
				slog.String("label", "api"),
				slog.String("source", "cwd"),
			),
			"[scope] api (cwd)",
		},
		{
			buildRecord(
				"cache.warn",
				slog.String("msg", "gc: corrupt manifest foo.json: unexpected EOF"),
			),
			"gc: corrupt manifest foo.json",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.record.Message, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			h := newTestHandler(&buf)
			if err := h.Handle(context.Background(), tc.record); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if got := buf.String(); !strings.Contains(got, tc.mustContain) {
				t.Fatalf("output %q does not contain %q", got, tc.mustContain)
			}
		})
	}
}

// TestPrettyHandlerUnknownMsgSilent verifies that an unrecognised message
// produces no output and no error.
func TestPrettyHandlerUnknownMsgSilent(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	h := newTestHandler(&buf)
	r := buildRecord("some.other.event", slog.String("key", "val"))
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no output for unknown message, got %q", buf.String())
	}
}

// TestPrettyHandlerReproLine verifies that a per-project result prints the
// copy-pasteable `magus run <target> <project>` line, and that the hints toggle
// silences it. Not parallel: it flips the process-wide hints switch.
func TestPrettyHandlerReproLine(t *testing.T) {
	rec := buildRecord(
		"cache.miss",
		slog.String("project", "web/studio"),
		slog.String("target", "test:debug"),
		slog.Int64("duration", int64(80*time.Millisecond)),
	)

	var buf bytes.Buffer
	h := newTestHandler(&buf)
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if want := "magus run test:debug web/studio"; !strings.Contains(buf.String(), want) {
		t.Fatalf("output %q does not contain repro command %q", buf.String(), want)
	}

	// The hints toggle silences it.
	interactive.SetEnabled(false)
	defer interactive.SetEnabled(true)
	buf.Reset()
	if err := h.Handle(context.Background(), rec); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if strings.Contains(buf.String(), "magus run") {
		t.Fatalf("repro command should be suppressed when hints are off, got %q", buf.String())
	}
}

// TestReproTarget verifies the target token the repro line uses: the bare name, or
// name:charm1,charm2 when charms are active (the `magus run` charm-suffix syntax).
func TestReproTarget(t *testing.T) {
	t.Parallel()
	if got := reproTarget(Spec{Target: "test"}); got != "test" {
		t.Errorf("reproTarget(no charms) = %q, want %q", got, "test")
	}
	if got := reproTarget(Spec{Target: "test", Charms: []string{"debug", "race"}}); got != "test:debug,race" {
		t.Errorf("reproTarget(charms) = %q, want %q", got, "test:debug,race")
	}
}

// TestRecordAttrExtraction verifies the attr extraction helpers.
func TestRecordAttrExtraction(t *testing.T) {
	t.Parallel()
	r := buildRecord(
		"test",
		slog.String("project", "web/studio"),
		slog.Int64("duration", int64(123*time.Millisecond)),
		slog.Int("hits", 7),
	)

	if got := recordStr(r, "project"); got != "web/studio" {
		t.Fatalf("recordStr = %q, want %q", got, "web/studio")
	}
	if got := recordStr(r, "missing"); got != "" {
		t.Fatalf("recordStr(missing) = %q, want empty", got)
	}
	if got := recordDur(r, "duration"); got != 123*time.Millisecond {
		t.Fatalf("recordDur = %v, want 123ms", got)
	}
	if got := recordInt(r, "hits"); got != 7 {
		t.Fatalf("recordInt = %d, want 7", got)
	}
}

// TestWithLoggerOption verifies that WithLogger replaces the cache's logger.
func TestWithLoggerOption(t *testing.T) {
	var buf bytes.Buffer
	customHandler := &prettyHandler{w: &buf} // fd nil → non-TTY
	l := slog.New(customHandler)

	dir := t.TempDir()
	t.Setenv("MAGUS_CACHE_MODE", "off")
	c, err := Open(dir, WithLogger(l))
	if err != nil {
		t.Fatal(err)
	}
	if c.log != l {
		t.Fatal("WithLogger did not replace the cache logger")
	}
}
