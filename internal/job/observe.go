package job

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// SymbolReader answers what the knowledge graph knows about a symbol. Supplied by the
// caller rather than reached for here, because internal/job grades evidence and does not
// own a graph; the CLI, the MCP tool and the Buzz binding each already hold one.
//
// signature is a declared signature to compare with the indexed one, which the fact
// answers in SignatureMatch; "" reads the symbol only.
type SymbolReader func(ctx context.Context, name, signature string) (SymbolFact, bool)

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
//
// The regions a diff placed are cached in the footprint cache on ctx, which [Wait] and
// [GradeGates] set to their store's, so a dry-run store's grading writes none.
func CheckpointObserver(root string, symbols SymbolReader) Observer {
	return func(ctx context.Context, row types.Job) (Observed, error) {
		ctx = withFootprintCache(ctx, footprintCacheIn(ctx).forRow(row))
		tree := WorkTree(root, row)
		var driver types.VCSDriver
		unresolved := ""
		if checkpointRevision(row.Checkpoint) != "" {
			driver, unresolved = resolveDriver(ctx, tree)
		}
		base := diffBase(ctx, driver, tree, row)
		seen := changedSince(ctx, driver, unresolved, tree, base.revision)
		if seen.ChangedFrom != "" {
			seen.ChangedFrom = base.named
		}
		if seen.ChangedKnown {
			seen.ChangedRevision = checkpointRevision(base.revision)
		}
		seen.Present, seen.PresentKnown = presentIn(tree, row)
		seen.readSymbols(ctx, row.EffectiveGoals(), symbols)
		// Workspace only, as for every rule that acts: a default in one person's global
		// config must not loosen what a check accepts. An unreadable magus.yaml leaves no
		// defaults, so a check binds only to the charms it names.
		if cfg, err := config.LoadWorkspaceOnly(tree); err == nil {
			seen.DefaultCharms = cfg.DefaultCharms
		}
		return seen, nil
	}
}

