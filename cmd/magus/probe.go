package main

import (
	"context"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/server"
)

// status.go, which this move does not hold, still names these.
var (
	parseProbeKinds        = server.ParseProbeKinds
	buildMCPEndpointStatus = server.BuildMCPEndpointStatus
	buildConsoleStatus     = server.BuildConsoleStatus
)

// runProbes asks the server at the address socket resolves to.
func runProbes(ctx context.Context, socket string, mcp config.MCP, kinds []server.ProbeKind, root string) error {
	return server.RunProbes(ctx, resolveServerAddr(socket), mcp, kinds, root)
}
