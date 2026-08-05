// Package impact computes the forensic blast radius of a changeset: the changed
// files, the projects that directly contain them (seeds), and the transitive set of
// projects and targets a change ripples out to via the dependency-graph reverse
// closure. It is read-only - it names what a change touches, it never executes a
// target.
//
// The engine is deliberately framed against the narrow types.WorkspaceRepository
// interface (the same handle `magus affected --explain` uses) so a future console or
// HTTP caller can reuse it without depending on the concrete engine. The CLI handler
// (`magus affected --impact`) formats the returned Result; this package does no I/O.
package impact

import (
	"cmp"
	"context"
	"slices"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// SymbolStore is the narrow knowledge-graph surface the caller and coverage overlays
// read. The concrete *knowledge.Graph satisfies it; a future console or HTTP caller can
// enrich from its own store, and tests supply a fake.
type SymbolStore interface {
	// HasSymbols reports whether the graph holds any ingested code symbol. False means
	// no SCIP index was ever built, so both overlays are unavailable (not merely empty).
	HasSymbols() bool
	// FileFacts returns the symbols defined in a workspace-relative file with their
	// reference spread and observed coverage. The zero value means the file has no
	// indexed symbol (a non-code file, or one absent from the index).
	FileFacts(relPath string) knowledge.FileFacts
}

// Enrich folds the changed-symbol caller and coverage overlays onto res from the loaded
// knowledge graph (a symbol index, and for coverage a prior `magus run coverage`), which
// Compute does not read. It is additive and never fails: the blast radius is untouched,
// overlays are appended, and absent data degrades to a Note. A nil store or nil res is a
// no-op.
// The report shape is a DOMAIN type (types.ImpactResult and friends): magus.affectedImpact
// hands it to a magusfile, so a caller reads the affected set as values rather than
// decoding JSON. Aliased rather than moved outright so this package keeps its own names.
type (
	Result             = types.ImpactResult
	AffectedProject    = types.ImpactProject
	SymbolImpact       = types.ImpactSymbol
	FileCoverageImpact = types.ImpactFileCoverage
	Coverage           = types.ImpactCoverage
)

func Enrich(res *Result, store SymbolStore) {
	if res == nil || store == nil {
		return
	}
	if !store.HasSymbols() {
		res.Notes = append(res.Notes,
			"no symbol index loaded: changed-symbol callers and coverage overlays are unavailable (build it with `magus graph build`)")
		return
	}

	coverageSeen := false
	for _, f := range res.ChangedFiles {
		ff := store.FileFacts(f)
		if ff.Coverage != nil {
			res.ChangedFileCoverage = append(res.ChangedFileCoverage, FileCoverageImpact{
				File:     f,
				Coverage: toCoverage(ff.Coverage),
			})
			coverageSeen = true
		}
		for _, s := range ff.Symbols {
			si := SymbolImpact{
				File:      f,
				Symbol:    s.ID,
				Label:     s.Label,
				RefCount:  s.RefCount,
				FileCount: s.FileCount,
			}
			if s.Coverage != nil {
				c := toCoverage(s.Coverage)
				si.Coverage = &c
				coverageSeen = true
			}
			res.ChangedSymbols = append(res.ChangedSymbols, si)
		}
	}

	// Flatten-and-sort by descending reference count (widest blast radius first), then
	// file and symbol id for a deterministic tie-break.
	slices.SortFunc(res.ChangedSymbols, func(a, b SymbolImpact) int {
		if c := cmp.Compare(b.RefCount, a.RefCount); c != 0 {
			return c
		}
		if c := cmp.Compare(a.File, b.File); c != 0 {
			return c
		}
		return cmp.Compare(a.Symbol, b.Symbol)
	})

	if len(res.ChangedSymbols) == 0 {
		res.Notes = append(res.Notes,
			"symbol index loaded, but no changed file defines an indexed symbol (callers overlay empty)")
	}
	if !coverageSeen {
		res.Notes = append(res.Notes,
			"no coverage data on changed files (run `magus run coverage` to populate it)")
	}
}

// toCoverage narrows the knowledge-graph coverage facts to the impact report's own
// Coverage type, so the impact JSON does not leak the internal graph type.
func toCoverage(c *knowledge.CoverageFacts) Coverage {
	return Coverage{Ratio: c.Ratio, Covered: c.Covered, Total: c.Total}
}

// Compute derives the impact report from a VCS diff against base (empty base uses
// the workspace default). It reuses the workspace's own affected-set computation, so
// the closure it reports is exactly the set `magus affected <target>` would run.
func Compute(ctx context.Context, ws types.WorkspaceRepository, base string) (*Result, error) {
	r, err := ws.Affected(ctx, base)
	if err != nil {
		return nil, err
	}
	return build(ctx, ws, r)
}

// ComputeFromPaths derives the impact report from an explicit changed-path set
// (repo-relative or absolute-within-workspace), bypassing the VCS. It is the seam a
// non-git caller (a watch loop, a console request carrying a diff) uses.
func ComputeFromPaths(ctx context.Context, ws types.WorkspaceRepository, paths []string) (*Result, error) {
	r, err := ws.AffectedFromPaths(ctx, paths)
	if err != nil {
		return nil, err
	}
	return build(ctx, ws, r)
}

// build turns a raw AffectedResult into the enriched, formatter-ready report. It is
// pure aside from the ListTargets read (customTargetsByProject): no I/O of its
// own, deterministic ordering.
func build(ctx context.Context, ws types.WorkspaceRepository, r *types.AffectedResult) (*Result, error) {
	changed := slices.Clone(r.Changed)
	slices.Sort(changed)

	seeds := slices.Clone(r.Seed)
	slices.Sort(seeds)
	seedSet := make(map[string]struct{}, len(seeds))
	for _, s := range seeds {
		seedSet[s] = struct{}{}
	}

	// A project can host a custom (export fun) target that no spell contributes; those
	// live on the workspace target inventory keyed by project, not on the project's
	// resolved spells. Pull them once so per-project enrichment sees the full vocabulary.
	customByProject, err := customTargetsByProject(ctx, ws)
	if err != nil {
		return nil, err
	}

	res := &Result{
		Base:             r.Base,
		ChangedFileCount: len(changed),
		ChangedFiles:     changed,
		SeedProjects:     seeds,
	}

	for _, path := range r.Affected {
		ap := AffectedProject{
			Path:    path,
			Targets: projectTargets(ws, path, customByProject),
		}
		if _, ok := seedSet[path]; ok {
			ap.Seed = true
			ap.Files = slices.Clone(r.FilesBySeed[path])
			slices.Sort(ap.Files)
		}
		if p := ws.Get(path); p != nil {
			ap.Spells = slices.Clone(p.Spells)
		}
		res.AffectedProjects = append(res.AffectedProjects, ap)
	}
	return res, nil
}

// customTargetsByProject inverts the workspace target inventory into a
// project-path -> custom-target-names map. Custom targets are magusfile export funs
// (e.g. build, test, lint, ci here) that no spell contributes; ListTargets is the
// one surface that attributes them to projects.
func customTargetsByProject(ctx context.Context, ws types.WorkspaceRepository) (map[string][]string, error) {
	targets, err := ws.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, t := range targets {
		if t.Kind != "custom" {
			continue
		}
		for _, p := range t.Projects {
			out[p] = append(out[p], t.Name)
		}
	}
	return out, nil
}

// projectTargets returns the sorted, deduplicated target vocabulary a project
// exposes: its resolved spells' ops unioned with any custom targets that name it.
func projectTargets(ws types.WorkspaceRepository, path string, customByProject map[string][]string) []string {
	set := map[string]struct{}{}
	if p := ws.Get(path); p != nil {
		for _, s := range p.ResolvedSpells {
			for _, t := range s.Targets() {
				set[t] = struct{}{}
			}
		}
	}
	for _, t := range customByProject[path] {
		set[t] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}
