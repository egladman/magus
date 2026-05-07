//go:build !mcp

package main

import (
	"context"
	"fmt"
	"os"
)

// mcpIsCompiled is false when the binary was built without -tags mcp.
const mcpIsCompiled = false

// mcpCmd informs the user that MCP was not compiled in.
func mcpCmd(_ context.Context, _ []string) error {
	fmt.Fprintln(os.Stderr, "magus mcp: this binary was compiled without MCP support (-tags mcp)")
	return nil
}

// startMCPWithDaemon is a no-op when MCP is not compiled in.
func startMCPWithDaemon(_ context.Context, _ context.CancelFunc) {}
