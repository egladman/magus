package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	internalci "github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/cmdline/output"
	"github.com/egladman/magus/libs/figure"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// deletionCandidates is the unreferenced symbols in row's write paths, or nil when the
// symbol index is not there to ask: no index is not the same as nothing to delete, and the
// fork must not fail for it.
func deletionCandidates(ctx context.Context, load func(context.Context) *knowledge.Graph, row types.Job) []types.UnreferencedEntry {
	if row.ReadOnly || len(row.WritePaths) == 0 {
		return nil
	}
	g := load(ctx)
	if g == nil {
		return nil
	}
	return job.UnreferencedInPaths(row, g.Unreferenced())
}

// referencedOutside is the files outside row's write paths that use what they define, and
// the gap that makes the list unknown. With no graph, or one holding no symbols, Referrers
// answers nil for every file, which reads the same as nothing outside using them.
func referencedOutside(ctx context.Context, load func(context.Context) *knowledge.Graph, root string, row types.Job, projects []*types.Project) (files []job.ReferencedFile, gap string) {
	if row.ReadOnly || len(row.WritePaths) == 0 {
		return nil, ""
	}
	g := load(ctx)
	if g == nil || !g.HasSymbols() {
		return nil, job.NoSymbolIndex()
	}
	return job.ReferencedOutside(root, row, projects, g.Referrers), ""
}

// leaseAffinityCommits is how far back the co-change scan reads. Wide enough that a
// coupling has to be a habit rather than one refactor, narrow enough that a boundary
// two owners ago does not warn about a partition nobody runs any more.
const leaseAffinityCommits = 200

// leaseBoundary derives what the WORKSPACE says about one lease: the projects its write
// paths reach, the paths its own declarations put out of reach, and the projects that
// change alongside the leased ones.
//
// This is the half a hand-written ledger row cannot carry. An orchestrator writes down
// the deny paths it REMEMBERED; the generated outputs of every project a lease
// invalidates, and the paths a sibling lease is holding right now, hold whether anybody
// remembered them or not.
//
// A read-only lease has no write set, so it gets none of this: the skill puts such a row
// outside the collision analysis entirely.
//
// A workspace that will not load DEGRADES rather than failing, the way the graph evidence
// already does: the row alone carries the criteria, the boundary and the check, and a worker
// in a tree whose magusfile is mid-edit is exactly who needs to read them.
func leaseBoundary(ctx context.Context, root string, store *job.Store, row types.Job, leases []types.Job, graph *jobGraph) job.TermsFacts {
	if len(row.WritePaths) == 0 {
		return job.TermsFacts{}
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return job.TermsFacts{WorkspaceCold: true}
	}
	affected, err := m.AffectedFromPaths(ctx, row.WritePaths)
	if err != nil {
		return job.TermsFacts{WorkspaceCold: true}
	}
	derived := generatedBoundary(m, affected.Affected, row.WritePaths)
	here := job.WorkingCheckout(cmp.Or(row.CheckoutRoot, m.Root()), row)
	derived = append(derived, leasedBoundary(row, leases, here, func() ([]*types.Project, string) { return m.All(), "" })...)
	derived = append(derived, sharedBoundary(m, affected.Seed)...)
	notes := collectNotes(ctx, m, row)
	facts := job.TermsFacts{
		Projects:           affected.Affected,
		DerivedDenyPaths:   derived,
		RegeneratedOutside: notes.RegeneratedOutside,
		UnrunGates:         notes.UnrunGates,
		Affinity:           leaseAffinity(ctx, m, affected.Seed),
		Stage:              workspaceStage(ctx, m),
		DeletionCandidates: deletionCandidates(ctx, graph.loadScoped(row.WritePaths), row),
	}
	facts.ReferencedOutside, facts.ReferencedOutsideGap = referencedOutside(ctx, graph.loadScoped(row.WritePaths), m.Root(), row, m.All())
	// A history file that does not read leaves the section out, the way Affinity's scan does.
	if grouping, err := store.GroupingLens(ctx, affected.Seed); err == nil {
		facts.Grouping = &grouping
	}
	return facts
}

// generatedBoundary is the declared output globs, across every project the lease
// invalidates, that land INSIDE its write paths.
//
// The intersection is what makes this a boundary rather than an inventory. This
// workspace declares around a hundred output globs; listing them all buries the two or
// three a given worker could actually hand-edit, and the worker is already fenced out of
// everything beyond its write paths. What it cannot know without being told is that a
// file it legitimately owns the directory of is generated.
//
// The AFFECTED set rather than the seeds, because a project writes outputs into trees it
// does not own: a glob from a downstream project can land in this lease's paths.
func generatedBoundary(m *magus.Magus, projects, owned []string) []job.TermsBoundary {
	var out []job.TermsBoundary
	seen := map[string]bool{}
	for _, path := range projects {
		p := m.Get(path)
		if p == nil {
			continue
		}
		for _, glob := range p.AllOutputs() {
			rooted := glob.Root(p.Path)
			if seen[rooted.String()] || !slices.ContainsFunc(owned, func(o string) bool { return types.PathsIntersect(o, rooted.Pattern) }) {
				continue
			}
			seen[rooted.String()] = true
			reason := fmt.Sprintf("generated by %s: regenerate it, never hand-edit", types.ProjectLabel(p.Path, p.Name))
			if len(rooted.Except) > 0 {
				// The carved-out files are hand-maintained, and a worker must not read
				// the pattern as a fence around them.
				reason = fmt.Sprintf("generated by %s, except %s: regenerate it, never hand-edit",
					types.ProjectLabel(p.Path, p.Name), strings.Join(rooted.Except, ", "))
			}
			out = append(out, job.TermsBoundary{Path: rooted.Pattern, Reason: reason})
		}
	}
	return out
}

