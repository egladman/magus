//go:build !mcp

package main

import (
	"context"
	"fmt"
	"os"
)

// graphOpenLive is a stub for non-mcp builds. Live mode requires the daemon
// binary compiled with -tags mcp.
func graphOpenLive(_ context.Context, _, _ string, _, _ bool) error {
	fmt.Fprintln(os.Stderr, "magus graph open --live: live mode requires the daemon binary (built with -tags mcp).")
	fmt.Fprintln(os.Stderr, "This binary was not built with MCP support. Use `magus graph open` for the fragment mode instead.")
	return errSilent{exitCode: 1}
}
