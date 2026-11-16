package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types/daemon"
)

func TestPrintMCPEndpointStatus(t *testing.T) {
	render := func(m *daemon.MCPEndpoint) string {
		var b strings.Builder
		printMCPEndpointStatus(&b, m)
		return b.String()
	}

	t.Run("nil-renders-nothing", func(t *testing.T) {
		assert.Empty(t, render(nil))
	})
	t.Run("serving-shows-url-and-state", func(t *testing.T) {
		out := render(&daemon.MCPEndpoint{Enabled: true, URL: "http://127.0.0.1:7391/mcp", Reachable: true, State: "serving"})
		assert.Contains(t, out, "mcp endpoint")
		assert.Contains(t, out, "http://127.0.0.1:7391/mcp")
		assert.Contains(t, out, "serving")
	})
	t.Run("unreachable-shows-note", func(t *testing.T) {
		out := render(&daemon.MCPEndpoint{Enabled: true, URL: "http://127.0.0.1:7391/mcp", State: "unreachable", Note: "start the server: magus server start"})
		assert.Contains(t, out, "unreachable")
		assert.Contains(t, out, "magus server start")
	})
	t.Run("disabled-omits-url", func(t *testing.T) {
		out := render(&daemon.MCPEndpoint{State: "disabled", Note: "MCP is disabled (mcp.enabled=false); no agent tools are served."})
		assert.Contains(t, out, "disabled")
		assert.NotContains(t, out, "url")
	})
}
