// Package impact computes the forensic blast radius of a changeset: the changed
// files, the projects that directly contain them (seeds), and the transitive set of
// projects and targets a change ripples out to via the dependency-graph reverse
// closure. It is read-only: it names what a change touches, it never executes a
// target.
//
// The engine is deliberately framed against the narrow types.WorkspaceRepository
// interface (the same handle `magus affected --explain` uses) so a future console or
// HTTP caller can reuse it without depending on the concrete engine. The CLI handler
// (`magus affected --impact`) formats the returned types.ImpactResult; this package does no I/O.
package impact

import (
	"cmp"
	"context"
	"slices"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// SymbolStore is the narrow interface the caller and coverage overlays read. The
// concrete *knowledge.Graph satisfies it through GraphStore; a console or HTTP caller
// can enrich from its own store, and tests supply a fake.
//
// Its types are declared HERE rather than reused from internal/graph/knowledge. That
// package is internal, so naming its types in this exported interface made the
// interface unimplementable by anyone outside the module, which is precisely the reuse
// the doc promised. An interface that cannot be satisfied by its stated audience is a
// concrete dependency wearing an interface's clothes.
type SymbolStore interface {
	// HasSymbols reports whether the store holds any ingested code symbol. False means
	// no SCIP index was ever built, so both overlays are unavailable (not merely empty).
	HasSymbols() bool
	// FileFacts returns the symbols defined in a workspace-relative file with their
	// reference spread and observed coverage. The zero value means the file has no
	// indexed symbol (a non-code file, or one absent from the index).
	FileFacts(relPath string) FileFacts
}

// FileFacts is one file's overlay: its own coverage, and the symbols it defines.
type FileFacts struct {
	// Coverage is the file-level observed coverage, or nil when no profile covers this
	// file (no `magus run coverage`, or the file has no statements).
	Coverage *Coverage
	// Symbols are the symbols defined in the file, sorted by descending reference count
	// then ID, so the widest-reach symbol leads.
	Symbols []SymbolFacts
}

// SymbolFacts is one symbol defined in a changed file: its identity, how widely it is
// referenced, and its own observed coverage when a profile is loaded.
type SymbolFacts struct {
	ID        string
	Label     string
	RefCount  int
	FileCount int
	Coverage  *Coverage
}

// Coverage is a covered/total statement tally and its ratio.
type Coverage struct {
	Ratio   float64
	Covered int
	Total   int
}

// GraphStore adapts the concrete knowledge graph to SymbolStore. It names an internal
// type, so only in-module callers can reach it, which is correct: an outside caller
// implements SymbolStore directly rather than going through the graph.
func GraphStore(g *knowledge.Graph) SymbolStore { return graphStore{g: g} }

type graphStore struct{ g *knowledge.Graph }

func (s graphStore) HasSymbols() bool { return s.g.HasSymbols() }

func (s graphStore) FileFacts(relPath string) FileFacts {
	ff := s.g.FileFacts(relPath)
	out := FileFacts{Coverage: fromGraphCoverage(ff.Coverage)}
	for _, sym := range ff.Symbols {
		out.Symbols = append(out.Symbols, SymbolFacts{
			ID:        sym.ID,
			Label:     sym.Label,
			RefCount:  sym.RefCount,
			FileCount: sym.FileCount,
			Coverage:  fromGraphCoverage(sym.Coverage),
		})
	}
	return out
}

func fromGraphCoverage(c *knowledge.CoverageFacts) *Coverage {
	if c == nil {
		return nil
	}
	return &Coverage{Ratio: c.Ratio, Covered: c.Covered, Total: c.Total}
}

// Enrich folds the changed-symbol caller and coverage overlays onto res from the loaded
// knowledge graph (a symbol index, and for coverage a prior `magus run coverage`), which
// Compute does not read. It is additive and never fails: the blast radius is untouched,
// overlays are appended, and absent data degrades to a Note. A nil store or nil res is a
// no-op.
//
// The report shape is a DOMAIN type (types.ImpactResult and friends): magus\impact
// hands it to a magusfile, so a caller reads the affected set as values rather than
// decoding JSON.
func Enrich(res *types.ImpactResult, store SymbolStore) {
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
			res.ChangedFileCoverage = append(res.ChangedFileCoverage, types.ImpactFileCoverage{
				File:     f,
				Coverage: toCoverage(ff.Coverage),
			})
			coverageSeen = true
		}
		for _, s := range ff.Symbols {
			si := types.ImpactSymbol{
				File:      f,
				Symbol:    s.ID,
				Label:     s.Label,
				RefCount:  s.RefCount,
				FileCount: s.FileCount,
			}
			if s.Coverage != nil {
				si.Coverage = toCoverage(s.Coverage)
				coverageSeen = true
			}
			res.ChangedSymbols = append(res.ChangedSymbols, si)
		}
	}

	// Flatten-and-sort by descending reference count (widest blast radius first), then
	// file and symbol id for a deterministic tie-break.
	slices.SortFunc(res.ChangedSymbols, func(a, b types.ImpactSymbol) int {
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

// HunkLines is the new-side lines one hunk of a patch changed. Index is the hunk's position
// within its file as changeset.Parse numbers it, so a hunk that changed no line leaves a gap
// rather than renumbering the ones after it.
type HunkLines struct {
	Index int
	Lines []int
}

// ChangedLinesByHunk maps each file a unified patch touches to the new-side lines each of its
// hunks changed, in hunk order. A removed run counts as the new-side line right after it, so a
// deletion inside a definition lands in that definition. A hunk that changed no line is left
// out.
func ChangedLinesByHunk(patch string) map[string][]HunkLines {
	out := map[string][]HunkLines{}
	for _, f := range changeset.Parse(patch) {
		for _, h := range f.Hunks {
			var lines []int
			next := h.NewStart
			for _, r := range h.Rows {
				switch {
				case r.NewLine != nil:
					next = *r.NewLine + 1
					if r.Kind == changeset.KindAdd {
						lines = append(lines, *r.NewLine)
					}
				case r.Kind == changeset.KindDel:
					lines = append(lines, next)
				}
			}
			if len(lines) > 0 {
				out[f.Path] = append(out[f.Path], HunkLines{Index: h.Index, Lines: lines})
			}
		}
	}
	return out
}

// ChangedLines is ChangedLinesByHunk with each file's hunks flattened into one list.
func ChangedLines(patch string) map[string][]int {
	out := map[string][]int{}
	for path, hunks := range ChangedLinesByHunk(patch) {
		for _, h := range hunks {
			out[path] = append(out[path], h.Lines...)
		}
	}
	return out
}

// Span is the lines one symbol's definition occupies in a file. End is 0 where the indexer
// recorded no enclosing range.
type Span struct {
	ID         string
	Start, End int
}

// Touched returns the IDs of the spans a changed line falls in. A span with an End contains
// the lines from Start to End, and one without always owns its Start line. A line no ranged
// span contains also belongs to the nearest definition above it when that one has no End,
// the rule [knowledge.Graph.SymbolAt] uses, so an indexer that records no ranges still places
// each change in a declaration. Inside a ranged span that rule would hand a new field's lines
// to the field declared above it, so a definition a closed range encloses is never nearest.
func Touched(spans []Span, lines []int) map[string]bool {
	// enclosedTo is the last line of the ranges enclosing each span's start, itself aside.
	enclosedTo := make([]int, len(spans))
	for i, s := range spans {
		for j, r := range spans {
			if i != j && r.End > 0 && r.Start <= s.Start && s.Start <= r.End {
				enclosedTo[i] = max(enclosedTo[i], r.End)
			}
		}
	}
	out := map[string]bool{}
	for _, l := range lines {
		contained, nearest := false, 0
		for i, s := range spans {
			switch {
			case s.End > 0 && s.Start <= l && l <= s.End:
				out[s.ID] = true
				contained = true
			case s.End == 0 && s.Start == l:
				out[s.ID] = true
			}
			if s.Start <= l && (enclosedTo[i] == 0 || enclosedTo[i] >= l) {
				nearest = max(nearest, s.Start)
			}
		}
		if contained {
			continue
		}
		for _, s := range spans {
			if s.End == 0 && s.Start > 0 && s.Start == nearest {
				out[s.ID] = true
			}
		}
	}
	return out
}

// CallGraph is what PublicThrough walks.
type CallGraph interface {
	// Callers are the symbols with a calls edge into id, in a stable order.
	Callers(id string) []string
	// Boundary is the DiffBoundary constant naming what a file referencing id sits outside
	// of, or empty when every referencing file shares id's package and project.
	Boundary(id string) string
	// Qualified is id's name through its enclosing declarations.
	Qualified(id string) string
}

// The bounds on one PublicThrough walk. A widely called helper fans out fast, and past a few
// hops the chain says more about the call graph than about the change.
const (
	reachDepth = 4
	reachLimit = 10
	reachVisit = 500
)

// PublicThrough walks id's callers breadth first and returns each one that crosses a package or
// project boundary, with the callers between. A path stops at its first such caller, because
// that is where the change becomes visible to other code.
func PublicThrough(g CallGraph, id string) []types.DiffPublicPath {
	type hop struct {
		id  string
		via []string
	}
	seen := map[string]bool{id: true}
	frontier := []hop{{id: id}}
	var out []types.DiffPublicPath
	for depth := 0; depth < reachDepth && len(frontier) > 0 && len(seen) < reachVisit; depth++ {
		var next []hop
		for _, h := range frontier {
			for _, c := range g.Callers(h.id) {
				if seen[c] {
					continue
				}
				seen[c] = true
				if b := g.Boundary(c); b != "" {
					out = append(out, types.DiffPublicPath{ID: c, Qualified: g.Qualified(c), Via: h.via, Boundary: b})
					if len(out) == reachLimit {
						return out
					}
					continue
				}
				next = append(next, hop{id: c, via: append(slices.Clone(h.via), g.Qualified(c))})
			}
		}
		frontier = next
	}
	return out
}

// toCoverage narrows the knowledge-graph coverage facts to the report's own
// types.ImpactCoverage, so the impact JSON does not leak the internal graph type.
func toCoverage(c *Coverage) types.ImpactCoverage {
	return types.ImpactCoverage{Ratio: c.Ratio, Covered: c.Covered, Total: c.Total}
}

// Compute derives the impact report from a VCS diff against base (empty base uses
// the workspace default). It reuses the workspace's own affected-set computation, so
// the closure it reports is exactly the set `magus affected <target>` would run.
func Compute(ctx context.Context, ws types.WorkspaceRepository, base string) (*types.ImpactResult, error) {
	r, err := ws.Affected(ctx, base)
	if err != nil {
		return nil, err
	}
	return build(ctx, ws, r)
}

// ComputeFromPaths derives the impact report from an explicit changed-path set
// (repo-relative or absolute-within-workspace), bypassing the VCS. It is the seam a
// non-git caller (a watch loop, a console request carrying a diff) uses.
func ComputeFromPaths(ctx context.Context, ws types.WorkspaceRepository, paths []string) (*types.ImpactResult, error) {
	r, err := ws.AffectedFromPaths(ctx, paths)
	if err != nil {
		return nil, err
	}
	return build(ctx, ws, r)
}

// build turns a raw AffectedResult into the enriched, formatter-ready report. It is
// pure aside from the ListTargets read (customTargetsByProject): no I/O of its
// own, deterministic ordering.
func build(ctx context.Context, ws types.WorkspaceRepository, r *types.AffectedResult) (*types.ImpactResult, error) {
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

	res := &types.ImpactResult{
		Base:             r.Base,
		ChangedFileCount: len(changed),
		ChangedFiles:     changed,
		SeedProjects:     seeds,
	}

	for _, path := range r.Affected {
		ap := types.ImpactProject{
			Path:    path,
			Targets: projectTargets(ws, path, customByProject),
		}
		if _, ok := seedSet[path]; ok {
			ap.Seed = true
			ap.Files = slices.Clone(r.FilesBySeed[path])
			slices.Sort(ap.Files)
			ap.UndeclaredFiles = slices.Clone(r.UndeclaredBySeed[path])
			slices.Sort(ap.UndeclaredFiles)
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
// one call that attributes them to projects.
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
