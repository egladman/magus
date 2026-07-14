package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileHandlerAppendsJSONL writes events through the file handler and reads them back as
// one JSON object per line - the on-disk schema the store and proto mapper depend on.
func TestFileHandlerAppendsJSONL(t *testing.T) {
	var buf bytes.Buffer
	fh := NewFileHandler(&buf)
	ctx := WithLogger(context.Background(), NewLogger(fh))

	Emit(ctx, Event{Kind: KindOutput, Stream: StreamStdout, Project: "web", Target: "build", Text: "line one"})
	Emit(ctx, Event{Kind: KindResult, Status: StatusPass, Ref: "outabc", DurMs: 5})
	fh.Flush()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	var first Event
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	assert.Equal(t, KindOutput, first.Kind)
	assert.Equal(t, "line one", first.Text)
	assert.Equal(t, "web", first.Project)
	assert.Greater(t, first.Ts, int64(0), "Emit stamps a timestamp")
}

// TestEmitStampsFromContext confirms Emit fills the timestamp and the invocation id from
// ctx when they are unset.
func TestEmitStampsFromContext(t *testing.T) {
	bc := NewBroadcaster()
	ctx := WithInvocationID(WithLogger(context.Background(), NewLogger(bc)), "inv9")
	Emit(ctx, Event{Kind: KindOutput, Text: "hi"})

	got, _, cancel := bc.Subscribe()
	defer cancel()
	require.Len(t, got, 1)
	assert.Equal(t, "inv9", got[0].Inv)
	assert.Greater(t, got[0].Ts, int64(0))
}

// TestEmitNoLoggerIsNoop confirms Emit on a ctx with no capture logger neither panics nor
// does anything (best-effort capture).
func TestEmitNoLoggerIsNoop(t *testing.T) {
	assert.NotPanics(t, func() { Emit(context.Background(), Event{Kind: KindOutput, Text: "x"}) })
}

// TestNewInvocationIDUnique confirms minted ids are unique and prefixed.
func TestNewInvocationIDUnique(t *testing.T) {
	a, b := NewInvocationID(), NewInvocationID()
	assert.NotEqual(t, a, b)
	assert.True(t, strings.HasPrefix(a, "inv"))
}

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
