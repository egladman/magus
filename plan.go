package magus

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

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
		targets   []types.Target
		source    string
		changed   []string
		unbounded string
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
		source, changed = "stdin paths", opts.ChangedPaths
	} else {
		var (
			fellBack bool
			res      *types.AffectedResult
			err      error
		)
		targets, source, fellBack, res, err = m.ExpandAffectedSet(ctx, target, opts.BaseRef)
		if err != nil {
			return types.ShardPlan{}, err
		}
		if fellBack {
			unbounded = source
		} else if res != nil {
			changed = res.Changed
		}
	}
	affected := make([]string, len(targets))
	for i, t := range targets {
		affected[i] = t.Path
	}
	slices.Sort(affected)
	if unbounded == "" {
		unbounded = unboundedBy(changed, affected)
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
	// Both are asked of the concrete forecaster rather than carried out of ci.Build: the
	// Forecaster interface returns an assignment and nothing about how it was reached, so
	// a plan built from real history and one built from fallbacks are the same value.
	assignments := make([][]*types.Project, len(plan.Shards))
	for i, s := range plan.Shards {
		assignments[i] = s.Projects
	}
	var overBudget []string
	for _, i := range f.OverBudget(assignments) {
		overBudget = append(overBudget, plan.Shards[i].ID)
	}
	return types.ShardPlan{
		Shards:      shards,
		Source:      source,
		MaxParallel: maxParallel,
		Sufficient:  f.SufficientShards(projects),
		OverBudget:  overBudget,
		Affected:    affected,
		Unbounded:   unbounded,
	}, nil
}

// unboundedBy says why the closure computed from changed is not a proof, or "" when it
// is. The closure comes from the declarations as they stand, so a change set that edits
// them (any Buzz source, since a magusfile can import it, or the workspace config and
// lockfile) can add an edge the closure never saw.
func unboundedBy(changed, affected []string) string {
	for _, p := range changed {
		switch filepath.Base(p) {
		case "magus.yaml", "magus.yml", "magus.lock":
			return p + " changes the declarations the affected set was computed from"
		}
		if strings.HasSuffix(p, ".buzz") {
			return p + " changes the declarations the affected set was computed from"
		}
	}
	if len(affected) == 0 && len(changed) > 0 {
		return "no project claims " + changed[0]
	}
	return ""
}