// leasedBoundary is every path another [job.Editing] lease owns ([types.Job.HardPaths]). A
// terminal or exited row claims nothing: that is the same rule the overlap report follows,
// and the reason a worker releasing a path early lets a waiter start against it.
//
// An ANCESTOR claims nothing against its descendant either: a child is forked inside its
// parent's boundary, so listing the parent's paths would put the child's own out of reach.
//
// Nor does a lease in another checkout claim a generated output ([job.HeldApart]): row's
// holder, working in here, regenerates its own copy.
func leasedBoundary(row types.Job, leases []types.Job, here string, projects job.Projects) []job.TermsBoundary {
	ancestors := types.JobAncestors(leases, row.ID)
	var out []job.TermsBoundary
	for _, other := range leases {
		if _, blocked := types.JobBlockedOn(leases, other); other.ID == row.ID || !job.Editing(other) || blocked ||
			slices.ContainsFunc(ancestors, func(a types.Job) bool { return a.ID == other.ID }) {
			continue
		}
		for _, p := range other.HardPaths() {
			if apart, _ := job.HeldApart(here, other, projects, p); apart {
				continue
			}
			out = append(out, job.TermsBoundary{
				Path:   p,
				Reason: fmt.Sprintf("owned by live lease %s: coordinate, never work around", other.ID),
			})
		}
	}
	return out
}

