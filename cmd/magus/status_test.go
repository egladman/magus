package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types/daemon"
)

// readyzServer stands up an httptest server whose /readyz returns code, and returns
// its host:port (the form mcp.address takes). Other paths 404, proving the probe keys
// on /readyz specifically.
func readyzServer(t *testing.T, code int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(code)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// mcpServing returns an MCP config enabled and pointing at a live endpoint whose /readyz
// returns code (200 serving, 503 not-ready). mcpUnreachable and mcpDisabled are the other
// two states. They build an explicit config to pass in, so no test mutates package state.
func mcpServing(t *testing.T, code int) config.MCP {
	return config.MCP{Address: readyzServer(t, code), Enabled: new(true)}
}

// TestPrintStatusTextRendersMCPEndpoint proves the mcp endpoint block appears in the full
// text render (not just the isolated helper). server.PrintStatusText writes to an *os.File, so a
// temp file stands in for stdout.
func TestPrintStatusTextRendersMCPEndpoint(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	r := daemon.Snapshot{
		MCPEndpoint: &daemon.MCPEndpoint{Enabled: true, URL: "http://127.0.0.1:7391/mcp", Reachable: true, State: "serving"},
	}
	server.PrintStatusText(f, r, server.StatusView{})
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, "mcp endpoint")
	assert.Contains(t, out, "http://127.0.0.1:7391/mcp")
	assert.Contains(t, out, "serving")
}

// TestWriteStatusStructuredFormats covers the dispatch: a machine-readable format bypasses
// every text renderer above and emits the snapshot itself.
func TestWriteStatusStructuredFormats(t *testing.T) {
	// --tee is process-global, so a leftover value from another test would send this
	// render at a file that may no longer be writable.
	prevTee := global.tee
	t.Cleanup(func() { global.tee = prevTee })
	global.tee = ""

	r := daemon.Snapshot{Cache: daemon.Cache{Dir: "/tmp/c"}}

	t.Run("json", func(t *testing.T) {
		out := captureStdout(t, func() {
			require.NoError(t, writeStatus(io.Discard, r, output.Options{Format: outputJSON}, 0, false))
		})
		assert.Contains(t, out, `"dir": "/tmp/c"`)
	})

	t.Run("yaml", func(t *testing.T) {
		out := captureStdout(t, func() {
			require.NoError(t, writeStatus(io.Discard, r, output.Options{Format: outputYAML}, 0, false))
		})
		assert.Contains(t, out, "dir: /tmp/c")
	})

	t.Run("compact wins over the text frame", func(t *testing.T) {
		var buf bytes.Buffer
		require.NoError(t, writeStatus(&buf, r, output.Options{Format: outputText}, 0, true))
		assert.Equal(t, "no broker · no server\n", buf.String())
	})
}
