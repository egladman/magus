//go:build !mcp

package main

import (
	"context"
	"errors"
)

// mcpIsCompiled is false when the binary was built without -tags mcp.
const mcpIsCompiled = false

// mcpCmd informs the user that MCP was not compiled in.
func mcpCmd(_ context.Context, _ []string) error {
	return errors.New("magus mcp: this binary was compiled without MCP support; rebuild with -tags mcp to enable")
}

// startMCPWithDaemon is a no-op when MCP is not compiled in.
func startMCPWithDaemon(_ context.Context, _ context.CancelFunc) {}
