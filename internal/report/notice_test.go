package report

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A log record no typed event converts still reaches a -o jsonl reader as a run.notice
// envelope, with its fields under attrs so none can collide with the envelope's own.
func TestNoticeHandlerWritesNoticeEnvelopes(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(NewNoticeHandler(&buf, slog.LevelInfo))
	log.Debug("cache.dropped")
	log.With("project", "api").WithGroup("remote").Warn("cache.warn",
		slog.String("msg", "push failed"), slog.Int("failures", 2), slog.Any("err", errors.New("unreachable")))
	log.Info("cache.notice")

	assert.Equal(t, `{"schema":5,"type":"run.notice","level":"warn","msg":"cache.warn","attrs":{"project":"api","remote":{"err":"unreachable","failures":2,"msg":"push failed"}}}
{"schema":5,"type":"run.notice","level":"info","msg":"cache.notice"}
`, buf.String())
}

// Levels below debug (magus's trace) and past error read as the documented names, never
// slog's "DEBUG-4".
func TestNoticeLevelsAreTheDocumentedNames(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := slog.New(NewNoticeHandler(&buf, slog.LevelDebug-4))
	log.Log(context.Background(), slog.LevelDebug-4, "trace")
	log.Log(context.Background(), slog.LevelError+4, "fatal")
	assert.Equal(t, `{"schema":5,"type":"run.notice","level":"debug","msg":"trace"}
{"schema":5,"type":"run.notice","level":"error","msg":"fatal"}
`, buf.String())
}

// countingWriter records each Write it receives.
type countingWriter struct{ writes []string }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes = append(c.writes, string(p))
	return len(p), nil
}

// A record reaches the stream in one Write however long it is, so a reader sharing the
// stream can never see half of it beside another writer's line.
func TestNoticeHandlerWritesARecordInOneWrite(t *testing.T) {
	t.Parallel()
	var w countingWriter
	slog.New(NewNoticeHandler(&w, slog.LevelInfo)).Info(strings.Repeat("x", 16<<10))
	require.Len(t, w.writes, 1)
	assert.True(t, strings.HasSuffix(w.writes[0], "\"}\n"))
}

// An attribute the encoder refuses costs the record its attributes, not its existence.
func TestNoticeHandlerKeepsARecordItCannotEncode(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	slog.New(NewNoticeHandler(&buf, slog.LevelInfo)).Warn("cache.warn", slog.Any("fn", func() {}))
	line := buf.String()
	assert.True(t, strings.HasPrefix(line, `{"schema":5,"type":"run.notice","level":"warn","msg":"cache.warn","attrs":{"encode_error":`), line)
}

// Output written outside any capture becomes one info record per line, past the
// handler's level, with a partial line held until its newline arrives.
func TestLineNoticesRecordsEachLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewLineNotices(context.Background(), NewNoticeHandler(&buf, slog.LevelError), "stdout")
	_, _ = fmt.Fprint(w, "one\ntw")
	_, _ = fmt.Fprint(w, "o\n")
	assert.Equal(t, `{"schema":5,"type":"run.notice","level":"info","msg":"one","attrs":{"stream":"stdout"}}
{"schema":5,"type":"run.notice","level":"info","msg":"two","attrs":{"stream":"stdout"}}
`, buf.String())
}
