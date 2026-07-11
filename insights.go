package magus

import (
	"context"
	"path/filepath"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// insightScan defaults opts.Dir to the workspace root and runs the shared history
// scan every insight lens aggregates from.
func (m *Magus) insightScan(ctx context.Context, opts *types.InsightOptions) ([]project.ScannedCommit, error) {
	if opts.Dir == "" {
		opts.Dir = m.ws.Root
	}
	return project.Scan(ctx, m.ws, opts.Dir, opts.Commits, opts.Since)
}

// Hotspots is the churn × complexity lens. The project view is the dependency graph
// heat-coloured by churn (with authors, recency, blast radius, and CI duration on each
// node); --files ranks individual files by edit frequency weighted by complexity.
func (m *Magus) Hotspots(ctx context.Context, opts types.InsightOptions) (types.HotspotOutput, error) {
	scan, err := m.insightScan(ctx, &opts)
	if err != nil {
		return types.HotspotOutput{}, err
	}
	g, err := m.Graph()
	if err != nil {
		return types.HotspotOutput{}, err
	}
	// Pull per-project CI duration onto the nodes (the "× CI cost" signal) when a
	// history file is configured; best-effort, silently skipped when unavailable.
	compose := []ComposeOption{WithGraphInput(g)}
	if path := m.cfg.HistoryPath; path != "" {
		var hist forecast.History
		if err := hist.Load(ctx, path); err == nil {
			compose = append(compose, WithGraphHistory(&hist, "ci"))
		}
	}
	out := ComposeGraph(m, compose...)
	stats := project.ProjectStats(scan)
	for i := range out.Nodes {
		st, ok := stats[out.Nodes[i].Path]
		if !ok {
			continue
		}
		out.Nodes[i].Churn = st.Commits
		out.Nodes[i].Authors = st.Authors
		if !st.Last.IsZero() {
			last := st.Last
			out.Nodes[i].LastCommit = &last
		}
	}
	res := types.HotspotOutput{
		Definition: types.HotspotDefinition,
		Commits:    opts.Commits,
		Since:      opts.Since,
		Nodes:      out.Nodes,
	}
	if opts.Files {
		res.Files = project.FileHotspots(scan, func(rel string) int {
			return project.Complexity(filepath.Join(m.ws.Root, rel))
		})
	}
	return res, nil
}

// Affinity is the temporal-coupling lens: projects that change together, with the
// pairs that lack any declared dependency between them flagged as hidden affinity.
func (m *Magus) Affinity(ctx context.Context, opts types.InsightOptions) (types.AffinityOutput, error) {
	scan, err := m.insightScan(ctx, &opts)
	if err != nil {
		return types.AffinityOutput{}, err
	}
	pairs := project.Affinity(scan)
	declared := m.declaredDeps()
	for i := range pairs {
		if !declared[pairs[i].A][pairs[i].B] && !declared[pairs[i].B][pairs[i].A] {
			pairs[i].Hidden = true
		}
	}
	return types.AffinityOutput{
		Definition: types.AffinityDefinition,
		Commits:    opts.Commits,
		Since:      opts.Since,
		Pairs:      pairs,
	}, nil
}

// declaredDeps returns each project's direct dependency set (both directions are
// looked up by the affinity lens to decide whether a co-change pair is hidden).
func (m *Magus) declaredDeps() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(m.ws.Projects))
	for _, p := range m.All() {
		set := make(map[string]bool, len(p.DependsOn))
		for _, d := range p.DependsOn {
			set[d] = true
		}
		out[p.Path] = set
	}
	return out
}

// Ownership is the knowledge-risk lens: author concentration, bus factor, and
// abandonment (projects gone quiet in the recent half of the window).
func (m *Magus) Ownership(ctx context.Context, opts types.InsightOptions) (types.OwnershipOutput, error) {
	scan, err := m.insightScan(ctx, &opts)
	if err != nil {
		return types.OwnershipOutput{}, err
	}
	return types.OwnershipOutput{
		Definition: types.OwnershipDefinition,
		Commits:    opts.Commits,
		Since:      opts.Since,
		Projects:   project.Ownership(scan, project.Midpoint(scan)),
	}, nil
}

// Volatility is the run-outcome lens: each (project, target) pair's recent pass/fail record
// scored by its Wilson lower bound, flagged volatile at or above the configured threshold.
// Unlike the git-history lenses it reads the shared runtime-history file (config.HistoryPath),
// not a commit scan - so it is workspace-wide and takes no InsightOptions window.
func (m *Magus) Volatility(ctx context.Context) (types.VolatilityReport, error) {
	return volatility.BuildReport(ctx, m.cfg.HistoryPath, m.volatilityConfig())
}

// Trend is the rising/cooling lens: each project's churn in the recent vs earlier
// half of the window.
func (m *Magus) Trend(ctx context.Context, opts types.InsightOptions) (types.TrendOutput, error) {
	scan, err := m.insightScan(ctx, &opts)
	if err != nil {
		return types.TrendOutput{}, err
	}
	return types.TrendOutput{
		Definition: types.TrendDefinition,
		Commits:    opts.Commits,
		Since:      opts.Since,
		Projects:   project.Trend(scan),
	}, nil
}
