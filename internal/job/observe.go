package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// SymbolReader answers what the knowledge graph knows about a symbol. Supplied by the
// caller rather than reached for here, because internal/job grades evidence and does not
// own a graph; the CLI, the MCP tool and the Buzz binding each already hold one.
type SymbolReader func(ctx context.Context, name string) (SymbolFact, bool)

// CheckpointObserver answers what a job changed, what is in the tree, and what the graph
// says about the symbols its gates named.
//
// Three sources, one for each kind of gate that is not a recorded run: the VCS for the
// diff since the job's checkpoint, the filesystem for what is there now, and symbols
// through the reader the caller supplied. A nil reader leaves the symbol half UNKNOWN
// rather than empty, so a symbol gate refuses instead of passing on a question nobody
// asked.
//
// Every failure resolves the same way: not known. A row with no checkpoint, version
// control disabled, a revision the backend cannot find, all leave the diff unread, and a
// gate that needed it then refuses and says so. That direction is deliberate and is the
// opposite of the guard's fail-open rule: a guard that cannot ask must not refuse a
// person's own command, while a gate that cannot verify must not certify. Refusing costs
// a re-run; certifying on an unread diff is the attestation the gate replaced.
func CheckpointObserver(root string, symbols SymbolReader) Observer {
	return func(ctx context.Context, row types.Job) (Observed, error) {
		var driver types.VCSDriver
		unresolved := ""
		if checkpointRevision(row.Checkpoint) != "" {
			driver, unresolved = resolveDriver(ctx, root)
		}
		seen := changedSince(ctx, driver, unresolved, root, row.Checkpoint)
		seen.Present, seen.PresentKnown = presentIn(root, row)
		seen.Symbols, seen.SymbolsKnown = readSymbols(ctx, row, symbols)
		return seen, nil
	}
}

// resolveDriver is the VCS answering for root, or nil and why none is.
func resolveDriver(ctx context.Context, root string) (types.VCSDriver, string) {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	switch {
	case err != nil:
		return nil, fmt.Sprintf("no version control answered in %s: %v", root, err)
	case res.Source == types.VCSSourceDisabled:
		return nil, "version control is disabled here"
	case res.VCS == nil:
		return nil, fmt.Sprintf("no version control answered in %s", root)
	}
	return res.VCS, ""
}

// checkpointRevision is the revision half of a checkpoint token.
//
// The token is `<revision>` or `<revision>+<patch digest>`; only the revision half is
// something a VCS can diff against, so the digest is cut. That digest still matters to a
// reader deciding whether the worker saw the same tree, and it is on the row for exactly
// that; it is not a revision, and asking a backend to resolve one would fail.
func checkpointRevision(checkpoint string) string {
	revision, _, _ := strings.Cut(checkpoint, "+")
	return strings.TrimSpace(revision)
}

// changedSince asks driver what differs from the checkpoint's revision, and which
// declarations those lines land in. A nil driver is one that did not resolve, and
// unresolved says why.
//
// The regions are read only when the paths were, and only for them: a region outside the
// diff a gate grades would be a footprint of something else.
func changedSince(ctx context.Context, driver types.VCSDriver, unresolved, root, checkpoint string) Observed {
	revision := checkpointRevision(checkpoint)
	if revision == "" {
		return Observed{RegionsReason: "the job was declared without a checkpoint, so there is no revision to diff against"}
	}
	seen := Observed{ChangedFrom: revision}
	if driver == nil {
		seen.RegionsReason = unresolved
		return seen
	}
	changed, err := driver.ChangedFiles(ctx, root, revision)
	if err != nil {
		seen.RegionsReason = fmt.Sprintf("the diff since %s could not be read: %v", revision, err)
		return seen
	}
	seen.Changed, seen.ChangedKnown = changed, true
	if len(changed) == 0 {
		seen.RegionsKnown = true
		return seen
	}
	files := make([]types.FileChange, len(changed))
	for i, p := range changed {
		files[i] = types.FileChange{Path: p}
	}
	seen.Regions, seen.RegionsKnown, seen.RegionsReason = regionsSince(ctx, driver, root, revision, files)
	return seen
}

// regionsSince is Regions with its failure turned into the reason a reader is shown. A
// backend declining the capability is named as that, since it is the one failure nobody
// can fix by re-running.
func regionsSince(ctx context.Context, driver types.VCSDriver, root, revision string, files []types.FileChange) ([]types.RegionChange, bool, string) {
	regions, err := driver.Regions(ctx, root, revision, files)
	var declined *types.VCSUnsupportedError
	switch {
	case errors.As(err, &declined):
		return nil, false, fmt.Sprintf("%s does not report changed regions (%s)", declined.VCS, declined.Capability)
	case err != nil:
		return nil, false, fmt.Sprintf("the regions changed since %s could not be read: %v", revision, err)
	}
	return regions, true, ""
}

