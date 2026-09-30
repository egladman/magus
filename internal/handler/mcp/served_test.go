package mcp

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// rootsSession is a client session that declares roots (or not) and counts how often the
// server asks for them.
type rootsSession struct {
	caps  mcplib.ClientCapabilities
	roots []mcplib.Root
	asked atomic.Int32
}

func (s *rootsSession) Initialize()       {}
func (s *rootsSession) Initialized() bool { return true }
func (s *rootsSession) NotificationChannel() chan<- mcplib.JSONRPCNotification {
	return make(chan mcplib.JSONRPCNotification, 1)
}
func (s *rootsSession) SessionID() string                                { return "session-1" }
func (s *rootsSession) GetClientInfo() mcplib.Implementation             { return mcplib.Implementation{} }
func (s *rootsSession) SetClientInfo(mcplib.Implementation)              {}
func (s *rootsSession) GetClientCapabilities() mcplib.ClientCapabilities { return s.caps }
func (s *rootsSession) SetClientCapabilities(mcplib.ClientCapabilities)  {}
func (s *rootsSession) ListRoots(context.Context, mcplib.ListRootsRequest) (*mcplib.ListRootsResult, error) {
	s.asked.Add(1)
	return &mcplib.ListRootsResult{Roots: s.roots}, nil
}

// callStamped serves one tool call through a real MCPServer, so the session and the server
// reach the handler the way they do in production, and returns the result.
func callStamped(t *testing.T, s *served, ctx context.Context, session server.ClientSession) mcplib.CallToolResult {
	t.Helper()
	srv := server.NewMCPServer("magus", "test", server.WithToolCapabilities(false))
	srv.AddTool(mcplib.NewTool("probe"), server.ToolHandlerFunc(s.annotate(func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return jsonResult(map[string]any{"ok": true})
	})))
	msg := srv.HandleMessage(srv.WithContext(ctx, session), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"probe"}}`))
	reply, ok := msg.(mcplib.JSONRPCResponse)
	require.True(t, ok, "not a response: %#v", msg)
	res, ok := reply.Result.(*mcplib.CallToolResult)
	require.True(t, ok, "not a tool result: %#v", reply.Result)
	return *res
}

func servedMeta(t *testing.T, res mcplib.CallToolResult) servedInfo {
	t.Helper()
	require.NotNil(t, res.Meta)
	info, ok := res.Meta.AdditionalFields[servedMetaKey].(servedInfo)
	require.True(t, ok, "_meta.%s is %#v", servedMetaKey, res.Meta.AdditionalFields[servedMetaKey])
	return info
}

func textParts(res mcplib.CallToolResult) []string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return parts
}

func workspaceDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), []byte("import \"magus\";\n"), 0o644))
	return dir
}

var testBuild = types.BuildInfo{Version: "v0.4.3-234", Commit: "00fd71e20"}

func TestServedNamesTheServedRootAndBuildOnEveryResult(t *testing.T) {
	root := workspaceDir(t)
	s := &served{root: root, build: testBuild, roots: newSessionRoots().lookup}
	session := &rootsSession{
		caps: mcplib.ClientCapabilities{Roots: &struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{}},
		roots: []mcplib.Root{{URI: "file://" + filepath.Join(root, "internal")}},
	}

	res := callStamped(t, s, context.Background(), session)
	assert.Equal(t, servedInfo{Root: root, Version: "v0.4.3-234", Commit: "00fd71e20", Transport: "http"}, servedMeta(t, res))
	assert.Equal(t, []string{`{"ok":true}`}, textParts(res), "no skew adds no line")
	assert.NotNil(t, res.StructuredContent, "no skew keeps the structured payload")
}

func TestServedReportsRootSkewFromTheCallersRoots(t *testing.T) {
	root, other := workspaceDir(t), workspaceDir(t)
	s := &served{root: root, build: testBuild, roots: newSessionRoots().lookup}
	session := &rootsSession{
		caps: mcplib.ClientCapabilities{Roots: &struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{}},
		roots: []mcplib.Root{{URI: "file://" + other}},
	}

	res := callStamped(t, s, context.Background(), session)
	callStamped(t, s, context.Background(), session)
	assert.Equal(t, int32(1), session.asked.Load(), "roots are asked once per session")
	assert.Equal(t, servedInfo{
		Root: root, Version: "v0.4.3-234", Commit: "00fd71e20", Transport: "http",
		Skew: []servedSkew{{Kind: "root", Caller: []string{other}}},
	}, servedMeta(t, res))
	assert.Equal(t, []string{
		`{"ok":true}`,
		"magus skew: this server serves " + root + " and your session is in " + other + "; register `./magus mcp` per checkout (magus describe harness)",
	}, textParts(res))
	assert.Nil(t, res.StructuredContent, "a host reading structured content would never show the skew line")
}

func TestServedReportsNoRootSkewWithoutTheRootsCapability(t *testing.T) {
	root, other := workspaceDir(t), workspaceDir(t)
	s := &served{root: root, build: testBuild, roots: newSessionRoots().lookup}
	session := &rootsSession{roots: []mcplib.Root{{URI: "file://" + other}}}

	res := callStamped(t, s, context.Background(), session)
	assert.Zero(t, session.asked.Load(), "a client that declared no roots is never asked")
	assert.Empty(t, servedMeta(t, res).Skew)
	assert.Equal(t, []string{`{"ok":true}`}, textParts(res))
}

func TestServedHasNoRootSkewOverStdio(t *testing.T) {
	root, other := workspaceDir(t), workspaceDir(t)
	s := &served{root: root, build: testBuild, roots: newSessionRoots().lookup}
	session := &rootsSession{
		caps: mcplib.ClientCapabilities{Roots: &struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{}},
		roots: []mcplib.Root{{URI: "file://" + other}},
	}

	res := callStamped(t, s, trail.ContextWithCredential(context.Background(), types.CredentialStdio), session)
	assert.Zero(t, session.asked.Load())
	assert.Equal(t, servedInfo{Root: root, Version: "v0.4.3-234", Commit: "00fd71e20", Transport: "stdio"}, servedMeta(t, res))
}

func TestServedReportsAReplacedExecutable(t *testing.T) {
	root := workspaceDir(t)
	exe := filepath.Join(root, "magus")
	require.NoError(t, os.WriteFile(exe, []byte("old build"), 0o755))
	s := &served{root: root, build: testBuild, roots: newSessionRoots().lookup}
	s.watchExecutable(exe)
	session := &rootsSession{}

	res := callStamped(t, s, context.Background(), session)
	assert.Empty(t, servedMeta(t, res).Skew, "an untouched executable is no skew")

	require.NoError(t, os.WriteFile(exe+".new", []byte("a newer build"), 0o755))
	require.NoError(t, os.Rename(exe+".new", exe))
	res = callStamped(t, s, context.Background(), session)
	assert.Equal(t, []servedSkew{{Kind: "build", Executable: exe}}, servedMeta(t, res).Skew)
	assert.Equal(t, []string{
		`{"ok":true}`,
		"magus skew: " + exe + " was rebuilt after this server started at v0.4.3-234; restart the MCP server",
	}, textParts(res))
}
