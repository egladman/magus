package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

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