// OverlapFootprints fills each overlap's Footprint: whether the two jobs' diffs, each
// taken in the checkout bound to that job against its own checkpoint, touch a common
// declaration. It returns a copy and never fails; every question it cannot answer is a
// FootprintUnknown verdict naming why.
//
// A job's checkout is the one whose binding marker names it, found by listing root and
// every other checkout driver knows. cacheDirOf maps a checkout root to the cache dir its
// markers live in. A nil driver is version control that did not resolve.
//
// Both sides of each diff are compared, per [types.Collisions]: two jobs deleting lines
// from one declaration both touch it.
func OverlapFootprints(ctx context.Context, driver types.VCSDriver, root string, cacheDirOf func(string) (string, error), rows []types.Job, overlaps []types.JobOverlap) []types.JobOverlap {
	if len(overlaps) == 0 {
		return overlaps
	}
	bound, listed := boundCheckouts(driver, root, cacheDirOf)
	footprints := map[string]footprint{}
	footprintOf := func(id string) footprint {
		fp, ok := footprints[id]
		if !ok {
			fp = readFootprint(ctx, driver, rows, bound, listed, id)
			footprints[id] = fp
		}
		return fp
	}
	out := slices.Clone(overlaps)
	for i := range out {
		verdict := compareFootprints(out[i].JobA, footprintOf(out[i].JobA), out[i].JobB, footprintOf(out[i].JobB))
		out[i].Footprint = &verdict
	}
	return out
}

// footprint is one job's regions, or why they are not known.
type footprint struct {
	regions []types.RegionChange
	known   bool
	reason  string
}

// boundCheckouts maps each lease bound anywhere in the repository to its checkout root.
// A lease two checkouts both claim maps to "", which readFootprint reports rather than
// guessing: checkouts sharing one cache dir do exactly that, and picking the first would
// diff the wrong tree. listed is "" when the checkouts were listed, else why not.
func boundCheckouts(driver types.VCSDriver, root string, cacheDirOf func(string) (string, error)) (map[string]string, string) {
	if driver == nil {
		return nil, "no version control answered here"
	}
	others, err := driver.OtherCheckouts(root)
	if err != nil {
		return nil, fmt.Sprintf("the checkouts of this repository could not be listed: %v", err)
	}
	bound := map[string]string{}
	for _, dir := range append([]string{root}, others...) {
		cacheDir, err := cacheDirOf(dir)
		if err != nil {
			continue // its markers cannot be found, so it binds nobody this can see
		}
		for _, id := range BoundLeases(cacheDir) {
			if prior, ok := bound[id]; ok && prior != dir {
				bound[id] = ""
				continue
			}
			bound[id] = dir
		}
	}
	return bound, ""
}

func readFootprint(ctx context.Context, driver types.VCSDriver, rows []types.Job, bound map[string]string, listed, id string) footprint {
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return footprint{reason: fmt.Sprintf("no job %s is declared", id)}
	}
	revision := checkpointRevision(rows[i].Checkpoint)
	if revision == "" {
		return footprint{reason: fmt.Sprintf("%s was declared without a checkpoint", id)}
	}
	if listed != "" {
		return footprint{reason: listed}
	}
	dir, ok := bound[id]
	switch {
	case !ok:
		return footprint{reason: fmt.Sprintf("no checkout of this repository is bound to %s", id)}
	case dir == "":
		return footprint{reason: fmt.Sprintf("more than one checkout is bound to %s", id)}
	}
	seen := changedSince(ctx, driver, "", dir, revision)
	return footprint{regions: seen.Regions, known: seen.RegionsKnown, reason: seen.RegionsReason}
}

// compareFootprints collides two footprints' regions, both sides of each.
func compareFootprints(idA string, a footprint, idB string, b footprint) types.JobOverlapFootprint {
	var unknown []string
	for _, side := range []struct {
		id string
		fp footprint
	}{{idA, a}, {idB, b}} {
		if !side.fp.known {
			unknown = append(unknown, side.id+": "+side.fp.reason)
		}
	}
	if len(unknown) > 0 {
		return types.JobOverlapFootprint{Verdict: types.FootprintUnknown, Reason: strings.Join(unknown, "; ")}
	}
	collisions := types.Collisions(asLocators(a.regions), asLocators(b.regions))
	if len(collisions) == 0 {
		return types.JobOverlapFootprint{Verdict: types.FootprintDisjoint}
	}
	shared := make([]string, len(collisions))
	for i, l := range collisions {
		shared[i] = l.String()
	}
	return types.JobOverlapFootprint{Verdict: types.FootprintShared, Shared: shared}
}

