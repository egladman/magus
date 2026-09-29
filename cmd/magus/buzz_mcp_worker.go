package main

import (
	"context"
	"io"

	"github.com/egladman/magus/internal/interp/transform"
)

// runBuzzWorker is the private re-exec entry point for the buzz MCP tool. It runs
// before workspace startup so the script cannot cause a magusfile load or gain
// the ordinary CLI's host bindings.
func runBuzzWorker(ctx context.Context, in io.Reader, out, errOut io.Writer) int {
	return transform.Serve(ctx, in, out, errOut)
}