// WorkTree is the checkout a job's work lives in: the one that took it, while it still
// exists, else root. The waiter usually sits in another checkout, and grading that one's
// diff held the job to work it never did.
func WorkTree(root string, row types.Job) string {
	if row.CheckoutRoot == "" || root == "" {
		return root
	}
	if info, err := os.Stat(row.CheckoutRoot); err != nil || !info.IsDir() {
		return root
	}
	return row.CheckoutRoot
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

// checkpointRevision is the revision half of a checkpoint token, the only half a VCS can
// resolve; see [types.ParseCheckpointToken].
func checkpointRevision(checkpoint string) string {
	revision, _ := types.ParseCheckpointToken(checkpoint)
	return revision
}

// diffFrom is the revision a holder's diff is read from.
type diffFrom struct {
	// revision is what the VCS is asked to diff against.
	revision string
	// named is revision as a reader is shown it, with where it came from when that is not
	// the checkpoint, and why the merge base was not read when it was asked for and failed.
	named string
}

// diffBase is the revision a holder's diff is read from: the merge base of the holder's
// HEAD and its checkpoint, else the revision the holder reported, else the checkpoint. A
// checkpoint the holder's branch never had would otherwise read every commit between the
// two as the holder's work. A base other than the checkpoint is named beside it, so a
// worker that branched from the wrong one shows, and so is a merge base that could not be
// read, so the fallback does not pass for the merge base.
//
// A holder that rebased onto a newer base still diffs from its checkpoint: the checkpoint
// is then an ancestor of HEAD, so it is the merge base, and the commits the rebase brought
// in read as the holder's. ReportedBase is not consulted there, since it is the base the
// holder reported when it took the job, not the one it rebased onto.
func diffBase(ctx context.Context, driver types.VCSDriver, tree string, row types.Job) diffFrom {
	checkpoint := checkpointRevision(row.Checkpoint)
	if checkpoint == "" || driver == nil {
		return diffFrom{revision: checkpoint, named: checkpoint}
	}
	var failed error
	switch head, err := driver.FindCommit(ctx, tree, ""); {
	case err != nil:
		failed = fmt.Errorf("the holder's HEAD could not be read: %w", err)
	case head.ID == "":
		failed = errors.New("the holder's checkout has no HEAD")
	default:
		base, ok, err := driver.MergeBase(ctx, tree, head.ID, checkpoint)
		switch {
		case err != nil:
			failed = err
		case !ok:
			failed = fmt.Errorf("the holder's HEAD %s shares no history with its checkpoint", head.ID)
		case SameRevision(base, checkpoint):
			return diffFrom{revision: checkpoint, named: checkpoint}
		default:
			return diffFrom{revision: base, named: fmt.Sprintf("%s (the merge base of the holder's HEAD %s and its checkpoint %s)", base, head.ID, checkpoint)}
		}
	}
	unread := fmt.Sprintf("no merge base: %v", failed)
	if reported := checkpointRevision(row.ReportedBase); reported != "" && !SameRevision(reported, checkpoint) {
		return diffFrom{revision: reported, named: fmt.Sprintf("%s (the base its holder reported, not its checkpoint %s; %s)", reported, checkpoint, unread)}
	}
	return diffFrom{revision: checkpoint, named: fmt.Sprintf("%s (its checkpoint; %s)", checkpoint, unread)}
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
	cache := footprintCacheIn(ctx)
	key, keyed := cache.regionsKey(ctx, driver, root, revision, files)
	if keyed {
		if regions, ok := cache.read(key); ok {
			return regions, true, ""
		}
	}
	regions, err := driver.Regions(ctx, root, revision, files)
	var declined *types.VCSUnsupportedError
	switch {
	case errors.As(err, &declined):
		return nil, false, fmt.Sprintf("%s does not report changed regions (%s)", declined.VCS, declined.Capability)
	case err != nil:
		return nil, false, fmt.Sprintf("the regions changed since %s could not be read: %v", revision, err)
	}
	// A file rewritten while Regions read it would file these regions under content they
	// were not read from.
	if keyed {
		if again, ok := cache.regionsKey(ctx, driver, root, revision, files); ok && again == key {
			cache.write(key, regions)
		}
	}
	return regions, true, ""
}

// MeasureOverlaps is [OverlapFootprints] with the version control resolved at root, the one
// measurement every read door that reports overlaps calls, so `magus ls jobs` and
// magus\job.list cannot disagree about a footprint. Only the overlaps pay for it: a list
// with none reads no VCS at all. Version control that is disabled or does not resolve
// leaves each footprint unknown, naming why.
func MeasureOverlaps(ctx context.Context, root string, rows []types.Job, overlaps []types.JobOverlap) []types.JobOverlap {
	if len(overlaps) == 0 {
		return overlaps
	}
	var driver types.VCSDriver
	if res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{}); err == nil && res.Source != types.VCSSourceDisabled {
		driver = res.VCS
	}
	return OverlapFootprints(ctx, driver, rows, overlaps)
}

// Report is the job list every read door serves: the rows, swept as [Store.List] sweeps
// them; the overlaps, blocked and read-only rows [types.NewJobList] derives; the rows
// flagged overdue, orphaned or stale as of now (unix seconds) against staleAfter; and each
// overlap's footprint measured at the store's checkout. `magus ls jobs`, magus\job.list and
// JobService's ListJobs all build it here, so no two doors disagree about a row.
//
// It never joins the queue snapshot: in-flight changes are the queue's, and a door that
// wants them joins them itself.
func (s *Store) Report(ctx context.Context, now int64, staleAfter time.Duration) (types.JobList, error) {
	rows, err := s.List()
	if err != nil {
		return types.JobList{}, err
	}
	list := types.NewJobList(HardenWritten(ctx, s, rows)).Flag(now, staleAfter)
	// A footprint reads each holder's diff, a VCS call per changed path, and a stale row's
	// holder is the one least likely to still be writing: on a store of abandoned rows,
	// measuring them all held `magus ls jobs` for minutes.
	stale := func(o types.JobOverlap) bool {
		return slices.Contains(list.Stale, o.JobA) || slices.Contains(list.Stale, o.JobB)
	}
	measured := MeasureOverlaps(withFootprintCache(ctx, s.footprintCache()), s.root, list.Jobs, slices.DeleteFunc(slices.Clone(list.Overlaps), stale))
	for i, o := range list.Overlaps {
		if !stale(o) {
			list.Overlaps[i], measured = measured[0], measured[1:]
			continue
		}
		list.Overlaps[i].Footprint = &types.JobOverlapFootprint{Verdict: types.FootprintUnknown,
			Reason: fmt.Sprintf("not measured: %s or %s is stale (jobs.stale_after is %s)", o.JobA, o.JobB, staleAfter)}
	}
	return list, nil
}