// sharedBoundary is the files in the workspace root and the lease's own project
// directories that decide what every tool DOES: dependency locks, rule sets, toolchain
// pins, the workspace config, and the files magus maintains itself.
//
// One owner each, which is the skill's collision rule. They are named from what is on
// disk rather than from a list of every manifest a monorepo could hold, so the boundary
// describes this workspace instead of a catalogue.
func sharedBoundary(m *magus.Magus, seeds []string) []job.TermsBoundary {
	var out []job.TermsBoundary
	seen := map[string]bool{}
	for _, dir := range append([]string{"."}, seeds...) {
		entries, err := os.ReadDir(filepath.Join(m.Root(), filepath.FromSlash(dir)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			rel := path.Join(dir, e.Name())
			if e.IsDir() || seen[rel] {
				continue
			}
			reason := sharedReason(rel, e.Name())
			if reason == "" {
				continue
			}
			seen[rel] = true
			out = append(out, job.TermsBoundary{Path: rel, Reason: reason})
		}
	}
	return out
}

// sharedReason says why a file has one owner, or "" for one that does not.
func sharedReason(rel, name string) string {
	switch {
	case name == config.Filename:
		return "workspace configuration: changing it changes the plan every lease is running"
	case types.IsMagusMaintained(rel):
		return "magus maintains this file itself"
	case types.LooksLikeBuildInput(rel):
		return "shared build input: it decides what the tools do, so it has one owner"
	default:
		return ""
	}
}

// leaseAffinity reports the undeclared couplings between a leased project and one outside
// the lease, loudest first.
//
// A pair INSIDE the lease is dropped: two projects one worker owns move together by
// assignment, and saying so is not evidence about anything. A pair that declares its
// dependency is dropped for the reason job.TermsAffinity documents.
//
// Best-effort. A repository with no readable history yields nothing, and a partition
// decided without this evidence is the ordinary case rather than a failure.
func leaseAffinity(ctx context.Context, m *magus.Magus, seeds []string) []job.TermsAffinity {
	if len(seeds) == 0 {
		return nil
	}
	out, err := m.Affinity(ctx, types.InsightOptions{Commits: leaseAffinityCommits})
	if err != nil {
		return nil
	}
	var pairs []job.TermsAffinity
	for _, pair := range out.Pairs {
		if !pair.Hidden {
			continue
		}
		mine, theirs := pair.A, pair.B
		if !slices.Contains(seeds, mine) {
			mine, theirs = pair.B, pair.A
		}
		if !slices.Contains(seeds, mine) || slices.Contains(seeds, theirs) {
			continue
		}
		pairs = append(pairs, job.TermsAffinity{Project: mine, With: theirs, Commits: pair.Count})
	}
	return pairs
}

// briefRefusesTheGate reports why this row must not be briefed, or nil to render it.
//
// The ONE verdict this command makes, and it is here rather than in job.Terms because
// the brief renders context and never a verdict. The gate runs once, in the orchestrator's
// tree, after every unit lands; seven workers ran the whole pipeline concurrently on one
// machine (2026-09-09) because every hand-typed brief ended with it. The guard already
// denies the gate to a worker that binds a narrower row, and this closes the half the
// guard cannot reach: a row whose validation IS the gate, which the guard reads as a lease
// that legitimately owns it.
//
// A COMPOSITE reaching the gate is refused too. Naming the pipeline indirectly buys the
// same seven concurrent runs, and it is the likelier mistake once this refuses the obvious
// spelling.
func jobRefusesTheGate(ctx context.Context, root string, row types.Job) error {
	// The holder of a row forked with --checkout gates in that checkout, as the guard lets it.
	if row.State.Live() && row.Worktree.Standing() {
		return nil
	}
	fix := fmt.Sprintf(" The gate runs ONCE, in the forking session's tree, after every job lands."+
		" Give this job the narrowest target covering its paths (`%s` decomposes what the gate chains) with `%s`, then ask for the terms again.",
		hint.DescribeTarget.With(types.TargetCI+" <project>"), hint.JobFork)

	if source, owns := guard.LeaseGateSource(row); owns {
		return fmt.Errorf("magus describe job: job %s is assigned %s, which names the `%s` gate.%s", row.ID, source, types.TargetCI, fix)
	}
	if chain := validationReachesGate(ctx, root, row.Validation); len(chain) > 0 {
		return fmt.Errorf("magus describe job: job %s is assigned %q, and that target reaches the `%s` gate through %s.%s",
			row.ID, row.Validation, types.TargetCI, strings.Join(chain, " -> "), fix)
	}
	return nil
}

// validationReachesGate walks the ctx.needs graph from each target a validation field
// names and returns the first chain that arrives at the gate, or nil.
//
// WITHIN one project's graph. A cross-project edge is a ref rather than a bare node name,
// so the walk stops at one instead of guessing which project it meant; that under-reports
// and never over-reports, which is the direction a refusal has to fail in. A workspace
// that cannot be inspected also reports nothing: this rule must not be the reason a brief
// is unavailable.
func validationReachesGate(ctx context.Context, root, validation string) []string {
	if strings.TrimSpace(validation) == "" {
		return nil
	}
	// The boundary loads the full workspace next; an inspection first would load it twice.
	ws, err := loadMagus(ctx, root)
	if err != nil {
		return nil
	}
	graph, err := ws.TargetGraph(ctx)
	if err != nil {
		return nil
	}
	for _, word := range strings.Fields(validation) {
		t, err := types.ParseTarget(word)
		if err != nil {
			continue
		}
		for _, p := range graph.Projects {
			deps := map[string][]string{}
			for _, n := range p.Nodes {
				deps[n.Name] = n.Dependencies
			}
			if chain := chainToGate(t.Name, deps); len(chain) > 0 {
				return chain
			}
		}
	}
	return nil
}

// chainToGate is the path from start to the gate through a project's target dependencies,
// or nil when there is none. Depth-first with a visited set, so a cyclic graph terminates
// rather than being trusted to be acyclic: `magus describe graph` reports cycles instead
// of rejecting them, so one can reach here.
func chainToGate(start string, deps map[string][]string) []string {
	seen := map[string]bool{}
	var walk func(name string) []string
	walk = func(name string) []string {
		if seen[name] {
			return nil
		}
		seen[name] = true
		if name == types.TargetCI {
			return []string{name}
		}
		for _, next := range deps[name] {
			if chain := walk(next); len(chain) > 0 {
				return append([]string{name}, chain...)
			}
		}
		return nil
	}
	// A start nobody declares is not a chain of length one: every bare word in the
	// validation field reaches here, project paths and flags included.
	if _, ok := deps[start]; !ok {
		return nil
	}
	return walk(start)
}

// leaseGraphEvidence resolves each write path against the knowledge graph, one line per
// path the graph knows. cold reports a graph that would not load at all.
//
// A path the graph cannot resolve is skipped SILENTLY, because most write paths are
// ordinary source directories the containment tree does not carry, and a "no node" line
// per path would bury the ones that do resolve. A cold graph is different and is
// reported once: "not asked" must not read as "nothing depends on this". It takes the domain
// graph through graph, so a graph that must not build answers cold while a stale symbol
// index may hold changes inside paths.
func leaseGraphEvidence(ctx context.Context, graph *jobGraph, paths []string) (evidence []job.TermsEvidence, cold bool) {
	if len(paths) == 0 {
		return nil, false
	}
	g := graph.loadDomain(ctx, paths)
	if g == nil {
		return nil, true
	}
	for _, p := range paths {
		if e, ok := pathEvidence(g, p); ok {
			evidence = append(evidence, e)
		}
	}
	return evidence, false
}

// pathEvidence answers for one declared path, trying the node ids a path can carry in the
// containment tree.
//
// It accepts only a node whose id IS the ref it asked for. Explain resolves a bare name
// fuzzily, which is right for a person typing `magus explain build` and wrong here: asked
// for "cmd/magus" it answered target:.:release-sign, and a brief that hands a worker the
// blast radius of an unrelated node is worse than one that stays quiet.
func pathEvidence(g *knowledge.Graph, declared string) (job.TermsEvidence, bool) {
	for _, ref := range []string{fmt.Sprintf("%s:%s", types.KindDir, declared), fmt.Sprintf("%s:%s", types.KindFile, declared), declared} {
		out, ok := g.Explain(ref)
		if !ok || out.Node.ID != ref {
			continue
		}
		return job.TermsEvidence{Path: declared, Node: out.Node.ID, BlastRadius: out.BlastRadius}, true
	}
	return job.TermsEvidence{}, false
}

// jobSymbolReader loads the knowledge graph ONCE and answers every symbol a job's gates
// name from it.
//
// Lazy and memoized because a verification reads several names and the graph is the
// expensive part; loading per name would reopen it once per symbol. A load failure is
// remembered too, so a verification does not retry a graph that is not there once per
// gate and report the same failure five times. Without rebuild, a stale symbol index
// answers no name at all: this reader has no write paths to clear it against (see jobGraph).
func jobSymbolReader(root string, rebuild bool) job.SymbolReader {
	return graphSymbolReader(newJobGraph(root, rebuild).load)
}

func graphSymbolReader(load func(context.Context) *knowledge.Graph) job.SymbolReader {
	return func(ctx context.Context, name, signature string) (job.SymbolFact, bool) {
		g := load(ctx)
		if g == nil {
			return job.SymbolFact{}, false
		}
		return job.GraphSymbols(g)(ctx, name, signature)
	}
}

// jobGraph is one command's knowledge graph, loaded on first use and handed to every later
// caller, or the same nil when the load failed.
//
// A read that must not build (rebuild false, which is `describe job`) asks first whether a
// stale symbol index could hold a change to the row's write paths. While one could, it
// loads nothing: bringing the graph up to date is a build of seconds, and the symbol goals
// graded against it would answer from an index that misses that change. They print as not
// graded, beside the stale-index line that names the rebuild. An index whose changed files
// all fall outside the write paths does not stop the read: the files the row writes are as
// the index saw them. An index that names no changed files, or only some of them, stops it
// as before. With nothing stopping it, it reads the graph as built, so it and
// `job wait` grade alike. A deleted source names no file, so a deletion inside the write
// paths does not stop the read.
//
// One jobGraph serves one row: the first read that names write paths scopes every later one.
type jobGraph struct {
	rebuild bool
	// workspace opens the workspace this graph reads.
	workspace func(context.Context) (*magus.Magus, error)
	stale     func(context.Context) []types.KnowledgeStaleIndex
	build     func(context.Context) (*knowledge.Graph, error)
	// domain loads the graph without its symbol shards, the graph whose blast radius
	// `magus explain` prints.
	domain func(context.Context) (*knowledge.Graph, error)

	staleOnce   sync.Once
	staleIdx    []types.KnowledgeStaleIndex
	scopeOnce   sync.Once
	scope       []string
	graph       lazyGraph
	domainGraph lazyGraph
}

// lazyGraph is one graph loaded on the first get and handed to every later one, or the
// same nil when skip declined the load or it failed, so a failure is not retried per ask.
type lazyGraph struct {
	once sync.Once
	g    *knowledge.Graph
}

func (l *lazyGraph) get(ctx context.Context, skip func(context.Context) bool, build func(context.Context) (*knowledge.Graph, error)) *knowledge.Graph {
	l.once.Do(func() {
		if skip(ctx) {
			return
		}
		if loaded, err := build(ctx); err == nil {
			l.g = loaded
		}
	})
	return l.g
}

func newJobGraph(root string, rebuild bool) *jobGraph {
	return &jobGraph{
		rebuild: rebuild,
		workspace: func(ctx context.Context) (*magus.Magus, error) {
			return loadMagus(ctx, root)
		},
		stale: func(ctx context.Context) []types.KnowledgeStaleIndex {
			// The opened workspace judges afresh and names the changed files, which a
			// verdict the store recorded does not carry. Describe opens it for the
			// boundary anyway.
			m, err := loadMagus(ctx, root)
			if err != nil {
				return staleIndexes(ctx, root)
			}
			return findStaleIndexes(ctx, m)
		},
		build: func(ctx context.Context) (*knowledge.Graph, error) {
			return loadKnowledgeGraphForRefs(ctx, root, false, "")
		},
		domain: func(ctx context.Context) (*knowledge.Graph, error) {
			return loadKnowledgeGraph(ctx, root, false, false, false)
		},
	}
}

// newTreeJobGraph is newJobGraph for a checkout the process did not open at startup. It
// opens tree's own workspace, once, beside the process-wide singleton rather than through
// it, which names a single workspace and panics on a second. The settings are the process's:
// a checkout reads the config the invoking tree loaded.
func newTreeJobGraph(tree string, rebuild bool) *jobGraph {
	var (
		once sync.Once
		m    *magus.Magus
		err  error
	)
	open := func(ctx context.Context) (*magus.Magus, error) {
		once.Do(func() {
			var root string
			if root, err = workspace.FindRoot(tree); err != nil {
				return
			}
			opts := append(cliOptions(), workspace.WithLoadedConfig(globalCfg), magus.WithVersion(version))
			m, err = magus.Open(ctx, root, opts...)
		})
		return m, err
	}
	return &jobGraph{
		rebuild:   rebuild,
		workspace: open,
		stale: func(ctx context.Context) []types.KnowledgeStaleIndex {
			m, err := open(ctx)
			if err != nil {
				return nil
			}
			return findStaleIndexes(ctx, m)
		},
		build: func(ctx context.Context) (*knowledge.Graph, error) {
			m, err := open(ctx)
			if err != nil {
				return nil, err
			}
			return loadRefsGraph(ctx, m, globalCfg, false, "")
		},
		domain: func(ctx context.Context) (*knowledge.Graph, error) {
			m, err := open(ctx)
			if err != nil {
				return nil, err
			}
			return loadWorkspaceGraph(ctx, m, globalCfg, false, false, false)
		},
	}
}

func (r *jobGraph) load(ctx context.Context) *knowledge.Graph {
	return r.graph.get(ctx, r.mustNotRead, r.build)
}

// loadScoped is load for a row writing paths.
func (r *jobGraph) loadScoped(paths []string) func(context.Context) *knowledge.Graph {
	return func(ctx context.Context) *knowledge.Graph {
		r.setScope(paths)
		return r.load(ctx)
	}
}

// loadDomain is loadScoped for the domain graph, under the same rule.
func (r *jobGraph) loadDomain(ctx context.Context, paths []string) *knowledge.Graph {
	r.setScope(paths)
	return r.domainGraph.get(ctx, r.mustNotRead, r.domain)
}

func (r *jobGraph) setScope(paths []string) {
	r.scopeOnce.Do(func() { r.scope = paths })
}

// mustNotRead reports whether a read that must not build faces a stale symbol index that
// could hold a change inside the write paths.
func (r *jobGraph) mustNotRead(ctx context.Context) bool {
	return !r.rebuild && len(r.staleIndexes(ctx)) > 0
}

// staleIndexes is the stale symbol indexes that keep a read from the graph, judged once
// against the write paths the first scoped read named; with none named, every stale index.
func (r *jobGraph) staleIndexes(ctx context.Context) []types.KnowledgeStaleIndex {
	r.staleOnce.Do(func() {
		r.setScope(nil)
		for _, s := range r.stale(ctx) {
			if staleIndexHits(s, r.scope) {
				r.staleIdx = append(r.staleIdx, s)
			}
		}
	})
	return r.staleIdx
}

// staleIndexHits reports whether s could hold a change inside paths: it names no changed
// file or only some, paths is empty, or a file it names falls inside one of them.
func staleIndexHits(s types.KnowledgeStaleIndex, paths []string) bool {
	if len(paths) == 0 || s.ChangedCount == 0 || len(s.Changed) < s.ChangedCount {
		return true
	}
	return slices.ContainsFunc(s.Changed, func(file string) bool {
		return slices.ContainsFunc(paths, func(p string) bool { return types.PathsIntersect(p, file) })
	})
}

// extendedFootprint is rec with the tests beside what it writes added to its write paths and
// the outputs regenerated from them permitted as the integrator's, with a note for each and a
// count line. A bound caller, whose fork may not widen its boundary, and a workspace that
// did not load are skipped.
func extendedFootprint(ctx context.Context, store *job.Store, graph *jobGraph, root string, rec types.Declaration, ws job.Workspace) (types.Declaration, []string) {
	if store.Actor().Bound() || ws.Unavailable != "" {
		return rec, nil
	}
	var declared types.Job
	rec.Apply(&declared)
	var referrers func(file, declaration string) map[string]int
	if g := graph.load(ctx); g != nil && g.HasSymbols() {
		referrers = g.Referrers
	}
	paths, notes := job.ExtendFootprint(root, declared, ws.Projects, referrers)
	rec.WritePaths = append(rec.WritePaths, paths...)
	rec.SoftPaths = append(rec.SoftPaths, paths...)
	declared.WritePaths = append(declared.WritePaths, paths...)
	deviations := job.IntegratorDeviations(root, declared, ws.Projects, ws.Ignored)
	rec.Deviations = append(rec.Deviations, deviations...)
	if len(deviations) > 0 {
		notes = append(notes, fmt.Sprintf("%d regenerated output(s) outside the write paths are permitted: the integrator owns them", len(deviations)))
	}
	return rec, notes
}

// graphsByTree answers a graph per checkout, root's being rootGraph and any other built on
// first ask under the same rebuild rule.
func graphsByTree(root string, rootGraph *jobGraph, rebuild bool) func(tree string) *jobGraph {
	var (
		mu     sync.Mutex
		others = map[string]*jobGraph{}
	)
	return func(tree string) *jobGraph {
		tree = filepath.Clean(tree)
		if tree == filepath.Clean(root) {
			return rootGraph
		}
		mu.Lock()
		defer mu.Unlock()
		if _, ok := others[tree]; !ok {
			others[tree] = newTreeJobGraph(tree, rebuild)
		}
		return others[tree]
	}
}

// runsAtCurrentKey answers whether a recorded run is current in tree: the cache key its
// check would mint there now is the key the run was stored under. Any lookup that fails
// answers no, so the clock judges the run as it would without this.
func runsAtCurrentKey(graphIn func(tree string) *jobGraph) job.CurrentRun {
	return func(ctx context.Context, tree string, run types.JobAttempt) bool {
		if run.Ref == "" || run.Failed {
			return false
		}
		dir, err := magus.ResolveCacheDir(tree, workspace.WithLoadedConfig(globalCfg))
		if err != nil {
			return false
		}
		desc, err := cache.NewOutputStore(dir).DescriptorByRef(run.Ref)
		if err != nil || desc.Key == "" {
			return false
		}
		graph := graphIn(tree)
		if graph == nil || graph.workspace == nil {
			return false
		}
		m, err := graph.workspace(ctx)
		if err != nil {
			return false
		}
		target, charms, _ := strings.Cut(desc.Target, ":")
		var names []string
		if charms != "" {
			names = strings.Split(charms, ",")
		}
		key, _, err := m.ComputeTargetKey(ctx, desc.Project, target, names)
		return err == nil && key == desc.Key
	}
}

// jobObserver is the checkpoint observer plus the symbol delta of the job's diff and, for a
// job with a check gate on ci, the newest green ci gate on this checkout's branch and the
// tier of the change since it. One graph load serves the symbol goals and the delta, and it
// is the graph of the checkout the job's work lives in: a worker's unintegrated branch is
// invisible to the waiter's.
func jobObserver(root string, graphIn func(tree string) *jobGraph) job.Observer {
	return func(ctx context.Context, row types.Job) (job.Observed, error) {
		tree := job.WorkTree(root, row)
		graph := graphIn(tree)
		seen, err := job.CheckpointObserver(root, graphSymbolReader(graph.loadScoped(row.WritePaths)))(ctx, row)
		if err != nil {
			return seen, err
		}
		seen.CurrentRun = runsAtCurrentKey(graphIn)
		if delta, ok := observeSymbolDelta(ctx, tree, row, seen, graph); ok {
			seen.AddedExports, seen.LeftUnreferenced, seen.SymbolDeltaKnown = delta.Added, delta.Left, true
		}
		if root == "" || !gatesOnCI(row) {
			return seen, nil
		}
		seen.GreenGate = latestGreenGate(ctx, root)
		return seen, nil
	}
}

// observeSymbolDelta reads the delta of the diff seen was graded on, in tree, the checkout
// that diff was read in, against graph, which is that tree's. A graph that will not load
// leaves the delta unknown, as does a stale index when graph must not build.
func observeSymbolDelta(ctx context.Context, tree string, row types.Job, seen job.Observed, graph *jobGraph) (symbolDelta, bool) {
	if row.ReadOnly || tree == "" || !seen.ChangedKnown || seen.ChangedRevision == "" {
		return symbolDelta{}, false
	}
	if len(seen.Changed) == 0 {
		return symbolDelta{}, true
	}
	g := graph.loadScoped(row.WritePaths)(ctx)
	res, err := vcs.Resolve(ctx, tree, "", types.VCSOptions{})
	if err != nil || res.Source == types.VCSSourceDisabled {
		return symbolDelta{}, false
	}
	return readSymbolDelta(ctx, tree, g, res.VCS, seen.ChangedRevision, seen.Changed)
}

func gatesOnCI(row types.Job) bool {
	return slices.ContainsFunc(row.EffectiveGoals(), func(g types.Goal) bool {
		return g.Kind == types.GoalKindCheck && g.Check.Target == types.TargetCI
	})
}

// latestGreenGate is the newest green ci gate on root's branch and the tier of the change
// since it. It is zero when there is none, a merge lies between it and HEAD, or the
// change cannot be assessed, the same conditions the redundancy check declines on.
func latestGreenGate(ctx context.Context, root string) job.GreenGate {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return job.GreenGate{}
	}
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil || res.VCS == nil || res.Source == types.VCSSourceDisabled {
		return job.GreenGate{}
	}
	meta, err := res.VCS.Metadata(ctx, m.Root())
	if err != nil || meta.Ref == "" {
		return job.GreenGate{}
	}
	dir, err := sessions.Dir(m.Root())
	if err != nil {
		return job.GreenGate{}
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return job.GreenGate{}
	}
	rec, ok := sessions.LatestGate(fold, meta.Ref, types.TargetCI)
	if !ok || rec.Outcome != sessions.OutcomePass || rec.Commit == "" {
		return job.GreenGate{}
	}
	if meta.ID != rec.Commit {
		history, err := res.VCS.History(ctx, m.Root(), types.HistoryQuery{Limit: gateMergeScanLimit})
		if err != nil || !internalci.MergeFreeRange(history, rec.Commit) {
			return job.GreenGate{}
		}
	}
	rep, err := m.AssessChange(ctx, types.TargetCI, magus.AssessOptions{Base: rec.Commit})
	if err != nil {
		return job.GreenGate{}
	}
	return job.GreenGate{Commit: rec.Commit, Projects: rec.Projects, Tier: rep.Tier}
}

