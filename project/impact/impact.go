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
	"strings"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// Result is the typed impact report for a changeset. Counts sit alongside the
// backing lists so a formatter can lead with the count and expand the detail
// (the `magus graph explain` house style).
type Result struct {
	// Base is the ref the VCS diff was taken against ("paths" when computed from an
	// explicit path set rather than a diff).
	Base string `json:"base" yaml:"base"`
	// ChangedFileCount and ChangedFiles are the full changed-file set. Files outside
	// any project still count here (they just seed nothing).
	ChangedFileCount int      `json:"changed_file_count"      yaml:"changed_file_count"`
	ChangedFiles     []string `json:"changed_files,omitempty" yaml:"changed_files,omitempty"`
	// SeedProjects are the projects that directly contain a changed file, sorted.
	SeedProjects []string `json:"seed_projects,omitempty" yaml:"seed_projects,omitempty"`
	// AffectedProjects is the transitive reverse closure of the seeds (seeds
	// included), sorted by path. Each carries its target vocabulary and whether a
	// changed file lands in it directly.
	AffectedProjects []AffectedProject `json:"affected_projects,omitempty" yaml:"affected_projects,omitempty"`
	// TestProjectCount is how many affected projects expose at least one test target.
	TestProjectCount int `json:"test_project_count" yaml:"test_project_count"`
	// ChangedSymbols is the changed-symbol caller overlay: every symbol defined in a
	// changed source file, with how widely it is referenced repo-wide. It is what a
	// plain difftool structurally cannot show - the reach of an edited definition.
	// Populated by Enrich when a symbol index is loaded; empty (with a Note) otherwise.
	// Flattened across files and sorted by descending reference count so the
	// widest-reach change leads.
	ChangedSymbols []SymbolImpact `json:"changed_symbols,omitempty" yaml:"changed_symbols,omitempty"`
	// ChangedFileCoverage is the coverage overlay: the observed statement coverage of
	// each changed file the local coverage profile covers. Empty (with a Note) when no
	// `magus run coverage` profile is loaded. Go-only and observed, never extracted.
	ChangedFileCoverage []FileCoverageImpact `json:"changed_file_coverage,omitempty" yaml:"changed_file_coverage,omitempty"`
	// Notes carries graceful-degradation messages (deferred overlays, missing data).
	// It never blocks a report; a formatter prints it verbatim.
	Notes []string `json:"notes,omitempty" yaml:"notes,omitempty"`
}

// SymbolImpact is one changed symbol's caller spread (and coverage, when observed): the
// file that defines it, its identity, how many references and distinct referencing files
// the symbol index recorded, and its covered-statement ratio if a coverage profile is
// loaded.
type SymbolImpact struct {
	File      string    `json:"file"               yaml:"file"`
	Symbol    string    `json:"symbol"             yaml:"symbol"`
	Label     string    `json:"label,omitempty"    yaml:"label,omitempty"`
	RefCount  int       `json:"ref_count"          yaml:"ref_count"`
	FileCount int       `json:"file_count"         yaml:"file_count"`
	Coverage  *Coverage `json:"coverage,omitempty" yaml:"coverage,omitempty"`
}

// FileCoverageImpact is one changed file's observed file-level coverage.
type FileCoverageImpact struct {
	File     string   `json:"file"     yaml:"file"`
	Coverage Coverage `json:"coverage" yaml:"coverage"`
}

// Coverage is a covered/total statement tally and its ratio (0..1), mirrored from the
// knowledge graph's @coverage overlay so the impact report carries the raw counts.
type Coverage struct {
	Ratio   float64 `json:"ratio"         yaml:"ratio"`
	Covered int     `json:"covered_stmts" yaml:"covered_stmts"`
	Total   int     `json:"total_stmts"   yaml:"total_stmts"`
}

// SymbolStore is the narrow knowledge-graph surface the caller and coverage overlays
// read: whether any symbol index is loaded at all, and the per-file overlay facts. The
// concrete *knowledge.Graph satisfies it; a test supplies a fake. Keeping it an
// interface (rather than taking *knowledge.Graph directly) mirrors how Compute is framed
// against a narrow handle, so a future console or HTTP caller can enrich from its own
// store without pulling in the CLI's graph loader.
type SymbolStore interface {
	// HasSymbols reports whether the graph holds any ingested code symbol. False means
	// no SCIP index was ever built, so both overlays are unavailable (not merely empty).
	HasSymbols() bool
	// FileFacts returns the symbols defined in a workspace-relative file with their
	// reference spread and observed coverage. The zero value means the file has no
	// indexed symbol (a non-code file, or one absent from the index).
	FileFacts(relPath string) knowledge.FileFacts
}

