package main

import (
	"context"
	"log/slog"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/log/attr"
)

// symbolCoverage reports what a lookup was able to search. input is the query text and
// seeded reports whether this lookup merged the lazy @symbols shards.
//
// It observes; knowledge.Answer judges. That split is what keeps this command and the MCP
// tools from reaching different verdicts about the same graph.
//
// Both probes are skipped entirely when the symbol layer could not have held the answer
// (`kind:author` returning nothing has no bearing on a missing or stale symbol index), so an
// ordinary domain query pays nothing for the verdict.
func symbolCoverage(ctx context.Context, root, input string, seeded bool) knowledge.Coverage {
	if !knowledge.CouldMatchLazyLayer(input) {
		return knowledge.Coverage{Seeded: seeded}
	}
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		// A probe that could not run must not come back as an empty gap list: that reads
		// as verified coverage and would assert the very fact it failed to establish.
		return knowledge.Coverage{Seeded: seeded}
	}
	return measureSymbolCoverage(ctx, ws, globalCfg, input, seeded)
}

// measureSymbolCoverage is symbolCoverage over a workspace the caller already holds, read under
// cfg: the form a server answering for one of many workspaces calls.
func measureSymbolCoverage(ctx context.Context, ws graphWorkspace, cfg config.Config, input string, seeded bool) knowledge.Coverage {
	cov := knowledge.Coverage{Seeded: seeded}
	if !knowledge.CouldMatchLazyLayer(input) {
		return cov
	}
	cov.Gaps, cov.Probed = workspacegraph.SymbolGaps(ctx, ws, ws.Root(), cfg, slog.Default())
	cov.Stale = findStaleIndexes(ctx, ws)
	return cov
}

// noteShardErrors says a read's answer lacks a shard that failed to assemble.
func noteShardErrors(ctx context.Context, root, label string) {
	for _, msg := range knowledge.ShardErrors(config.CacheDir(root, globalCfg)) {
		slog.WarnContext(ctx, "this answer lacks "+msg, attr.Notice(label))
	}
}