// gradesSymbols reports whether grading id reads the symbol graph: a symbol gate of its
// own, or one inherited from an ancestor.
func gradesSymbols(rows []types.Job, id string) bool {
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return false
	}
	for _, r := range append([]types.Job{rows[i]}, types.JobAncestors(rows, id)...) {
		if slices.ContainsFunc(r.Goals, func(g types.Goal) bool { return g.Resolve().Kind == types.GoalKindSymbol }) {
			return true
		}
	}
	return false
}

// jobGraphCommand is `magus job graph`: the live plan as figures that fit
// magus/figure's budgets. -o svg writes them and prints each path.
func jobGraphCommand(ctx context.Context, root string, args []string) error {
	var gf *flaggen.JobGraphFlags
	pos, err := commandParse("job graph", args, func(fs *flag.FlagSet) {
		gf = flaggen.BindJobGraph(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job graph [-o svg] [--dir <dir>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Draw the live jobs as a plan: one box per job, an edge per fork,")
			fmt.Fprintln(os.Stderr, "merge and declared dependency, each box tagged with its state and")
			fmt.Fprintln(os.Stderr, "naming its holder. A plan larger than one figure holds splits into an")
			fmt.Fprintln(os.Stderr, "overview, one box per group of jobs, and a figure per group.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Without -o it prints each figure's boxes and edges. -o svg writes")
			fmt.Fprintln(os.Stderr, "each figure to <id>.svg in --dir and prints one path per line,")
			fmt.Fprintln(os.Stderr, "the overview first. The same plan writes the same bytes.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job graph -o svg --dir /tmp/plan")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return exit.Usagef("magus job graph: takes no arguments (got %q)", pos[0])
	}
	opts, err := output.Parse(global.output, outputSVG)
	if err != nil {
		return exit.Usagef("magus job graph: %s", err)
	}
	if gf.Dir != "" && opts.Format != outputSVG {
		return exit.Usagef("magus job graph: --dir names where -o svg writes; add -o svg")
	}
	root = resolveRootOrEmpty(root)
	store, err := openJobs(ctx, root, jobOpen{})
	if err != nil {
		return err
	}
	list, err := store.Report(ctx, time.Now().Unix(), globalCfg.Jobs.StaleAfter)
	if err != nil {
		return err
	}
	list, _ = withoutEnded(list)
	plan := newJobPlan(list.Jobs)
	figs := plan.figures()
	switch opts.Format {
	case outputSVG:
		dir := gf.Dir
		if dir == "" {
			cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
			if err != nil {
				return err
			}
			dir = filepath.Join(cacheDir, "job-graph")
		}
		paths, err := writePlanSVGs(ctx, plan, figs, dir)
		if err != nil {
			return err
		}
		return emitNames(os.Stdout, paths)
	case outputName:
		ids := make([]string, len(figs))
		for i, f := range figs {
			ids[i] = f.ID
		}
		return emitNames(os.Stdout, ids)
	case outputText:
		plan.print(os.Stdout, figs)
		return nil
	default:
		return emitFormatted(opts, figs)
	}
}

// outputSVG is `magus job graph`'s own -o format.
const outputSVG output.Format = "svg"

// The budgets magus/figure holds one figure to.
const (
	planMaxNodes = 9
	planMaxEdges = 12
)

// planEdgeKind is what joins two jobs. Where several kinds join
// two boxes, the first in planEdgeKinds is drawn.
type planEdgeKind string

const (
	planForked planEdgeKind = "forked"
	planMerges planEdgeKind = "merges"
	planAfter  planEdgeKind = "after"
)

var planEdgeKinds = []planEdgeKind{planForked, planMerges, planAfter}

// planEdge runs from the forking, merging or depended-on job.
type planEdge struct {
	Src  string       `json:"src"`
	Dst  string       `json:"dst"`
	Kind planEdgeKind `json:"kind"`
}

// planNode is one box: a job, or a group a figure of its own draws.
type planNode struct {
	Name  string      `json:"name"`
	Job   string      `json:"job,omitempty"`
	Group *planFigure `json:"-"`
	Draws string      `json:"figure,omitempty"`
}

// planFigure is one figure of the plan. Jobs counts its groups' jobs.
type planFigure struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Jobs  []string   `json:"jobs"`
	Nodes []planNode `json:"nodes"`
	Edges []planEdge `json:"edges"`
}

// jobPlan is the job rows as a forest. A job hangs from its fork
// parent, else from the first integrator that merges it.
type jobPlan struct {
	byID     map[string]types.Job
	order    []string
	children map[string][]string
	roots    []string
	edges    []planEdge
}

func newJobPlan(rows []types.Job) *jobPlan {
	rows = slices.Clone(rows)
	slices.SortStableFunc(rows, func(a, b types.Job) int {
		return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID))
	})
	p := &jobPlan{byID: map[string]types.Job{}, children: map[string][]string{}}
	for _, r := range rows {
		if _, dup := p.byID[r.ID]; !dup {
			p.byID[r.ID] = r
			p.order = append(p.order, r.ID)
		}
	}
	seen := map[planEdge]bool{}
	add := func(e planEdge) {
		_, src := p.byID[e.Src]
		_, dst := p.byID[e.Dst]
		if src && dst && e.Src != e.Dst && !seen[e] {
			seen[e] = true
			p.edges = append(p.edges, e)
		}
	}
	for _, id := range p.order {
		r := p.byID[id]
		add(planEdge{Src: r.Parent, Dst: id, Kind: planForked})
		for _, merged := range r.Integrates {
			add(planEdge{Src: id, Dst: merged, Kind: planMerges})
		}
		for _, dep := range r.DependsOn {
			add(planEdge{Src: dep, Dst: id, Kind: planAfter})
		}
	}
	parent := map[string]string{}
	reaches := func(from, to string) bool {
		for at := from; at != ""; at = parent[at] {
			if at == to {
				return true
			}
		}
		return false
	}
	for _, kind := range []planEdgeKind{planForked, planMerges} {
		for _, e := range p.edges {
			if e.Kind == kind && parent[e.Dst] == "" && !reaches(e.Src, e.Dst) {
				parent[e.Dst] = e.Src
			}
		}
	}
	for _, id := range p.order {
		if up := parent[id]; up != "" {
			p.children[up] = append(p.children[up], id)
		} else {
			p.roots = append(p.roots, id)
		}
	}
	return p
}

