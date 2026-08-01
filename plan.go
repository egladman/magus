package magus

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
)

// PlanOptions configures a [Magus.Plan] call.
type PlanOptions struct {
	// MaxShards caps the number of CI shards. -1 = unlimited; 0 uses the
	// value from magus.yaml (CI.MaxShards).
	MaxShards int
	// RunnerPoolBudget limits cross-shard concurrency. 0 = unlimited.
	RunnerPoolBudget int
	// HistoryPath overrides the configured history_path when non-empty.
	HistoryPath string
	// BaseRef overrides the VCS base used to compute the affected set.
	// It is mutually exclusive with ChangedPaths.
	BaseRef string
	// ChangedPaths computes the affected set from these repo-relative paths
	// instead of a VCS diff. A non-nil empty slice deliberately means no paths.
	ChangedPaths []string
}

// Plan computes a provider-neutral CI shard plan for the affected project
// set using target as the CI target (typically "ci"). Adaptive sharding is applied
// when runtime history is available at the resolved HistoryPath.
func (m *Magus) Plan(ctx context.Context, target string, opts PlanOptions) (types.ShardPlan, error) {
	if opts.ChangedPaths != nil && opts.BaseRef != "" {
		return types.ShardPlan{}, fmt.Errorf("affected plan: changed paths and base ref are mutually exclusive")
	}

	var (
		targets []types.Target
		source  string
	)
	if opts.ChangedPaths != nil {
		result, err := m.AffectedFromPaths(ctx, opts.ChangedPaths)
		if err != nil {
			return types.ShardPlan{}, fmt.Errorf("affected plan: compute paths: %w", err)
		}
		targets = make([]types.Target, len(result.Affected))
		for i, path := range result.Affected {
			targets[i] = types.Target{Path: path, Name: target, Files: result.FilesBySeed[path]}
		}
		source = "stdin paths"
	} else {
		var err error
		targets, source, _, err = m.ExpandAffected(ctx, target, opts.BaseRef)
		if err != nil {
			return types.ShardPlan{}, err
		}
	}

	projects := make([]*types.Project, 0, len(targets))
	for _, t := range targets {
		if p := m.Get(t.Path); p != nil {
			projects = append(projects, p)
		}
	}

	histPath := opts.HistoryPath
	if histPath == "" {
		histPath = m.cfg.HistoryPath
	}
	var hist forecast.History
	if err := hist.Load(ctx, histPath); err != nil {
		return types.ShardPlan{}, fmt.Errorf("affected plan: load history: %w", err)
	}

	f := forecast.Forecaster{History: hist, Target: target}
	tags := make(map[string][]string, len(targets))
	for _, t := range targets {
		if len(t.Files) > 0 {
			tags[t.Path] = forecast.Tags(t.Path, t.Files)
		}
	}
	if len(tags) > 0 {
		f.TagsByProject = tags
	}

	maxShards := opts.MaxShards
	if maxShards == 0 {
		maxShards = m.cfg.CI.MaxShards
	}

	plan, err := ci.Build(projects, source, ci.WithMaxShards(maxShards), ci.WithForecaster(f))
	if err != nil {
		return types.ShardPlan{}, fmt.Errorf("affected plan: %w", err)
	}

	maxParallel := len(plan.Shards)
	if opts.RunnerPoolBudget > 0 && opts.RunnerPoolBudget < maxParallel {
		maxParallel = opts.RunnerPoolBudget
	}

	shards := make([]types.Shard, len(plan.Shards))
	for i, s := range plan.Shards {
		paths := make([]string, len(s.Projects))
		for j, p := range s.Projects {
			paths[j] = p.Path
		}
		shards[i] = types.Shard{ID: s.ID, ProjectPaths: paths}
	}
	return types.ShardPlan{
		Shards:      shards,
		Source:      source,
		MaxParallel: maxParallel,
	}, nil
}
