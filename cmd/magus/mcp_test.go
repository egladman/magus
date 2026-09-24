package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// The command's own serve path, driven over pipes: every line on the wire is a JSON-RPC
// frame answering a request, and a print to os.Stdout while it serves lands on stderr.
func TestServeMCPStdioKeepsTheWireToProtocolFrames(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"magus\";\n\nmagus.project({})\n"), 0o644))
	m, err := magus.Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	diag, err := os.CreateTemp(t.TempDir(), "stderr")
	require.NoError(t, err)
	t.Cleanup(func() { _ = diag.Close() })

	stdout := os.Stdout
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- serveMCPStdio(context.Background(), m, inR, outW, diag)
		_ = outW.Close()
	}()
	wire := bufio.NewScanner(outR)
	wire.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	frames := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"cmd-test","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"magus_describe","arguments":{"kind":"mcp_tools"}}}`,
	}
	for i, frame := range frames {
		_, err := io.WriteString(inW, frame+"\n")
		require.NoError(t, err)
		require.True(t, wire.Scan(), "no reply to %s", frame)
		var got struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
		}
		require.NoError(t, json.Unmarshal(wire.Bytes(), &got), "the wire carried a line that is not JSON-RPC: %q", wire.Text())
		assert.Equal(t, "2.0", got.JSONRPC)
		assert.Equal(t, i+1, got.ID)
		assert.NotEmpty(t, got.Result)
		if i == 0 {
			fmt.Println("stray")
		}
	}

	require.NoError(t, inW.Close())
	require.NoError(t, <-served)
	for wire.Scan() {
		t.Errorf("the wire carried a line nobody asked for: %q", wire.Text())
	}
	assert.Same(t, stdout, os.Stdout, "os.Stdout is restored once serving stops")

	logged, err := os.ReadFile(diag.Name())
	require.NoError(t, err)
	assert.Contains(t, string(logged), "magus: serving MCP over stdio for "+m.Root())
	assert.Contains(t, string(logged), "stray\n")
}

func TestMCPCmdRefusesArguments(t *testing.T) {
	err := mcpCmd(context.Background(), "", []string{"--stdio"})
	assert.ErrorContains(t, err, "flag provided but not defined: -stdio")

	err = mcpCmd(context.Background(), "", []string{"serve"})
	assert.ErrorContains(t, err, `magus mcp: takes no arguments (got "serve")`)
}

func TestMCPSocketPathFollowsTheSetting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	want := filepath.Join(dir, "magus", "mcp.sock")
	on, off := true, false

	assert.Empty(t, mcpSocketPath(config.MCP{UnixSocket: &off}), "false serves no socket")
	assert.Equal(t, want, mcpSocketPath(config.MCP{UnixSocket: &on}), "true is the path even where the bind will refuse")
	unset := ""
	if httpx.PeerCredentialsSupported() {
		unset = want
	}
	assert.Equal(t, unset, mcpSocketPath(config.MCP{}), "unset is on exactly where a peer's uid can be read")
}

func TestServerInfoReportsTheMCPSocket(t *testing.T) {
	serverMCPSocket.Store("/run/magus/mcp.sock")
	t.Cleanup(func() { serverMCPSocket.Store("") })

	st := serverInfo("unix:///run/magus/server.sock")
	assert.Contains(t, st.Listeners, types.StatusListener{Kind: types.ListenerMCPSocket, Address: "/run/magus/mcp.sock"})
	assert.Contains(t, st.Listeners, types.StatusListener{Kind: types.ListenerSocket, Address: "unix:///run/magus/server.sock"})
}
