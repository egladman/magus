package main

import (
	"context"

	"github.com/egladman/magus/internal/service/console"
)

// graphExplorerLink returns a Graph Explorer URL preloaded with the repo's committed
// docs/graph.json, or "" when there is none to link (see console.GraphExplorerLink).
func graphExplorerLink(ctx context.Context, root string) string {
	return console.GraphExplorerLink(root, func() string { return deriveSourceBase(ctx, root) })
}

// deriveSourceBase resolves the workspace's forge blob-URL base, or "" when there is none.
func deriveSourceBase(ctx context.Context, root string) string {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return ""
	}
	return console.DeriveSourceBase(ctx, ws)
}
