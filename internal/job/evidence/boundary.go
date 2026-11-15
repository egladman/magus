package evidence

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// DeletionCandidates is the unreferenced symbols in row's write paths, or nil when the
// symbol index is not there to ask: no index is not the same as nothing to delete, and the
// fork must not fail for it.
func DeletionCandidates(ctx context.Context, load func(context.Context) *knowledge.Graph, row types.Job) []types.UnreferencedEntry {
	if row.ReadOnly || len(row.Paths.Write) == 0 {
		return nil
	}
	g := load(ctx)
	if g == nil {
		return nil
	}
	return job.UnreferencedInPaths(row, g.Unreferenced())
}

// ReferencedOutside is the files outside row's write paths that use what they define, and
// the gap that makes the list unknown. With no graph, or one holding no symbols, Referrers
// answers nil for every file, which reads the same as nothing outside using them.
func ReferencedOutside(ctx context.Context, load func(context.Context) *knowledge.Graph, root string, row types.Job, projects []*types.Project) (files []job.ReferencedFile, gap string) {
	if row.ReadOnly || len(row.Paths.Write) == 0 {
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

// LeaseBoundary derives what the WORKSPACE says about one lease: the projects its write
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
func LeaseBoundary(ctx context.Context, root string, store *job.Store, row types.Job, leases []types.Job, graph *Graph, env Env) job.TermsFacts {
	if len(row.Paths.Write) == 0 {
		return job.TermsFacts{}
	}
	m, err := env.LoadMagus(ctx, root)
	if err != nil {
		return job.TermsFacts{WorkspaceCold: true}
	}
	affected, err := m.AffectedFromPaths(ctx, row.Paths.Write)
	if err != nil {
		return job.TermsFacts{WorkspaceCold: true}
	}
	derived := generatedBoundary(m, affected.Affected, row.Paths.Write)
	here := job.WorkingCheckout(cmp.Or(row.CheckoutRoot, m.Root()), row)
	derived = append(derived, leasedBoundary(row, leases, here, func() ([]*types.Project, string) { return m.All(), "" })...)
	derived = append(derived, sharedBoundary(m, affected.Seed)...)
	regenerated, unrun := env.Notes(ctx, m, row)
	facts := job.TermsFacts{
		Projects:           affected.Affected,
		DerivedDenyPaths:   derived,
		RegeneratedOutside: regenerated,
		UnrunGates:         unrun,
		Affinity:           leaseAffinity(ctx, m, affected.Seed),
		Stage:              env.Stage(ctx, m),
		DeletionCandidates: DeletionCandidates(ctx, graph.LoadScoped(row.Paths.Write), row),
		TestScope:          job.NarrowTestScope(m.All(), row.Paths.Write, here),
	}
	facts.ReferencedOutside, facts.ReferencedOutsideGap = ReferencedOutside(ctx, graph.LoadScoped(row.Paths.Write), m.Root(), row, m.All())
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
	out, err := m.Affinity(ctx, types.HistoryMetricsOptions{Commits: leaseAffinityCommits})
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