func (p *jobPlan) subtree(id string) []string {
	out := []string{id}
	for _, c := range p.children[id] {
		out = append(out, p.subtree(c)...)
	}
	return out
}

// figures is the plan split to fit the budgets, the overview first.
// A group's figure follows the figure that draws it as a box.
func (p *jobPlan) figures() []*planFigure {
	title := fmt.Sprintf("Job plan: %d %s", len(p.order), plural(len(p.order), "job", "jobs"))
	var out []*planFigure
	taken := map[string]int{}
	var walk func(f *planFigure, id string)
	walk = func(f *planFigure, id string) {
		taken[id]++
		if n := taken[id]; n > 1 {
			id = fmt.Sprintf("%s-%d", id, n)
		}
		f.ID = id
		out = append(out, f)
		for _, n := range f.Nodes {
			if n.Group != nil {
				walk(n.Group, "plan-"+planSlug(n.Name))
			}
		}
	}
	walk(p.group(title, p.roots), "plan")
	for _, f := range out {
		for i, n := range f.Nodes {
			if n.Group != nil {
				f.Nodes[i].Draws = n.Group.ID
			}
		}
	}
	return out
}

// group draws the subtrees under tops: every job when they fit, else
// the top job and a box per child subtree, bundled by fit.
func (p *jobPlan) group(title string, tops []string) *planFigure {
	var jobs []string
	for _, t := range tops {
		jobs = append(jobs, p.subtree(t)...)
	}
	f := &planFigure{Title: title, Jobs: jobs}
	flat := make([]planNode, len(jobs))
	for i, id := range jobs {
		flat[i] = planNode{Name: id, Job: id}
	}
	if edges := p.between(flat); len(flat) <= planMaxNodes && len(edges) <= planMaxEdges {
		f.Nodes, f.Edges = flat, edges
		return f
	}
	var head []planNode
	rest := tops
	if len(tops) == 1 {
		head = []planNode{{Name: tops[0], Job: tops[0]}}
		rest = p.children[tops[0]]
	}
	parts := make([]planNode, len(rest))
	for i, t := range rest {
		parts[i] = p.box(t)
	}
	f.Nodes = p.fit(head, parts)
	f.Edges = p.between(f.Nodes)
	return f
}

