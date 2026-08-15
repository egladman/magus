package mcp

import (
	"bytes"
	"context"
	"errors"
	"github.com/egladman/magus/internal/secret"
	server "github.com/mark3labs/mcp-go/server"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/spells"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	return slog.New(slog.DiscardHandler)
}

func callRequest(name string, args map[string]any) mcplib.CallToolRequest {
	var req mcplib.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	return req
}

func TestWrapRecordsMCPCall(t *testing.T) {
	t.Parallel()

	originFn := func(context.Context) origin.Origin { return origin.Origin{Agent: "test-agent"} }
	req := callRequest("magus_query", map[string]any{"query": "kind:target"})

	t.Run("ok outcome sizes input and output", func(t *testing.T) {
		tel := &fakeTel{}
		const out = "hello world result"
		h := wrap(quietLogger(), originFn, "", noSecrets, tel, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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
		h := wrap(quietLogger(), originFn, "", noSecrets, tel, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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
		h := wrap(quietLogger(), originFn, "", noSecrets, nil, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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

	// The session User-Agent comes off originFn and is recorded on the event.
	originFn := func(context.Context) origin.Origin {
		return origin.Origin{Agent: "test-agent", UserAgent: "claude-code/1.2.3"}
	}
	req := callRequest("magus_query", map[string]any{"query": "kind:target"})
	const out = "hello world result payload"
	h := wrap(quietLogger(), originFn, dir, noSecrets, nil, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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
	assert.Equal(t, "claude-code/1.2.3", ev.UserAgent, "the session User-Agent is recorded on the event")
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
	originFn := func(context.Context) origin.Origin { return origin.Origin{Agent: "a"} }
	h := wrap(quietLogger(), originFn, dir, noSecrets, tel, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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

func TestOptions_Accessors(t *testing.T) {
	t.Parallel()

	// validate: Magus is the one required field.
	assert.Error(t, Options{}.validate())
	assert.NoError(t, Options{Magus: &magus.Magus{}}.validate())

	// logger falls back to slog.Default() when unset, returns the given one otherwise.
	assert.NotNil(t, Options{}.logger())
	custom := slog.New(slog.NewTextHandler(nil, nil))
	assert.Same(t, custom, Options{Logger: custom}.logger())

	// httpAddr falls back to defaultAddrPort when the passed AddrPort is invalid.
	assert.Equal(t, defaultAddrPort, Options{}.httpAddr())

	// SiteOrigin parses the hosted explorer URL down to scheme://host.
	origin, err := Options{}.SiteOrigin()
	require.NoError(t, err)
	assert.Equal(t, "https://eli.gladman.cc", origin)
}

func TestAgentFromRequest(t *testing.T) {
	t.Parallel()

	mk := func(name, ver string) *mcplib.InitializeRequest {
		req := &mcplib.InitializeRequest{}
		req.Params.ClientInfo.Name = name
		req.Params.ClientInfo.Version = ver
		return req
	}
	assert.Equal(t, "claude/1.2", agentFromRequest(mk("claude", "1.2")))
	assert.Equal(t, "claude", agentFromRequest(mk("claude", ""))) // no version: bare name
	assert.Equal(t, "unknown", agentFromRequest(mk("", "")))      // nothing to identify
}

// fakeDriver is a SpellDriver that returns a canned response for adapt tests.
type fakeDriver struct {
	resp spells.InvokeResponse
	err  error
}

func (f fakeDriver) Name() string { return "fake" }
func (f fakeDriver) Invoke(context.Context, spells.InvokeRequest) (spells.InvokeResponse, error) {
	return f.resp, f.err
}

func TestAdapt(t *testing.T) {
	t.Parallel()

	t.Run("error becomes a soft IsError result, not a transport error", func(t *testing.T) {
		h := adapt(fakeDriver{err: errors.New("boom")})
		res, err := h(context.Background(), mcplib.CallToolRequest{})
		require.NoError(t, err)
		assert.True(t, res.IsError)
		assert.Contains(t, allText(res), "boom")
	})

	t.Run("structured Data is marshaled to JSON text", func(t *testing.T) {
		h := adapt(fakeDriver{resp: spells.InvokeResponse{Data: map[string]any{"ok": true}}})
		res, err := h(context.Background(), mcplib.CallToolRequest{})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		assert.JSONEq(t, `{"ok":true}`, allText(res))
	})

	t.Run("plain Text is returned verbatim when Data is nil", func(t *testing.T) {
		h := adapt(fakeDriver{resp: spells.InvokeResponse{Text: "just text"}})
		res, err := h(context.Background(), mcplib.CallToolRequest{})
		require.NoError(t, err)
		assert.Equal(t, "just text", allText(res))
	})
}

// TestUnregisteredDrivers reproduces the T-2 gap: registerTools's loop only checked
// Registry -> driver, so a driver wired into allMCPTools but never given a Registry
// entry mounted nowhere and nothing reported it. Pre-fix this function did not exist;
// post-fix it is what registerTools panics on.
func TestUnregisteredDrivers(t *testing.T) {
	t.Parallel()

	// fakeDriver.Name() is "fake": no Registry entry named "fake" exists here, so it
	// must be reported.
	got := unregisteredDrivers([]spells.Driver{fakeDriver{}}, []ToolDescriptor{{Name: "other"}})
	assert.Equal(t, []string{"fake"}, got)

	// A driver with a matching Registry entry is not reported.
	got = unregisteredDrivers([]spells.Driver{fakeDriver{}}, []ToolDescriptor{{Name: "fake"}})
	assert.Empty(t, got)
}

func TestBuildMCPTool(t *testing.T) {
	t.Parallel()

	tool := buildMCPTool(ToolDescriptor{
		Name:        "demo",
		Description: "a demo tool",
		Params: []ParamDescriptor{
			{Name: "q", Type: "string", Required: true, Description: "query"},
			{Name: "dry", Type: "boolean"},
			{Name: "n", Type: "number"},
		},
	})
	assert.Equal(t, "demo", tool.Name)
	assert.Contains(t, tool.InputSchema.Properties, "q")
	assert.Contains(t, tool.InputSchema.Properties, "dry")
	assert.Contains(t, tool.InputSchema.Properties, "n")
	assert.Equal(t, []string{"q"}, tool.InputSchema.Required)

	// An unknown param type is a programming error, surfaced as a panic at build time.
	assert.Panics(t, func() {
		buildMCPTool(ToolDescriptor{Name: "bad", Params: []ParamDescriptor{{Name: "x", Type: "date"}}})
	})
}

func TestJSONResult(t *testing.T) {
	t.Parallel()

	res, err := jsonResult(map[string]any{"a": 1})
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1}`, allText(res))

	// A value the JSON codec cannot encode surfaces as an error, not a partial result.
	_, err = jsonResult(make(chan int))
	assert.Error(t, err)
}

func TestToolLogger(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, toolLogger(context.Background())) // default when none attached

	custom := slog.New(slog.NewTextHandler(nil, nil))
	assert.Same(t, custom, toolLogger(withLogger(context.Background(), custom)))
}

// TestToolCallBlobsAreRedacted is the test the trail's ctx parameter always needed.
//
// internal/trail redacts through the resolver on its context, but the MCP request context
// is an ANCESTOR of any run a tool starts, never a descendant, so it never inherited one.
// The seam was threaded through four packages and did nothing on the path that motivated
// it: a request/response pair is a whole tool payload, written verbatim to an append-only
// file. wrap now installs the workspace resolver, and this pins that.
//
// Drop the ws.ContextWithSecrets call in wrap and this fails.
func TestToolCallBlobsAreRedacted(t *testing.T) {
	const credential = "sk-live-must-not-be-persisted"
	base := t.TempDir()

	res := secret.New()
	t.Setenv("MCP_TEST_TOKEN", credential)
	// Provenance is what marks a value as a credential, so resolve it once.
	got, err := res.Read(secret.ContextWithResolver(t.Context(), res), "MCP_TEST_TOKEN")
	require.NoError(t, err)
	require.Equal(t, credential, got.Reveal())

	// A tool whose arguments and result both carry the credential, which is exactly the
	// shape an agent passing a token through a tool call produces.
	h := wrapWithResolver(t, base, res, func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText("upstream said: " + credential), nil
	})
	_, err = h(context.Background(), callRequest("magus_query", map[string]any{"q": credential}))
	require.NoError(t, err)

	var found []string
	require.NoError(t, filepath.WalkDir(filepath.Join(base, "activity"), func(path string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), credential) {
			found = append(found, path)
		}
		return nil
	}))
	assert.Empty(t, found, "the credential was persisted to the activity trail in these files")
}

// noSecrets is the identity context seam, for the tests that are not about redaction.
func noSecrets(ctx context.Context) context.Context { return ctx }

// wrapWithResolver builds a handler whose trail writes redact against res.
func wrapWithResolver(t *testing.T, trailDir string, res *secret.Resolver, fn handlerFn) server.ToolHandlerFunc {
	t.Helper()
	return wrap(quietLogger(), func(context.Context) origin.Origin { return origin.Origin{Agent: "test-agent"} },
		trailDir, func(ctx context.Context) context.Context { return secret.ContextWithResolver(ctx, res) },
		nil, fn)
}
