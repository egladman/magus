package mcp

import (
	"context"
	"fmt"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// insightAnalyzer is the workspace capability the insight tool needs; the real
// *magus.Magus passed as opts.Magus satisfies it.
type insightAnalyzer interface {
	Hotspots(ctx context.Context, opts types.InsightOptions) (types.HotspotOutput, error)
	Affinity(ctx context.Context, opts types.InsightOptions) (types.AffinityOutput, error)
	Ownership(ctx context.Context, opts types.InsightOptions) (types.OwnershipOutput, error)
	Trend(ctx context.Context, opts types.InsightOptions) (types.TrendOutput, error)
	Unreferenced(ctx context.Context) (types.UnreferencedOutput, error)
}

type insightTool struct {
	ws types.WorkspaceRepository
}

func (t *insightTool) Name() string { return "magus_insight" }

func (t *insightTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	analyzer, ok := t.ws.(insightAnalyzer)
	if !ok {
		return spells.InvokeResponse{}, fmt.Errorf("mcp: workspace does not support insight analysis")
	}
	// Agents have no meaningful cwd, so Dir is left empty (whole workspace).
	opts := types.InsightOptions{
		Commits: int(paramFloat(req.Params, "commits", 500)),
		Since:   paramString(req.Params, "since", ""),
	}
	lens := paramString(req.Params, "lens", "hotspots")

	var (
		data any
		err  error
	)
	switch lens {
	case "hotspots", "files":
		opts.Files = lens == "files"
		data, err = analyzer.Hotspots(ctx, opts)
	case "affinity":
		data, err = analyzer.Affinity(ctx, opts)
	case "ownership":
		data, err = analyzer.Ownership(ctx, opts)
	case "trend":
		data, err = analyzer.Trend(ctx, opts)
	case "unreferenced":
		// Takes no window: it reads the knowledge graph, not the commit log.
		data, err = analyzer.Unreferenced(ctx)
	default:
		return spells.InvokeResponse{}, fmt.Errorf("mcp: unknown insight lens %q (use hotspots, files, affinity, ownership, trend, or unreferenced)", lens)
	}
	if err != nil {
		toolLogger(ctx).WarnContext(ctx, "mcp: insight computation failed", "error", err)
		return spells.InvokeResponse{}, fmt.Errorf("mcp: insight: %w", err)
	}
	return spells.InvokeResponse{Data: data}, nil
}

var _ spells.Driver = (*insightTool)(nil)
