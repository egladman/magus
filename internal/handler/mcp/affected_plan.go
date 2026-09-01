package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/spells"
)

type affectedPlanShard struct {
	Shard    string `json:"shard"`
	Projects string `json:"projects"`
}

type affectedPlanResult struct {
	Count       int                 `json:"count"`
	MaxParallel int                 `json:"max_parallel"`
	Source      string              `json:"source"`
	Matrix      []affectedPlanShard `json:"matrix"`
}

type affectedPlanTool struct {
	opts Options
}

func (t *affectedPlanTool) Name() string { return hint.ToolAffectedPlan.String() }

func (t *affectedPlanTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	target := strings.TrimSpace(paramString(req.Params, "target", "ci"))
	if target == "" {
		return spells.InvokeResponse{}, fmt.Errorf("mcp: affected plan: target is required")
	}
	maxShards := t.opts.Config.CI.MaxShards
	if v := paramFloat(req.Params, "max_shards", 0); v != 0 {
		maxShards = int(v)
	}

	plan, err := t.opts.Magus.Plan(ctx, target, magus.PlanOptions{
		MaxShards:        maxShards,
		RunnerPoolBudget: t.opts.Config.CI.RunnerPoolBudget,
		HistoryPath:      t.opts.Config.HistoryPath,
		BaseRef:          paramString(req.Params, "base", ""),
	})
	if err != nil {
		toolLogger(ctx).WarnContext(ctx, "mcp: build plan failed", "error", err)
		return spells.InvokeResponse{}, fmt.Errorf("mcp: build plan: %w", err)
	}

	out := affectedPlanResult{
		Count:       len(plan.Shards),
		MaxParallel: plan.MaxParallel,
		Source:      plan.Source,
		Matrix:      make([]affectedPlanShard, len(plan.Shards)),
	}
	for i, s := range plan.Shards {
		out.Matrix[i] = affectedPlanShard{
			Shard:    s.ID,
			Projects: strings.Join(s.ProjectPaths, " "),
		}
	}
	return spells.InvokeResponse{Data: out}, nil
}

var _ spells.Driver = (*affectedPlanTool)(nil)