// box is the subtree under id as one box.
func (p *jobPlan) box(id string) planNode {
	if len(p.children[id]) == 0 {
		return planNode{Name: id, Job: id}
	}
	n := len(p.subtree(id))
	return planNode{Name: id, Group: p.group(fmt.Sprintf("%s: %d jobs", id, n), []string{id})}
}

// fit is head then parts, folding the tail of parts into bundles when
// they overflow. It takes the fewest bundles that each fit whole, and
// more while the edges overflow. A bundle's figure fits it the same way.
func (p *jobPlan) fit(head, parts []planNode) []planNode {
	nodes := append(slices.Clone(head), parts...)
	if len(nodes) <= planMaxNodes && len(p.between(nodes)) <= planMaxEdges {
		return nodes
	}
	room := planMaxNodes - len(head)
	least := max(1, min(room, ceilDiv(len(parts)-room, planMaxNodes-1)))
	if len(head) == 0 {
		least = max(least, 2)
	}
	for bundles := least; bundles <= room; bundles++ {
		loose := room - bundles
		if loose >= len(parts) {
			continue
		}
		nodes = append(slices.Clone(head), parts[:loose]...)
		for chunk := range slices.Chunk(parts[loose:], ceilDiv(len(parts)-loose, bundles)) {
			nodes = append(nodes, p.bundled(chunk))
		}
		if len(p.between(nodes)) <= planMaxEdges {
			break
		}
	}
	return nodes
}