// StaleAfter is jobs.stale_after from the workspace's own magus.yaml: the window the sweep
// ends untaken rows by, and the one a door with no config of its own flags stale rows by.
func (s *Store) StaleAfter() (time.Duration, error) { return s.resolveStaleAfter() }

// OverlapFootprints fills each overlap's Footprint: whether the two jobs' diffs, each
// taken in the checkout that job was taken in (its CheckoutRoot) against its own
// checkpoint, touch a common declaration. It returns a copy and never fails; every
// question it cannot answer is a FootprintUnknown verdict naming why. A nil driver is
// version control that did not resolve.
//
// Both sides of each diff are compared, per [types.Collisions]: two jobs deleting lines
// from one declaration both touch it.
func OverlapFootprints(ctx context.Context, driver types.VCSDriver, rows []types.Job, overlaps []types.JobOverlap) []types.JobOverlap {
	if len(overlaps) == 0 {
		return overlaps
	}
	footprints := map[string]footprint{}
	footprintOf := func(id string) footprint {
		fp, ok := footprints[id]
		if !ok {
			fp = readFootprint(ctx, driver, rows, id)
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

func readFootprint(ctx context.Context, driver types.VCSDriver, rows []types.Job, id string) footprint {
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return footprint{reason: fmt.Sprintf("no job %s is declared", id)}
	}
	revision := checkpointRevision(rows[i].Checkpoint)
	if revision == "" {
		return footprint{reason: fmt.Sprintf("%s was declared without a checkpoint", id)}
	}
	if driver == nil {
		return footprint{reason: "no version control answered here"}
	}
	dir := rows[i].CheckoutRoot
	if dir == "" {
		return footprint{reason: fmt.Sprintf("no checkout of this repository has taken %s", id)}
	}
	cache := footprintCacheIn(ctx).forRow(rows[i])
	key, keyed := cache.footprintKey(ctx, driver, rows[i])
	if keyed {
		if regions, ok := cache.read(key); ok {
			return footprint{regions: regions, known: true}
		}
	}
	seen := changedSince(withFootprintCache(ctx, cache), driver, "", dir, diffBase(ctx, driver, dir, rows[i]).revision)
	fp := footprint{regions: seen.Regions, known: seen.RegionsKnown, reason: seen.RegionsReason}
	// The checkout is probed again because a commit or an edit landing while the diff was
	// read would file this footprint under a state it was not read from.
	if keyed && fp.known {
		if again, ok := cache.footprintKey(ctx, driver, rows[i]); ok && again == key {
			cache.write(key, fp.regions)
		}
	}
	return fp
}

// compareFootprints collides two footprints' regions, both sides of each.
func compareFootprints(idA string, a footprint, idB string, b footprint) types.JobOverlapFootprint {
	var unknown []string
	for _, side := range []struct {
		id string
		fp footprint
	}{{idA, a}, {idB, b}} {
		if !side.fp.known {
			unknown = append(unknown, fmt.Sprintf("%s: %s", side.id, side.fp.reason))
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
	for _, gate := range row.EffectiveGoals() {
		if gate.Kind != types.GoalKindPaths {
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

// readSymbols resolves every symbol goals name into o: a signature goal's against its
// declared signature, every other one's plainly. One miss from the reader leaves the whole
// observation unknown: a graph that answered for three names and failed on the fourth cannot
// be told apart from one that found the fourth absent, and those mean opposite things to a
// gate.
func (o *Observed) readSymbols(ctx context.Context, goals []types.Goal, read SymbolReader) {
	o.Symbols, o.Signatures, o.conflicts, o.SymbolsKnown = nil, nil, nil, false
	if read == nil {
		return
	}
	symbols, signatures := map[string]SymbolFact{}, map[string]SymbolFact{}
	declared, conflicts := map[string]string{}, map[string]bool{}
	for _, gate := range goals {
		if gate.Kind != types.GoalKindSymbol {
			continue
		}
		for _, name := range gate.Symbols {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			signature := ""
			if gate.Expect == types.ExpectSignature {
				signature = gate.Signature
				if prior, ok := declared[name]; ok && prior != signature {
					conflicts[name] = true
					continue
				}
				declared[name] = signature
			}
			fact, ok := read(ctx, name, signature)
			if !ok {
				return
			}
			if gate.Expect == types.ExpectSignature {
				signatures[name] = fact
			} else {
				symbols[name] = fact
			}
		}
	}
	o.Symbols, o.Signatures, o.SymbolsKnown = symbols, signatures, true
	if len(conflicts) > 0 {
		o.conflicts = conflicts
	}
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
	// SignatureMatches compares a declared signature with the one the graph holds for
	// symbol node id; ok is false when it holds none.
	SignatureMatches(id, declared string) (indexed string, match, ok bool)
}

// GraphSymbols reads symbol facts off a loaded graph. A nil graph reads as UNREADABLE
// rather than as a graph with nothing in it, which is the distinction a symbol gate turns
// on: absent and unknown mean opposite things to a verdict.
func GraphSymbols(g SymbolGraph) SymbolReader {
	return func(_ context.Context, name, signature string) (SymbolFact, bool) {
		if g == nil {
			return SymbolFact{}, false
		}
		out, ok := g.Refs(bareName(name))
		if ok && !namesSymbol(out, name) {
			// Refs ranks fuzzily, so a deleted Foo resolves to a surviving FooBar and
			// `present` passes on it. Only a symbol carrying the name answers for it.
			out, ok = exactSymbol(g, name)
		}
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
		if indexed, match, ok := g.SignatureMatches(out.Symbol, signature); ok {
			fact.Signature = indexed
			fact.SignatureMatch = signature != "" && match
		}
		return fact, true
	}
}

// namesSymbol reports whether a resolved symbol is the one name declares: its id, its
// label, a qualified tail of its id such as `Declaration#Validate`, or `<package>.<Name>`.
func namesSymbol(out types.KnowledgeRefsOutput, name string) bool {
	if out.Symbol == name || out.Label == name {
		return true
	}
	id := strings.TrimRight(strings.TrimPrefix(out.Symbol, types.KindSymbol+":"), "().#:")
	if pkg, rest, ok := packageQualified(name); ok {
		return id == fmt.Sprintf("%s/%s", pkg, rest) || id == fmt.Sprintf("%s %s", pkg, rest) || strings.HasSuffix(id, fmt.Sprintf("/%s/%s", pkg, rest)) || strings.HasSuffix(id, fmt.Sprintf("/%s %s", pkg, rest))
	}
	return id == name || strings.HasSuffix(id, "/"+name) || strings.HasSuffix(id, " "+name)
}

// packageQualified splits `<package>.<Name>`, the last path segment of a package and a name
// in it. A symbol id, a path and a name holding no dot are not of that shape.
func packageQualified(name string) (pkg, rest string, ok bool) {
	pkg, rest, ok = strings.Cut(name, ".")
	if !ok || pkg == "" || rest == "" || strings.ContainsAny(name, "/ :") || strings.Contains(rest, ".") {
		return "", "", false
	}
	return pkg, rest, true
}

// bareName is the label a name searches the graph by: the name itself, or for
// `<package>.<Name>` the name's last segment.
func bareName(name string) string {
	if _, rest, ok := packageQualified(name); ok {
		_, label, _ := strings.Cut(rest, "#")
		return cmp.Or(label, rest)
	}
	return name
}

// matchesName reports whether graph match m is a symbol name declares: its label is the
// name's bare label, and under `<package>.<Name>` it sits in that package.
func matchesName(m types.KnowledgeMatch, name string) bool {
	if m.Kind != types.KindSymbol || m.Label != bareName(name) {
		return false
	}
	if _, _, ok := packageQualified(name); !ok {
		return true
	}
	return namesSymbol(types.KnowledgeRefsOutput{Symbol: m.ID}, name)
}

// exactSymbol is the top-ranked symbol name declares.
func exactSymbol(g SymbolGraph, name string) (types.KnowledgeRefsOutput, bool) {
	for _, m := range g.Resolve(bareName(name), 0) {
		if matchesName(m, name) {
			return g.Refs(m.ID)
		}
	}
	return types.KnowledgeRefsOutput{}, false
}

// sameNameDefinitions is one defining file per symbol name declares, or nil when fewer
// than two are.
func sameNameDefinitions(g SymbolGraph, name string) []string {
	var files []string
	for _, m := range g.Resolve(bareName(name), 0) {
		if !matchesName(m, name) {
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

// footprintCache keeps what reading a footprint cost, under the job store's directory, so an
// unchanged checkout is not diffed again: the regions a diff placed, keyed by what they were
// read from (see regionsKey), and a row's whole footprint, keyed by the row and the state of
// its checkout (see footprintKey). Entries sit in one directory per checkpoint revision,
// which retention prunes by (see [Store.pruneFootprints]).
//
// An entry that will not read, or whose recorded key is not the one asked, is a miss, and a
// miss computes as if there were no cache: it may cost a recomputation and never a verdict.
// The zero value reads and writes nothing.
type footprintCache struct {
	dir string
	// revision is the checkpoint revision of the row being read; its entries go under it.
	revision string
	readOnly bool
	// build is the running build's identity (see [Location.Build]); empty keys nothing.
	build string
}

type footprintCacheKey struct{}

func withFootprintCache(ctx context.Context, c footprintCache) context.Context {
	return context.WithValue(ctx, footprintCacheKey{}, c)
}

func footprintCacheIn(ctx context.Context) footprintCache {
	c, _ := ctx.Value(footprintCacheKey{}).(footprintCache)
	return c
}

// footprintsDir is where the store whose ledger is at ledger keeps its footprint cache.
func footprintsDir(ledger string) string {
	return filepath.Join(filepath.Dir(ledger), "footprints")
}

// footprintCache is s's cache; a dry-run store reads it and writes nothing.
func (s *Store) footprintCache() footprintCache {
	if s.err != nil || s.path == "" {
		return footprintCache{}
	}
	return footprintCache{dir: footprintsDir(s.path), readOnly: s.dryRun, build: s.build}
}

// forRow is c filing its entries under row's checkpoint revision.
func (c footprintCache) forRow(row types.Job) footprintCache {
	c.revision = checkpointRevision(row.Checkpoint)
	return c
}

// regionsKey is what regions read over files since revision in root depend on: the
// running build, which carries the default funcname patterns, the checkout, its HEAD,
// which with revision fixes the merge base every file's base side is read at, the
// configuration placement reads (see regionsConfig), and each file's name and working-tree
// content. A .gitattributes that changed is among files. false when c is off or any of them
// cannot be read.
func (c footprintCache) regionsKey(ctx context.Context, driver types.VCSDriver, root, revision string, files []types.FileChange) (string, bool) {
	h, ok := c.keyHash("regions", root, revision)
	if !ok || driver == nil {
		return "", false
	}
	head, err := driver.FindCommit(ctx, root, "")
	if err != nil || head.ID == "" {
		return "", false
	}
	fmt.Fprintf(h, "%s\x00", head.ID)
	if !regionsConfig(ctx, h, driver, root) {
		return "", false
	}
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", f.Path, f.PrevPath, f.Status)
		if !digestPath(h, root, f.Path, false) {
			return "", false
		}
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// footprintKey is what row's footprint depends on: the row as last written, its checkout,
// that checkout's HEAD and uncommitted state (see checkoutState) and the configuration
// placement reads there (see regionsConfig), read through driver. false when c is off or
// the checkout cannot be read.
func (c footprintCache) footprintKey(ctx context.Context, driver types.VCSDriver, row types.Job) (string, bool) {
	h, ok := c.keyHash("footprint", row.CheckoutRoot, row.Checkpoint)
	if !ok || driver == nil {
		return "", false
	}
	fmt.Fprintf(h, "%s\x00%d\x00%s\x00", row.ID, row.Updated, row.ReportedBase)
	if !checkoutState(ctx, h, driver, row.CheckoutRoot) || !regionsConfig(ctx, h, driver, row.CheckoutRoot) {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// regionsConfigReader is a backend that says what configuration its Regions reads besides
// the files and revisions it is handed, such as git's diff.<driver>.xfuncname and the
// attribute files outside the tree that pick each file's driver.
type regionsConfigReader interface {
	RegionsConfig(ctx context.Context, root string) (string, error)
}

// regionsConfig folds into h the configuration driver places lines in root with. A backend
// that cannot say what it reads keys nothing, since an edit to its configuration would then
// answer from an entry placed under the old one.
func regionsConfig(ctx context.Context, h hash.Hash, driver types.VCSDriver, root string) bool {
	reader, ok := driver.(regionsConfigReader)
	if !ok {
		return false
	}
	cfg, err := reader.RegionsConfig(ctx, root)
	if err != nil {
		return false
	}
	fmt.Fprintf(h, "%d\x00%s", len(cfg), cfg)
	return true
}

// keyHash starts a key of kind with the running build's identity and fields. The build is
// read once per process by whoever opened the Store, not stat'ed here per key (see
// BenchmarkFootprintKeyHash): a file rebuilt underneath the process must not speak for the
// code it loaded.
func (c footprintCache) keyHash(kind string, fields ...string) (hash.Hash, bool) {
	if c.dir == "" || c.revision == "" || c.build == "" {
		return nil, false
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s/2\x00%s\x00", kind, c.build)
	for _, f := range fields {
		fmt.Fprintf(h, "%s\x00", f)
	}
	return h, true
}

// checkoutState folds dir's HEAD and uncommitted state into h: the backend's diff of the
// tracked changes, which carries a deletion or rename no file content shows, and the content
// of every changed path, untracked directories walked. A clean checkout costs two probes.
func checkoutState(ctx context.Context, h hash.Hash, driver types.VCSDriver, dir string) bool {
	head, err := driver.FindCommit(ctx, dir, "")
	if err != nil || head.ID == "" {
		return false
	}
	dirty, err := driver.DirtyFiles(ctx, dir, nil)
	if err != nil {
		return false
	}
	fmt.Fprintf(h, "%s\x00%d\x00", head.ID, len(dirty))
	if len(dirty) == 0 {
		return true
	}
	patch, err := driver.DirtyDiff(ctx, dir, nil)
	if err != nil {
		return false
	}
	fmt.Fprintf(h, "%d\x00%s", len(patch), patch)
	dirty = slices.Clone(dirty)
	slices.Sort(dirty)
	for _, p := range dirty {
		fmt.Fprintf(h, "%s\x00", p)
		if !digestPath(h, dir, strings.TrimSuffix(p, "/"), true) {
			return false
		}
	}
	return true
}

// digestPath folds the content at root/rel into h: absent, a symlink's target, a file's
// bytes, or, when walk is set, every file beneath a directory. A directory without walk, or
// anything unreadable, reports false.
func digestPath(h hash.Hash, root, rel string, walk bool) bool {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprint(h, "absent\x00")
		return true
	case err != nil:
		return false
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(full)
		if err != nil {
			return false
		}
		fmt.Fprintf(h, "link\x00%s\x00", target)
		return true
	case info.IsDir():
		if !walk {
			return false
		}
		ok := true
		err := filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			sub, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "%s\x00", filepath.ToSlash(sub))
			if !digestPath(h, root, filepath.ToSlash(sub), false) {
				ok = false
				return filepath.SkipAll
			}
			return nil
		})
		return err == nil && ok
	case !info.Mode().IsRegular():
		return false
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return false
	}
	fmt.Fprintf(h, "file\x00%d\x00", len(body))
	_, _ = h.Write(body)
	return true
}

// footprintEntry is one cached read. Key is recorded so a file that answers to another key
// (a truncated name, a hand edit) is a miss rather than an answer.
type footprintEntry struct {
	Key     string               `json:"key"`
	Regions []types.RegionChange `json:"regions"`
}

func (c footprintCache) entryPath(key string) string {
	return filepath.Join(c.dir, revisionDir(c.revision), key+".json")
}

func (c footprintCache) read(key string) ([]types.RegionChange, bool) {
	raw, err := os.ReadFile(c.entryPath(key))
	if err != nil {
		return nil, false
	}
	var e footprintEntry
	if err := json.Unmarshal(raw, &e); err != nil || e.Key != key {
		return nil, false
	}
	return e.Regions, true
}

// write files regions under key. Best effort: a cache that cannot be written is a later
// miss, never a failed read.
func (c footprintCache) write(key string, regions []types.RegionChange) {
	if c.readOnly {
		return
	}
	raw, err := json.Marshal(footprintEntry{Key: key, Regions: regions})
	if err != nil {
		return
	}
	p := c.entryPath(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	_ = file.WriteFileAtomic(p, raw, 0o644)
}

// revisionDir is the directory a revision's entries go in: the revision itself when it is
// a plain name, as every backend's commit id is, else a digest of it.
func revisionDir(revision string) string {
	plain := revision != "" && revision != "." && revision != ".." && len(revision) <= 128 &&
		strings.Trim(revision, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ._-") == ""
	if plain {
		return revision
	}
	sum := sha256.Sum256([]byte(revision))
	return "rev-" + hex.EncodeToString(sum[:8])
}
