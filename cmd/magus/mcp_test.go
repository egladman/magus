package main

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalmcp "github.com/egladman/magus/internal/handler/mcp"
)

func TestMCPCmdRefusesArguments(t *testing.T) {
	err := mcpCommand(context.Background(), "", []string{"--stdio"})
	assert.ErrorContains(t, err, "flag provided but not defined: -stdio")

	err = mcpCommand(context.Background(), "", []string{"serve"})
	assert.ErrorContains(t, err, `magus mcp: takes no arguments (got "serve")`)
}

// TestMCPAddrFallsBackToTheDefault covers both arms of the address resolution. The
// unconfigured case has to yield a parseable host:port on its own, because that is what
// the doctor bridge check probes when nothing declares an address.
func TestMCPAddrFallsBackToTheDefault(t *testing.T) {
	prev := globalCfg.MCP.Address
	t.Cleanup(func() { globalCfg.MCP.Address = prev })

	globalCfg.MCP.Address = ""
	assert.Equal(t, internalmcp.DefaultAddress, mcpAddrString())
	parsed, err := netip.ParseAddrPort(mcpAddrString())
	require.NoError(t, err)
	assert.NotZero(t, parsed.Port())

	globalCfg.MCP.Address = "127.0.0.1:9999"
	assert.Equal(t, "127.0.0.1:9999", mcpAddrString())
	parsed, err = netip.ParseAddrPort(mcpAddrString())
	require.NoError(t, err)
	assert.Equal(t, uint16(9999), parsed.Port())
}