func ceilDiv(a, b int) int {
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func (p *jobPlan) bundled(parts []planNode) planNode {
	if len(parts) == 1 {
		return parts[0]
	}
	name := fmt.Sprintf("%s and %d more", parts[0].Name, len(parts)-1)
	f := &planFigure{Title: name, Nodes: p.fit(nil, parts)}
	f.Edges = p.between(f.Nodes)
	for _, n := range parts {
		f.Jobs = append(f.Jobs, n.jobs()...)
	}
	return planNode{Name: name, Group: f}
}

func (n planNode) jobs() []string {
	if n.Group != nil {
		return n.Group.Jobs
	}
	return []string{n.Job}
}

// between is the plan's edges among nodes, one per ordered pair. An
// edge with an end in no box belongs to a figure further up.
func (p *jobPlan) between(nodes []planNode) []planEdge {
	at := map[string]string{}
	for _, n := range nodes {
		for _, id := range n.jobs() {
			at[id] = n.Name
		}
	}
	type pair struct{ src, dst string }
	kinds := map[pair]planEdgeKind{}
	var order []pair
	for _, e := range p.edges {
		k := pair{at[e.Src], at[e.Dst]}
		if k.src == "" || k.dst == "" || k.src == k.dst {
			continue
		}
		have, ok := kinds[k]
		if !ok {
			order = append(order, k)
		}
		if !ok || slices.Index(planEdgeKinds, e.Kind) < slices.Index(planEdgeKinds, have) {
			kinds[k] = e.Kind
		}
	}
	out := make([]planEdge, len(order))
	for i, k := range order {
		out[i] = planEdge{Src: k.src, Dst: k.dst, Kind: kinds[k]}
	}
	return out
}

func planSlug(name string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '-'
	}, name)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return strings.Trim(slug, "-")
}

