package main

import (
	"context"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/job/evidence"
	"github.com/egladman/magus/types"
)

func readSymbolDelta(ctx context.Context, root string, g *knowledge.Graph, driver types.RevisionReader, base string, changed []string) (job.SymbolDelta, bool) {
	return evidence.ReadSymbolDelta(ctx, root, g, driver, base, changed)
}
