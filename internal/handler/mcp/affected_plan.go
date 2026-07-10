package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
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

func (t *affectedPlanTool) Name() string { return "magus_affected_plan" }

func (t *affectedPlanTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	maxShards := t.opts.Config.CI.MaxShards
	if v := paramFloat(req.Params, "max_shards", 0); v != 0 {
		maxShards = int(v)
	}

	ws := t.opts.Magus

	targets, source, _, err := ws.ExpandAffected(ctx, "ci", "")
	if err != nil {
		toolLogger(ctx).Warn("mcp: expand affected failed", "error", err)
		return types.InvokeResponse{}, fmt.Errorf("mcp: expand affected: %w", err)
	}

	projects := make([]*types.Project, 0, len(targets))
	for _, tg := range targets {
		if p := ws.Get(tg.Path); p != nil {
			projects = append(projects, p)
		}
	}

	var hist forecast.History
	if err := hist.Load(ctx, t.opts.Config.HistoryPath); err != nil {
		toolLogger(ctx).Warn("mcp: load history failed", "error", err)
		return types.InvokeResponse{}, fmt.Errorf("mcp: load history: %w", err)
	}

	f := forecast.Forecaster{History: hist, Target: "ci"}
	tags := make(map[string][]string, len(targets))
	for _, tg := range targets {
		if len(tg.Files) > 0 {
			tags[tg.Path] = forecast.Tags(tg.Path, tg.Files)
		}
	}
	if len(tags) > 0 {
		f.TagsByProject = tags
	}

	ciOpts := []ci.Option{
		ci.WithMaxShards(maxShards),
		ci.WithForecaster(f),
	}
	plan, err := ci.Build(projects, source, ciOpts...)
	if err != nil {
		toolLogger(ctx).Warn("mcp: build plan failed", "error", err)
		return types.InvokeResponse{}, fmt.Errorf("mcp: build plan: %w", err)
	}

	runnerBudget := t.opts.Config.CI.RunnerPoolBudget
	maxParallel := len(plan.Shards)
	if runnerBudget > 0 && runnerBudget < maxParallel {
		maxParallel = runnerBudget
	}

	out := affectedPlanResult{
		Count:       len(plan.Shards),
		MaxParallel: maxParallel,
		Source:      source,
		Matrix:      make([]affectedPlanShard, len(plan.Shards)),
	}
	for i, s := range plan.Shards {
		paths := make([]string, len(s.Projects))
		for j, p := range s.Projects {
			paths[j] = p.Path
		}
		out.Matrix[i] = affectedPlanShard{
			Shard:    s.ID,
			Projects: strings.Join(paths, " "),
		}
	}
	return types.InvokeResponse{Data: out}, nil
}

var _ types.SpellDriver = (*affectedPlanTool)(nil)
