package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/observability"
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
		h := wrap(quietLogger(), agentFn, nil, tel, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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
		h := wrap(quietLogger(), agentFn, nil, tel, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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
		h := wrap(quietLogger(), agentFn, nil, nil, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return mcplib.NewToolResultText("ok"), nil
		})
		result, err := h(context.Background(), req)
		require.NoError(t, err)
		assert.NotNil(t, result)
	})
}

func TestSumTextBytes(t *testing.T) {
	t.Parallel()

	assert.Zero(t, sumTextBytes(nil))
	assert.Equal(t, int64(len("abc")), sumTextBytes(mcplib.NewToolResultText("abc")))

	multi := &mcplib.CallToolResult{
		Content: []mcplib.Content{
			mcplib.TextContent{Type: "text", Text: "ab"},
			mcplib.ImageContent{Type: "image", Data: "ignored"},
			mcplib.TextContent{Type: "text", Text: "cde"},
		},
	}
	assert.Equal(t, int64(5), sumTextBytes(multi))
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

func TestParseEventLines(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		assert.Empty(t, parseEventLines(bytes.NewBufferString("")))
	})
	t.Run("blank lines only", func(t *testing.T) {
		assert.Empty(t, parseEventLines(bytes.NewBufferString("\n\n")))
	})
	t.Run("single event", func(t *testing.T) {
		assert.Len(t, parseEventLines(bytes.NewBufferString(`{"type":"run"}`)), 1)
	})
	t.Run("two events", func(t *testing.T) {
		assert.Len(t, parseEventLines(bytes.NewBufferString("{\"type\":\"a\"}\n{\"type\":\"b\"}")), 2)
	})
	t.Run("whitespace around", func(t *testing.T) {
		assert.Len(t, parseEventLines(bytes.NewBufferString("  {\"k\":1}  \n")), 1)
	})
	t.Run("invalid json skipped", func(t *testing.T) {
		assert.Len(t, parseEventLines(bytes.NewBufferString("not-json\n{\"ok\":true}")), 1)
	})
}
