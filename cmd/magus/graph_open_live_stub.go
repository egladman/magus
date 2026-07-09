//go:build !mcp

package main

import (
	"context"
	"fmt"
	"os"
)

// graphOpenLive is a stub for non-mcp builds. Live mode requires the daemon
// binary compiled with -tags mcp. Only reached via explicit --live; the
// zero-arg auto-switch in graphOpen never calls this on a non-mcp binary
// because liveBridgeReachable below always reports false.
func graphOpenLive(_ context.Context, _ string, _, _ bool) error {
	fmt.Fprintln(os.Stderr, "magus graph open --live: live mode requires the daemon binary (built with -tags mcp).")
	fmt.Fprintln(os.Stderr, "This binary was not built with MCP support. Use `magus graph open` for the fragment mode instead.")
	return errSilent{exitCode: 1}
}

// liveBridgeReachable is always false on non-mcp builds: there is no web
// bridge in this binary, so the zero-arg default in graphOpen always falls
// back to fragment mode instead of reaching the stub error above. Explicit
// --live still hits graphOpenLive's hard error.
func liveBridgeReachable(_ context.Context) bool { return false }