func asLocators(regions []types.RegionChange) []types.Locator {
	out := make([]types.Locator, len(regions))
	for i, r := range regions {
		out[i] = r
	}
	return out
}

// presentIn reports which of the paths the row's gates NAME are in the tree.
//
// Only the named ones, never a walk: a gate asks about the paths it declared, and walking
// the tree to answer would read a repository to answer a question about six files.
//
// doublestar, which is the matcher covers(), the guard and the cache all use, and NOT
// filepath.Glob. Under filepath.Glob `**` is one segment, so `db/migrations/**` found
// nothing two levels down and an `absent` gate over it PASSED while the files were there:
// a false certification, which is the one direction this design may never fail in.
func presentIn(root string, row types.Job) ([]string, bool) {
	if root == "" {
		return nil, false
	}
	tree := os.DirFS(root)
	var out []string
	for _, gate := range row.EffectiveCompletionGates() {
		if gate.Kind != types.GateKindPaths {
			continue
		}
		for _, declared := range gate.Paths {
			if declared = strings.TrimSpace(declared); declared == "" {
				continue
			}
			matches, err := doublestar.Glob(tree, declared)
			if err != nil {
				return nil, false
			}
			out = append(out, matches...)
		}
	}
	return out, true
}

// readSymbols resolves every symbol the row's gates named. One miss from the reader leaves
// the whole observation unknown: a graph that answered for three names and failed on the
// fourth cannot be told apart from one that found the fourth absent, and those mean
// opposite things to a gate.
func readSymbols(ctx context.Context, row types.Job, read SymbolReader) (map[string]SymbolFact, bool) {
	if read == nil {
		return nil, false
	}
	out := map[string]SymbolFact{}
	for _, gate := range row.EffectiveCompletionGates() {
		if gate.Kind != types.GateKindSymbol {
			continue
		}
		for _, name := range gate.Symbols {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			fact, ok := read(ctx, name)
			if !ok {
				return nil, false
			}
			out[name] = fact
		}
	}
	return out, true
}

// SymbolGraph is the slice of the knowledge graph a symbol gate needs.
//
// Declared HERE, as the narrowest interface that answers the question, rather than
// importing the graph package: internal/job grades evidence and has no business knowing
// how a graph is built, and every caller already holds something that satisfies this.
type SymbolGraph interface {
	Refs(ref string) (types.KnowledgeRefsOutput, bool)
	// Resolve is how a bare name is checked for other symbols carrying it, since Refs
	// answers for the top-ranked one alone.
	Resolve(input string, limit int) []types.KnowledgeMatch
}

// GraphSymbols reads symbol facts off a loaded graph. A nil graph reads as UNREADABLE
// rather than as a graph with nothing in it, which is the distinction a symbol gate turns
// on: absent and unknown mean opposite things to a verdict.
func GraphSymbols(g SymbolGraph) SymbolReader {
	return func(_ context.Context, name string) (SymbolFact, bool) {
		if g == nil {
			return SymbolFact{}, false
		}
		out, ok := g.Refs(name)
		if !ok {
			// The graph was readable and holds no such symbol. A real answer, so the
			// reader SUCCEEDS with Defined false; reporting failure here would make a
			// removal that landed indistinguishable from a graph nobody could open.
			return SymbolFact{}, true
		}
		definedIn := make([]string, 0, len(out.Defs))
		for _, d := range out.Defs {
			definedIn = append(definedIn, d.File)
		}
		fact := SymbolFact{DefinedIn: definedIn, ReferenceCount: out.RefCount}
		if out.Symbol != name {
			fact.SameNameDefinitions = sameNameDefinitions(g, name)
		}
		return fact, true
	}
}

// sameNameDefinitions is one defining file per symbol whose label IS name, or nil when
// fewer than two are.
func sameNameDefinitions(g SymbolGraph, name string) []string {
	var files []string
	for _, m := range g.Resolve(name, 0) {
		if m.Kind != types.KindSymbol || m.Label != name {
			continue
		}
		file := m.ID
		if refs, ok := g.Refs(m.ID); ok && len(refs.Defs) > 0 {
			file = refs.Defs[0].File
		}
		files = append(files, file)
	}
	if len(files) < 2 {
		return nil
	}
	slices.Sort(files)
	return files
}
