package mcp

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/types"
)

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
	resp types.InvokeResponse
	err  error
}

func (f fakeDriver) Name() string { return "fake" }
func (f fakeDriver) Invoke(context.Context, types.InvokeRequest) (types.InvokeResponse, error) {
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
		h := adapt(fakeDriver{resp: types.InvokeResponse{Data: map[string]any{"ok": true}}})
		res, err := h(context.Background(), mcplib.CallToolRequest{})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		assert.JSONEq(t, `{"ok":true}`, allText(res))
	})

	t.Run("plain Text is returned verbatim when Data is nil", func(t *testing.T) {
		h := adapt(fakeDriver{resp: types.InvokeResponse{Text: "just text"}})
		res, err := h(context.Background(), mcplib.CallToolRequest{})
		require.NoError(t, err)
		assert.Equal(t, "just text", allText(res))
	})
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
