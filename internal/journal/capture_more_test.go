package journal

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithStepAndStepFromContext(t *testing.T) {
	_, _, ok := StepFromContext(context.Background())
	assert.False(t, ok, "a bare context is not inside a captured step")

	ctx := WithStep(context.Background(), "api", "build")
	project, target, ok := StepFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "api", project)
	assert.Equal(t, "build", target)
}

// recordWith builds a slog.Record carrying (or not carrying) a journal Event.
func recordWith(attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0)
	r.AddAttrs(attrs...)
	return r
}

func TestEventFromRecord(t *testing.T) {
	want := Event{Kind: KindOutput, Stream: "stdout", Text: "hello"}
	got, ok := EventFromRecord(recordWith(slog.Any(eventAttr, want)))
	require.True(t, ok)
	assert.Equal(t, want, got)

	// A record with no event attr, or a wrongly-typed one, reports absent.
	_, ok = EventFromRecord(recordWith())
	assert.False(t, ok)
	_, ok = EventFromRecord(recordWith(slog.String(eventAttr, "not-an-event")))
	assert.False(t, ok)
}

func TestFanout_DeliversToEveryChildAndDerives(t *testing.T) {
	var a, b bytes.Buffer
	f := fanout{NewFileHandler(&a), NewFileHandler(&b)}

	assert.True(t, f.Enabled(context.Background(), slog.LevelInfo))
	// WithAttrs/WithGroup return a fanout of the same width, still wired to the children.
	require.Len(t, f.WithAttrs(nil).(fanout), 2)
	require.Len(t, f.WithGroup("g").(fanout), 2)

	ev := Event{Kind: KindResult, Status: "pass", Ref: "out1a2b3c"}
	require.NoError(t, f.Handle(context.Background(), recordWith(slog.Any(eventAttr, ev))))
	for _, h := range f {
		h.(*FileHandler).Flush()
	}
	assert.Contains(t, a.String(), `"status":"pass"`)
	assert.Contains(t, b.String(), `"ref":"out1a2b3c"`)
}

func TestFileHandler_WritesEventLinesOnly(t *testing.T) {
	var buf bytes.Buffer
	h := NewFileHandler(&buf)
	assert.True(t, h.Enabled(context.Background(), slog.LevelInfo))
	assert.Same(t, h, h.WithAttrs(nil))
	assert.Same(t, h, h.WithGroup("g"))

	// A record without an event attr writes nothing.
	require.NoError(t, h.Handle(context.Background(), recordWith()))
	// A record with an event writes exactly one JSONL line.
	require.NoError(t, h.Handle(context.Background(), recordWith(slog.Any(eventAttr, Event{Kind: KindStarted, Text: "go"}))))
	h.Flush()
	assert.Equal(t, 1, strings.Count(buf.String(), "\n"))
	assert.Contains(t, buf.String(), `"text":"go"`)
}

func TestDiscardHandler_NeverEnabled(t *testing.T) {
	var h slog.Handler = discardHandler{}
	assert.False(t, h.Enabled(context.Background(), slog.LevelError))
	assert.NoError(t, h.Handle(context.Background(), recordWith()))
	assert.IsType(t, discardHandler{}, h.WithAttrs(nil))
	assert.IsType(t, discardHandler{}, h.WithGroup("g"))
}