func planUnfinished(state types.JobState) bool {
	return state == types.StateDeclared || state == types.StateRunning
}

// figureRecord is f as a magus/figure record. A group's box links to
// the file writePlanSVGs writes its figure to.
func (p *jobPlan) figureRecord(f *planFigure) figure.Figure {
	plain, optional := figure.Look("plain"), figure.Look("optional")
	solid, dashed := figure.Stroke("plain"), figure.Stroke("optional")
	rec := figure.Figure{
		ID:          f.ID,
		Title:       f.Title,
		Eyebrow:     "job plan",
		Desc:        "A box is a job with its state and holder, or a group another figure draws. A solid edge is a fork or a merge, a dashed one a dependency.",
		Direction:   figure.Direction("down"),
		UnscopedWhy: "a plan's jobs are units of work, not directories",
	}
	actors := map[string]figure.Actor{}
	anyOpen := false
	for _, n := range f.Nodes {
		a := figure.Actor{Name: n.Name, Look: &plain}
		open := 0
		for _, id := range n.jobs() {
			if planUnfinished(p.byID[id].State) {
				open++
			}
		}
		if n.Group != nil {
			a.Sub = fmt.Sprintf("%d jobs, %d unfinished", len(n.Group.Jobs), open)
			a.Tag = "group"
			a.Link = n.Draws + ".svg"
		} else {
			a.Sub = jobHolderCell(p.byID[n.Job])
			a.Tag = orDash(string(p.byID[n.Job].State))
		}
		if open > 0 {
			a.Look = &optional
			anyOpen = true
		}
		actors[n.Name] = a
		rec.Boxes = append(rec.Boxes, figure.Box{Actor: &a})
	}
	for _, e := range f.Edges {
		src, dst := actors[e.Src], actors[e.Dst]
		fl := figure.Flow{Src: figure.End{Actor: &src}, Dst: figure.End{Actor: &dst}, Stroke: &solid}
		switch e.Kind {
		case planMerges:
			fl.Label = "merges"
		case planAfter:
			fl.Label, fl.Stroke = "after", &dashed
		}
		rec.Flows = append(rec.Flows, fl)
	}
	if anyOpen {
		rec.Legends = append(rec.Legends, figure.Legend{Look: optional, Label: "not finished"})
	}
	return rec
}

// drawPlanFigure paints rec in a session of its own, as Draw needs.
func drawPlanFigure(ctx context.Context, rec figure.Figure) (string, error) {
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer sess.Close()
	buzzstd.RegisterWithOutput(sess, io.Discard)
	bindings.RegisterMagusRecordTypes(sess)
	sess.SetModuleDecls("magus/figure", figure.Source)
	return figure.Draw(ctx, sess, rec, "")
}

// writePlanSVGs writes each figure to dir as <id>.svg. It draws every
// figure before writing any, so a refusal writes nothing.
func writePlanSVGs(ctx context.Context, p *jobPlan, figs []*planFigure, dir string) ([]string, error) {
	svgs := make([]string, len(figs))
	for i, f := range figs {
		svg, err := drawPlanFigure(ctx, p.figureRecord(f))
		if err != nil {
			return nil, fmt.Errorf("magus job graph: draw %s: %w", f.ID, err)
		}
		svgs[i] = svg
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("magus job graph: %w", err)
	}
	paths := make([]string, len(figs))
	for i, f := range figs {
		paths[i] = filepath.Join(dir, f.ID+".svg")
		if err := os.WriteFile(paths[i], []byte(svgs[i]), 0o644); err != nil {
			return nil, fmt.Errorf("magus job graph: %w", err)
		}
	}
	return paths, nil
}

func (p *jobPlan) print(out io.Writer, figs []*planFigure) {
	for i, f := range figs {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s: %s\n", f.ID, f.Title)
		for _, n := range f.Nodes {
			if n.Group != nil {
				fmt.Fprintf(out, "  %s: %d jobs in %s\n", n.Name, len(n.Group.Jobs), n.Draws)
				continue
			}
			row := p.byID[n.Job]
			fmt.Fprintf(out, "  %s: %s, %s\n", n.Name, orDash(string(row.State)), jobHolderCell(row))
		}
		for _, e := range f.Edges {
			fmt.Fprintf(out, "  %s -> %s (%s)\n", e.Src, e.Dst, e.Kind)
		}
	}
}
