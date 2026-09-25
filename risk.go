package magus

import (
	"context"
	"time"

	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/ci/risk"
	"github.com/egladman/magus/project/impact"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// RiskOptions configures Risk.
type RiskOptions struct {
	// Base is the diff base, or with ChangedPaths the revision base-side content
	// is read at. Empty with ChangedPaths means nothing can be proven comment- or
	// format-only.
	Base string
	// ChangedPaths replaces the VCS diff when non-nil.
	ChangedPaths []string
	// HistoryPath is the run history statistical pruning reads. An unreadable
	// history prunes nothing and says so in the report's evidence.
	HistoryPath string
	// MinRuns and Window are ci.risk_min_runs and ci.risk_window.
	MinRuns int
	Window  time.Duration
}

// Risk classifies the changeset by how much of target's gate it provably needs
// and returns the reduced gate; see internal/ci/risk. It is read-only and runs
// nothing but `go list` in the Go modules the change touches.
func (m *Magus) Risk(ctx context.Context, target string, opts RiskOptions) (types.RiskReport, error) {
	var res *types.ImpactResult
	var err error
	base, label := opts.Base, "paths"
	if opts.ChangedPaths != nil {
		res, err = impact.ComputeFromPaths(ctx, m, opts.ChangedPaths)
	} else {
		res, err = impact.Compute(ctx, m, opts.Base)
		if res != nil {
			base, label = res.Base, res.Base
		}
	}
	if err != nil {
		return types.RiskReport{}, err
	}
	entries, err := m.ClassifyFiles(ctx, res.ChangedFiles)
	if err != nil {
		return types.RiskReport{}, err
	}
	var resolved []*spells.Spell
	for _, p := range m.All() {
		resolved = append(resolved, p.ResolvedSpells...)
	}
	in := risk.Inputs{
		Root:     m.Root(),
		Target:   target,
		Base:     base,
		Label:    label,
		Changed:  res.ChangedFiles,
		Files:    entries,
		Affected: res.AffectedProjects,
		Projects: m.All(),
		Prose:    ci.ProseScopes(m.All()),
		Syntax:   spells.CommentSyntaxIndex(resolved),
		GoList:   risk.GoList,
		MinRuns:  opts.MinRuns,
		Window:   opts.Window,
		Now:      time.Now(),
	}
	if vr, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions()); err == nil && vr.VCS != nil {
		drv := vr.VCS
		in.At = func(ctx context.Context, rev, p string) (string, error) { return drv.ReadFileAt(ctx, m.Root(), rev, p) }
	}
	var hist forecast.History
	histErr := hist.Load(ctx, opts.HistoryPath)
	if histErr == nil {
		in.History = &hist
	}
	rep, err := risk.Assess(ctx, in)
	if err != nil {
		return types.RiskReport{}, err
	}
	if histErr != nil {
		rep.Evidence = append(rep.Evidence, types.RiskEvidence{Tier: rep.Tier,
			Why: "run history unreadable, so nothing was pruned statistically: " + histErr.Error()})
	}
	return rep, nil
}
