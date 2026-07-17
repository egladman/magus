package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/trail"
)

// fakeTel records MCP calls for assertions. It embeds the wide Provider
// interface so only RecordMCPCall needs an implementation; wrap touches no
// other method, so the nil embedded value is never dereferenced.
type fakeTel struct {
	observability.Provider
	calls []observability.MCPCall
}

func (f *fakeTel) RecordMCPCall(_ context.Context, c observability.MCPCall) {
	f.calls = append(f.calls, c)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func callRequest(name string, args map[string]any) mcplib.CallToolRequest {
	var req mcplib.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	return req
}

func TestWrapRecordsMCPCall(t *testing.T) {
	t.Parallel()

	agentFn := func(context.Context) string { return "test-agent" }
	req := callRequest("magus_query", map[string]any{"query": "kind:target"})

	t.Run("ok outcome sizes input and output", func(t *testing.T) {
		tel := &fakeTel{}
		const out = "hello world result"
		h := wrap(quietLogger(), agentFn, "", tel, new(atomic.Uint64), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return mcplib.NewToolResultText(out), nil
		})

		result, err := h(context.Background(), req)
		require.NoError(t, err)
		require.NotNil(t, result)

		require.Len(t, tel.calls, 1)
		got := tel.calls[0]
		assert.Equal(t, "magus_query", got.Tool)
		assert.Equal(t, "ok", got.Outcome)
		assert.Positive(t, got.InputBytes)
		assert.Equal(t, int64(len(out)), got.OutputBytes)
		assert.GreaterOrEqual(t, got.Duration, 0.0)
	})

	t.Run("error outcome nil result contributes zero output", func(t *testing.T) {
		tel := &fakeTel{}
		h := wrap(quietLogger(), agentFn, "", tel, new(atomic.Uint64), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return nil, errors.New("boom")
		})

		result, err := h(context.Background(), req)
		require.Error(t, err)
		assert.Nil(t, result)

		require.Len(t, tel.calls, 1)
		got := tel.calls[0]
		assert.Equal(t, "magus_query", got.Tool)
		assert.Equal(t, "error", got.Outcome)
		assert.Positive(t, got.InputBytes)
		assert.Zero(t, got.OutputBytes)
	})

	t.Run("nil telemetry is a no-op", func(t *testing.T) {
		h := wrap(quietLogger(), agentFn, "", nil, new(atomic.Uint64), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return mcplib.NewToolResultText("ok"), nil
		})
		result, err := h(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

func TestWrapCapturesExchange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	agentFn := func(context.Context) string { return "test-agent" }
	req := callRequest("magus_query", map[string]any{"query": "kind:target"})
	const out = "hello world result payload"
	h := wrap(quietLogger(), agentFn, dir, nil, new(atomic.Uint64), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText(out), nil
	})

	_, err := h(context.Background(), req)
	require.NoError(t, err)

	events, err := trail.ReadRecent(dir, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	ev := events[0]

	assert.Equal(t, trail.KindMCPToolCall, ev.Kind)
	assert.Equal(t, "test-agent", ev.Actor)
	assert.Equal(t, "magus_query", ev.Action)
	assert.Equal(t, trail.OutcomeOK, ev.Outcome)
	assert.Equal(t, int64(len(out)), ev.ResponseBytes)
	assert.Equal(t, out, ev.Preview) // a short response: the preview is the whole body
	require.NotEmpty(t, ev.ResponseRef)
	require.NotEmpty(t, ev.RequestRef) // the request arguments were captured too

	// Each ref resolves back to the exact bytes the agent exchanged.
	resp, err := trail.ReadBlob(dir, ev.ResponseRef)
	require.NoError(t, err)
	assert.Equal(t, out, string(resp))
	reqBody, err := trail.ReadBlob(dir, ev.RequestRef)
	require.NoError(t, err)
	assert.Contains(t, string(reqBody), "kind:target")
}

func TestWrapRecordsSoftErrorAsError(t *testing.T) {
	t.Parallel()

	// adapt() turns a soft failure into an IsError result with a nil err. The trail (and the
	// metric) must record it as error, not ok - the regression the review caught.
	dir := t.TempDir()
	tel := &fakeTel{}
	agentFn := func(context.Context) string { return "a" }
	h := wrap(quietLogger(), agentFn, dir, tel, new(atomic.Uint64), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultError("bad arguments"), nil // soft error, err == nil
	})

	_, err := h(context.Background(), callRequest("magus_query", map[string]any{"q": "x"}))
	require.NoError(t, err) // the handler itself does not error

	events, err := trail.ReadRecent(dir, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, trail.OutcomeError, events[0].Outcome, "a soft IsError result must record as error")
	require.Len(t, tel.calls, 1)
	assert.Equal(t, trail.OutcomeError, tel.calls[0].Outcome, "the metric must also see error")
}

func TestAllText(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", allText(nil))
	assert.Equal(t, "abc", allText(mcplib.NewToolResultText("abc")))

	multi := &mcplib.CallToolResult{
		Content: []mcplib.Content{
			mcplib.TextContent{Type: "text", Text: "ab"},
			mcplib.ImageContent{Type: "image", Data: "ignored"},
			mcplib.TextContent{Type: "text", Text: "cde"},
		},
	}
	assert.Equal(t, "abcde", allText(multi))
}

func TestParamString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "v", paramString(map[string]any{"k": "v"}, "k", "def"))
	assert.Equal(t, "def", paramString(map[string]any{"k": "v"}, "missing", "def"))
	assert.Equal(t, "def", paramString(map[string]any{"k": 42}, "k", "def")) // wrong type → default
	assert.Equal(t, "def", paramString(nil, "k", "def"))
}

func TestParamBool(t *testing.T) {
	t.Parallel()
	assert.True(t, paramBool(map[string]any{"dry_run": true}, "dry_run", false))
	assert.False(t, paramBool(map[string]any{"dry_run": false}, "dry_run", true))
	assert.False(t, paramBool(map[string]any{"dry_run": "yes"}, "dry_run", false)) // wrong type → default
	assert.True(t, paramBool(nil, "dry_run", true))
}

func TestParamFloat(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 3.14, paramFloat(map[string]any{"n": float64(3.14)}, "n", 0))
	assert.Equal(t, float64(7), paramFloat(map[string]any{"n": int(7)}, "n", 0))
	assert.Equal(t, float64(99), paramFloat(map[string]any{"n": int64(99)}, "n", 0))
	assert.Equal(t, 1.5, paramFloat(map[string]any{"n": "oops"}, "n", 1.5)) // wrong type → default
	assert.Equal(t, 2.0, paramFloat(nil, "n", 2.0))
}

func TestParseRunEvents(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		assert.Empty(t, parseRunEvents(bytes.NewBufferString("")))
	})
	t.Run("blank lines only", func(t *testing.T) {
		assert.Empty(t, parseRunEvents(bytes.NewBufferString("\n\n")))
	})
	t.Run("single event", func(t *testing.T) {
		assert.Len(t, parseRunEvents(bytes.NewBufferString(`{"type":"run"}`)), 1)
	})
	t.Run("two events", func(t *testing.T) {
		assert.Len(t, parseRunEvents(bytes.NewBufferString("{\"type\":\"a\"}\n{\"type\":\"b\"}")), 2)
	})
	t.Run("whitespace around", func(t *testing.T) {
		assert.Len(t, parseRunEvents(bytes.NewBufferString("  {\"k\":1}  \n")), 1)
	})
	t.Run("invalid json skipped", func(t *testing.T) {
		assert.Len(t, parseRunEvents(bytes.NewBufferString("not-json\n{\"ok\":true}")), 1)
	})
}