// Enrich folds the changed-symbol caller and coverage overlays onto res, reading the
// loaded knowledge graph. It is the ENRICHMENT step the plan keeps out of the lean
// Compute: Compute runs against the workspace repository handle, while these overlays
// need the heavier knowledge store (a prior symbol index and/or `magus run coverage`),
// which the caller loads and passes here.
//
// It is deliberately additive and never fails: the blast radius Compute produced is left
// untouched, overlays are appended, and absent data degrades to a Note rather than an
// error. A nil store (the caller could not load a graph) or nil res is a no-op.
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

// AffectedProject is one project in the blast radius.
type AffectedProject struct {
	Path string `json:"path" yaml:"path"`
	// Seed is true when a changed file lands directly in this project (it is a root
	// of the closure, not only reached transitively).
	Seed bool `json:"seed" yaml:"seed"`
	// Files are the changed files inside this project, present only for seeds.
	Files []string `json:"files,omitempty" yaml:"files,omitempty"`
	// Spells are the project's bound spells (its toolchains).
	Spells []string `json:"spells,omitempty" yaml:"spells,omitempty"`
	// Targets is the project's target vocabulary: the spell-contributed ops plus any
	// custom magusfile targets that name it, sorted and deduplicated.
	Targets []string `json:"targets,omitempty" yaml:"targets,omitempty"`
	// TestTargets is the subset of Targets that look like test targets.
	TestTargets []string `json:"test_targets,omitempty" yaml:"test_targets,omitempty"`
}

// Compute derives the impact report from a VCS diff against base (empty base uses
// the workspace default). It reuses the workspace's own affected-set computation, so
// the closure it reports is exactly the set `magus affected <target>` would run.
func Compute(ctx context.Context, ws types.WorkspaceRepository, base string) (*Result, error) {
	r, err := ws.Affected(ctx, base)
	if err != nil {
		return nil, err
	}
	return build(ws, r), nil
}

// ComputeFromPaths derives the impact report from an explicit changed-path set
// (repo-relative or absolute-within-workspace), bypassing the VCS. It is the seam a
// non-git caller (a watch loop, a console request carrying a diff) uses.
func ComputeFromPaths(ctx context.Context, ws types.WorkspaceRepository, paths []string) (*Result, error) {
	r, err := ws.AffectedFromPaths(ctx, paths)
	if err != nil {
		return nil, err
	}
	return build(ws, r), nil
}

// build turns a raw AffectedResult into the enriched, formatter-ready report. It is
// pure: no I/O, deterministic ordering.
func build(ws types.WorkspaceRepository, r *types.AffectedResult) *Result {
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
	customByProject := customTargetsByProject(ws)

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
		for _, t := range ap.Targets {
			if isTestTarget(t) {
				ap.TestTargets = append(ap.TestTargets, t)
			}
		}
		if len(ap.TestTargets) > 0 {
			res.TestProjectCount++
		}
		res.AffectedProjects = append(res.AffectedProjects, ap)
	}
	return res
}

// customTargetsByProject inverts the workspace target inventory into a
// project-path -> custom-target-names map. Custom targets are magusfile export funs
// (e.g. build, test, lint, ci here) that no spell contributes; DescribeTargets is the
// one surface that attributes them to projects.
func customTargetsByProject(ws types.WorkspaceRepository) map[string][]string {
	out := map[string][]string{}
	for _, t := range ws.DescribeTargets().Targets {
		if t.Kind != "custom" {
			continue
		}
		for _, p := range t.Projects {
			out[p] = append(out[p], t.Name)
		}
	}
	return out
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

// isTestTarget reports whether a target name reads as a test target: the canonical
// "test", any "<tool>-test" op (go-test), or a name that otherwise carries "test".
// A heuristic on purpose - target names are workspace-defined, so there is no
// authoritative "this is the test target" flag to key on.
func isTestTarget(name string) bool {
	return strings.Contains(strings.ToLower(name), "test")
}
