package magus

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/project"
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
		targets     []types.Target
		source      string
		unboundedBy string
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
		result.Changed = opts.ChangedPaths
		unboundedBy = m.unboundedBy(result)
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
		switch {
		case fellBack:
			unboundedBy = "the VCS could not diff, so the plan holds every project (" + source + ")"
		case res != nil:
			unboundedBy = m.unboundedBy(res)
		}
	}
	affected := make([]string, len(targets))
	for i, t := range targets {
		affected[i] = t.Path
	}
	slices.Sort(affected)

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
		UnboundedBy: unboundedBy,
	}, nil
}

// unboundedBy says why the closure computed from res is not a proof, or "" when it is.
func (m *Magus) unboundedBy(res *types.AffectedResult) string {
	claimed := make(map[string]bool, len(res.Changed))
	for _, files := range res.FilesBySeed {
		for _, f := range files {
			claimed[f] = true
		}
	}
	if len(res.Changed) > 0 {
		if name := m.opaqueProvider(); name != "" {
			return "workspace provider " + name + " declares no inputs, so any file may decide the project graph"
		}
	}
	return unboundedBy(res.Changed, claimed, m.edgeInputs())
}

// opaqueProvider names a wired workspace provider that declares no input globs, or "".
func (m *Magus) opaqueProvider() string {
	if m.wsReg == nil {
		return ""
	}
	for _, name := range m.wsReg.Providers() {
		if sp, ok := project.DefaultSpellRegistry().Lookup(name); !ok || len(sp.Sources()) == 0 {
			return name
		}
	}
	return ""
}

// edgeInputs matches the files that can move the graph's edges or every project's
// verdict at once without seeding the projects they reach.
func (m *Magus) edgeInputs() func(string) bool {
	names := map[string]bool{config.Filename: true, config.DottedFilename: true, remotespell.LockFile: true}
	for _, p := range m.ws.All() {
		for _, sp := range p.ResolvedSpells {
			for _, mf := range sp.Manifests() {
				names[path.Base(mf.Value)] = true
				for _, lock := range mf.LockCandidates {
					names[lock] = true
				}
			}
		}
	}
	var globs []string
	if m.wsReg != nil {
		for _, name := range m.wsReg.Providers() {
			if sp, ok := project.DefaultSpellRegistry().Lookup(name); ok {
				globs = append(globs, sp.Sources()...)
			}
		}
	}
	return func(p string) bool {
		if names[path.Base(p)] || strings.HasSuffix(p, ".buzz") || types.LooksLikeBuildInput(p) {
			return true
		}
		for _, g := range globs {
			if ok, _ := doublestar.Match(g, p); ok {
				return true
			}
		}
		return false
	}
}

// unboundedBy is the rule [Magus.unboundedBy] applies. The closure is computed from the
// declarations as they stand, so a change that edits them (a Buzz source, the workspace
// config or lock, a dependency manifest a spell or a workspace provider reads, a
// toolchain pin or rule set every project builds under) can add an edge the closure
// never saw. A path no project claims reaches nothing the closure can name.
func unboundedBy(changed []string, claimed map[string]bool, edgeInput func(string) bool) string {
	for _, p := range changed {
		if edgeInput(p) {
			return p + " can change the dependency graph or every project's build, which the affected set cannot see"
		}
	}
	for _, p := range changed {
		if !claimed[p] {
			return "no project claims " + p
		}
	}
	return ""
}
