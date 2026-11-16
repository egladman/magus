package guard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// narrowCoverageAt weighs the passing runs the checkout's output store holds
// against each file commit changed since its newest gated ancestor, else since
// the merge base. dir is the checkout the push runs in, and lineage names
// that ancestor.
func narrowCoverageAt(ctx context.Context, deps Dependencies, at location, dir, commit string, lineage gateLineage) narrowCoverage {
	if deps.Inspect == nil || at.workspace == "" || at.cacheDir == "" || !matchableRevision(commit) {
		return narrowCoverage{unread: "this caller cannot open the workspace or its output store"}
	}
	res, err := vcs.Resolve(ctx, dir, "", deps.VCS)
	if err != nil || res.VCS == nil {
		return narrowCoverage{unread: "no version control answers what the push changes"}
	}
	root, err := res.VCS.Root(ctx, dir)
	if err != nil {
		return narrowCoverage{unread: fmt.Sprintf("the checkout root is unread: %v", err)}
	}
	n := narrowCoverage{since: res.Base}
	if lineage.descends {
		n.since, n.gated = lineage.ancestor, true
	}
	changed, err := res.VCS.RangeFiles(ctx, dir, n.since, commit, nil)
	if err != nil {
		return narrowCoverage{unread: fmt.Sprintf("the files changed since %s are unread: %v", n.since, err)}
	}
	ws, err := deps.inspect(ctx, at.workspace)
	if err != nil {
		return narrowCoverage{unread: fmt.Sprintf("the workspace did not open, so no target's inputs are known: %v", err)}
	}
	runs, err := cache.NewOutputStore(at.cacheDir).ListDescriptors()
	if err != nil {
		return narrowCoverage{unread: fmt.Sprintf("the output store is unread: %v", err)}
	}
	ev := newRunEvidence(res.VCS, realPath(root), realPath(at.workspace), ws, runs, commit)
	weigh, hostless := splitHostless(ev.root, changed)
	n.hostless = hostless
	if err := ev.readRevisions(ctx, weigh); err != nil {
		return narrowCoverage{unread: fmt.Sprintf("the changed files are unread: %v", err)}
	}
	n.weighed, n.changed = true, changed
	for _, file := range weigh {
		if !ev.covered(ctx, file) {
			n.uncovered = append(n.uncovered, uncoveredFile{path: file, runs: ev.wouldCover(file)})
		}
	}
	return n
}

// runEvidence matches the checkout's recorded runs to the files a push changes.
type runEvidence struct {
	driver types.VCSDriver
	// root is the checkout's top level and workspace the workspace inside it,
	// both with symlinks resolved.
	root, workspace string
	ws              types.WorkspaceRepository
	runs            []cache.OutputDescriptor
	commit          string
	files           map[revisionFile]fileAt
	revisions       map[string]bool
	// differingPaths is, per clean run's revision the repository holds, the
	// files it holds unlike commit.
	differingPaths map[string]map[string]bool
	// footprints is each run's, by its index in runs, and declared each
	// magusfile target's. Both name glob sets by index in globSets.
	footprints []runFootprint
	declared   []declaredTarget
	globSets   [][]types.Glob
	// claimed memoizes claiming.
	claimed map[string][]bool
}

// newRunEvidence weighs runs, recorded in the checkout at root, against what
// commit holds. workspace is the workspace inside root.
func newRunEvidence(driver types.VCSDriver, root, workspace string, ws types.WorkspaceRepository, runs []cache.OutputDescriptor, commit string) *runEvidence {
	ev := &runEvidence{
		driver: driver, root: root, workspace: workspace, ws: ws, runs: runs, commit: commit,
		files: map[revisionFile]fileAt{}, revisions: map[string]bool{}, claimed: map[string][]bool{},
	}
	ev.index()
	return ev
}

// targetFootprint names the glob sets one target declares as its inputs and
// its outputs.
type targetFootprint struct {
	inputs, outputs int
}

// runFootprint is one run's target footprint and the paths its args name.
// known is false when the run names no target of a project the workspace
// holds.
type runFootprint struct {
	targetFootprint
	known bool
	named []string
}

// declaredTarget is a magusfile target wouldCover may name.
type declaredTarget struct {
	project, target string
	targetFootprint
}

// index roots each target's globs and reads each run's named paths once.
// Done per changed file, they spent a 1104-file push's whole deadline.
func (ev *runEvidence) index() {
	setIDs := map[string]int{}
	targets := map[[2]string]targetFootprint{}
	footprint := func(p *types.Project, target string) targetFootprint {
		key := [2]string{p.Path, target}
		fp, ok := targets[key]
		if !ok {
			fp = targetFootprint{
				inputs:  ev.globSet(setIDs, rootedInputs(p, target)),
				outputs: ev.globSet(setIDs, rootedOutputs(p, target)),
			}
			targets[key] = fp
		}
		return fp
	}
	named := map[[2]string][]string{}
	ev.footprints = make([]runFootprint, len(ev.runs))
	for i, d := range ev.runs {
		p := ev.ws.Get(d.Project)
		if p == nil || d.Target == "" {
			continue
		}
		key := [2]string{d.Project, strings.Join(d.ExtraArgs, "\x00")}
		paths, ok := named[key]
		if !ok {
			paths = ev.namedPaths(d)
			named[key] = paths
		}
		ev.footprints[i] = runFootprint{targetFootprint: footprint(p, types.Normalize(bareTarget(d.Target))), known: true, named: paths}
	}
	for _, p := range ev.ws.All() {
		for _, target := range p.MagusfileTargets {
			if target != string(types.TargetCI) {
				ev.declared = append(ev.declared, declaredTarget{project: p.Path, target: target, targetFootprint: footprint(p, target)})
			}
		}
	}
}

// globSet is the index in ev.globSets of a set matching as globs do, added
// when none does. Targets of one project often share its sources.
func (ev *runEvidence) globSet(ids map[string]int, globs []types.Glob) int {
	var key strings.Builder
	for _, g := range globs {
		key.WriteString(g.Pattern)
		for _, e := range g.Except {
			key.WriteString("\x01" + e)
		}
		key.WriteString("\x00")
	}
	id, ok := ids[key.String()]
	if !ok {
		id = len(ev.globSets)
		ev.globSets = append(ev.globSets, globs)
		ids[key.String()] = id
	}
	return id
}

// claiming reports, for each of ev.globSets, whether it claims rel.
func (ev *runEvidence) claiming(rel string) []bool {
	if got, ok := ev.claimed[rel]; ok {
		return got
	}
	got := make([]bool, len(ev.globSets))
	for i, set := range ev.globSets {
		got[i] = slices.ContainsFunc(set, func(g types.Glob) bool { return g.Match(rel) })
	}
	ev.claimed[rel] = got
	return got
}

// fileAt is one file at one revision; known is false when the revision is not
// in the repository.
type fileAt struct {
	body           string
	present, known bool
}

// covered reports a passing run that read file as commit holds it. A run
// whose target outputs hold file and wrote it so covers it too. file is
// repository-relative.
func (ev *runEvidence) covered(ctx context.Context, file string) bool {
	rel, inside := ev.workspacePath(file)
	if !inside {
		return false
	}
	claims := ev.claiming(rel)
	for i, d := range ev.runs {
		if d.Failed {
			continue
		}
		if ev.reads(i, claims, rel, true) && ev.heldAsCommit(ctx, file, d, false) || ev.writes(i, claims) && ev.heldAsCommit(ctx, file, d, true) {
			return true
		}
	}
	return false
}

// wouldCover names at most three runs that would cover file: targets this
// store ran over it, else magusfile targets whose inputs hold it.
func (ev *runEvidence) wouldCover(file string) []string {
	rel, inside := ev.workspacePath(file)
	if !inside {
		return nil
	}
	claims := ev.claiming(rel)
	var out []string
	add := func(command string) {
		if len(out) < 3 && !slices.Contains(out, command) {
			out = append(out, command)
		}
	}
	for i, d := range ev.runs {
		if len(out) == 3 {
			return out
		}
		if ev.writes(i, claims) {
			add(hint.Run.With(bareTarget(d.Target), d.Project))
			continue
		}
		if !ev.reads(i, claims, rel, false) {
			continue
		}
		target := bareTarget(d.Target)
		if len(ev.footprints[i].named) > 0 {
			add(hint.Run.With(target, d.Project, "--", projectRelative(d.Project, rel)))
		} else {
			add(hint.Run.With(target, d.Project))
		}
	}
	for _, t := range ev.declared {
		if claims[t.inputs] || claims[t.outputs] {
			add(hint.Run.With(t.target, t.project))
		}
	}
	return out
}

// workspacePath is file, repository-relative, relative to the workspace.
func (ev *runEvidence) workspacePath(file string) (string, bool) {
	prefix, err := filepath.Rel(ev.root, ev.workspace)
	if err != nil {
		return "", false
	}
	if prefix = filepath.ToSlash(prefix); prefix == "." {
		return file, true
	}
	return strings.CutPrefix(file, prefix+"/")
}

// reads reports whether run i's target declares rel among its inputs. claims
// is claiming(rel). scoped also holds a file-scoped run to the paths it named.
func (ev *runEvidence) reads(i int, claims []bool, rel string, scoped bool) bool {
	r := ev.footprints[i]
	if !r.known || !claims[r.inputs] {
		return false
	}
	if !scoped || len(r.named) == 0 {
		return true
	}
	return slices.ContainsFunc(r.named, func(n string) bool { return n == rel || strings.HasPrefix(rel, n+"/") })
}

// namedPaths are the workspace paths run d's forwarded args name, read
// against its project and then the workspace. None means the run is not
// file-scoped: `-run Push` names no path.
func (ev *runEvidence) namedPaths(d cache.OutputDescriptor) []string {
	var out []string
	for _, arg := range d.ExtraArgs {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		for _, c := range []string{path.Join(d.Project, arg), path.Clean(arg)} {
			if !filepath.IsLocal(filepath.FromSlash(c)) {
				continue
			}
			if _, err := os.Lstat(filepath.Join(ev.workspace, filepath.FromSlash(c))); err == nil {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// writes reports whether run i's target declares, among its outputs, the
// file claims is claiming of.
func (ev *runEvidence) writes(i int, claims []bool) bool {
	r := ev.footprints[i]
	return r.known && claims[r.outputs]
}

// heldAsCommit reports whether run d read file (wrote it, when wrote is set)
// as commit holds it. A clean run is compared at its revision. A dirty run
// needs the tree to hold commit's file, unwritten since the run started, or
// for a writer since it finished.
func (ev *runEvidence) heldAsCommit(ctx context.Context, file string, d cache.OutputDescriptor, wrote bool) bool {
	want := ev.at(ctx, ev.commit, file)
	if !want.known {
		return false
	}
	if d.Revision != "" && !d.Dirty {
		// readRevisions answered every revision the repository holds.
		differing, held := ev.differingPaths[d.Revision]
		return held && !differing[file]
	}
	started := time.UnixMilli(d.TimestampMs - d.DurationMs)
	if wrote {
		started = time.UnixMilli(d.TimestampMs)
	}
	full := filepath.Join(ev.root, filepath.FromSlash(file))
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		// A deletion dates only its directory.
		dir, err := os.Lstat(filepath.Dir(full))
		return !want.present && err == nil && !dir.ModTime().After(started)
	}
	if err != nil || !want.present || info.ModTime().After(started) {
		return false
	}
	body, err := os.ReadFile(full)
	return err == nil && string(body) == want.body
}

func (ev *runEvidence) at(ctx context.Context, rev, file string) fileAt {
	key := revisionFile{rev: rev, path: file}
	if got, ok := ev.files[key]; ok {
		return got
	}
	got := fileAt{}
	if ev.known(ctx, rev) {
		// ReadFileAt errs on a path the revision lacks.
		body, err := ev.driver.ReadFileAt(ctx, ev.root, rev, file)
		if ctx.Err() != nil {
			// A cut-short read is not an absent file; absent at both covers.
			return fileAt{}
		}
		got = fileAt{body: body, present: err == nil, known: true}
	}
	ev.files[key] = got
	return got
}

// known reports whether the repository holds rev, asked once per revision.
func (ev *runEvidence) known(ctx context.Context, rev string) bool {
	known, asked := ev.revisions[rev]
	if !asked {
		_, err := ev.driver.FindCommit(ctx, ev.root, rev)
		known = err == nil
		ev.revisions[rev] = known
	}
	return known
}

// readRevisions answers what heldAsCommit compares of files: which files
// each clean run's revision holds unlike commit, and commit's copy of each.
// A process per file and revision spent a 1104-file push's deadline.
func (ev *runEvidence) readRevisions(ctx context.Context, files []string) error {
	var weighed []string
	compared := map[string][]string{}
	asked := map[revisionFile]bool{}
	for _, file := range files {
		rel, inside := ev.workspacePath(file)
		if !inside {
			continue
		}
		claims := ev.claiming(rel)
		read := false
		for i, d := range ev.runs {
			if d.Failed || !ev.reads(i, claims, rel, true) && !ev.writes(i, claims) {
				continue
			}
			read = true
			key := revisionFile{rev: d.Revision, path: file}
			if d.Revision != "" && !d.Dirty && !asked[key] {
				asked[key] = true
				compared[d.Revision] = append(compared[d.Revision], file)
			}
		}
		if read {
			weighed = append(weighed, file)
		}
	}
	if len(weighed) == 0 {
		return nil
	}
	differing, err := vcs.DifferingPathsByRevision(ctx, ev.driver, ev.root, ev.commit, compared)
	if err != nil {
		return err
	}
	ev.differingPaths = make(map[string]map[string]bool, len(differing))
	for rev, paths := range differing {
		ev.differingPaths[rev] = make(map[string]bool, len(paths))
		for _, p := range paths {
			ev.differingPaths[rev][p] = true
		}
	}
	return ev.readCommitFiles(ctx, weighed)
}

// readCommitFiles reads commit's copy of each of files through one batch,
// where the driver opens one. Otherwise at reads each when asked.
func (ev *runEvidence) readCommitFiles(ctx context.Context, files []string) error {
	opener, ok := ev.driver.(vcs.ObjectBatchOpener)
	if !ok || !ev.known(ctx, ev.commit) {
		return nil
	}
	batch, err := opener.OpenObjectBatch(ctx, ev.root, ev.commit)
	if err != nil {
		return err
	}
	for _, file := range files {
		// A batch request is one line.
		if strings.ContainsAny(file, "\n\r") {
			continue
		}
		body, err := batch.Read(file)
		switch {
		case errors.Is(err, vcs.ErrObjectMissing):
			ev.files[revisionFile{rev: ev.commit, path: file}] = fileAt{known: true}
		case err != nil:
			return errors.Join(fmt.Errorf("read %s at %s: %w", file, ev.commit, err), batch.Close())
		default:
			ev.files[revisionFile{rev: ev.commit, path: file}] = fileAt{body: body, present: true, known: true}
		}
	}
	return batch.Close()
}

// revisionFile is one repository-relative path at one revision.
type revisionFile struct {
	rev, path string
}

// admits reports a coverage that lets a push out. Each rests on a read that
// finished: a read the budget cuts answers no coverage.
func (c gateCoverage) admits() bool {
	switch c {
	case gatePassed, gateWaived, gateNarrow, gateUnrun, gateRemote:
		return true
	}
	return false
}

// pushLapse is the read of pushCoverage underway when its budget ran out.
type pushLapse struct {
	// cut is that read's phase, "" when none ran out.
	cut    string
	budget time.Duration
}

// reason words the lapse as the refusal of commit, "" when it went unread.
func (l pushLapse) reason(commit string) string {
	what := cmp.Or(commit, "this revision")
	cut := lapseWording{pushReadWording(l.cut), "finish"}.clause(l.budget)
	return fmt.Sprintf("magus did not judge pushing %s: %s. "+
		"A push is weighed within a share of the guard's %s, and one it cannot judge is refused, never let through. "+
		"A retry passes only if judging costs less this time, such as on a less loaded machine; if it lapses twice, report it rather than repeat it.\n",
		what, cut, hookBudget)
}

// pushReadWording names phase, a read pushCoverage makes, for a refusal.
func pushReadWording(phase string) string {
	switch phase {
	case phasePushRevision:
		return "reading the pushed revision"
	case phasePushGateHistory:
		return "reading the recorded `ci` verdicts"
	case phasePushRunLog:
		return "reading the run log"
	case phasePushLineage:
		return "placing the commit against passing `ci` runs"
	case phasePushWaiver:
		return "reading the waivers"
	case phasePushNarrow:
		return "weighing the changed files against recorded runs"
	case phaseInspect:
		return "opening the workspace to weigh the changed files"
	case phasePushRemoteGate:
		return "reading ci.remote_gate"
	case phasePushMoves:
		return "reading what the line runs before the push"
	}
	return fmt.Sprintf("the %s read", phase)
}

// pushCoverageShare is the most of the hook's budget pushCoverage spends,
// half as jobRowsBudget is, so the workspace rules judged after it keep the
// rest.
const pushCoverageShare = hookBudget / 2

// pushCoverageBudget is share, or what remains of ctx's deadline less
// judgeReserve when that is less, as rereadJobStoreRows bounds its wait. A
// dry run has no share: it gets the whole of what remains.
func pushCoverageBudget(ctx context.Context, share time.Duration) time.Duration {
	left, bounded := untilReserve(ctx)
	switch {
	case !bounded:
		return share
	case isDryRun(ctx):
		return left
	}
	return min(share, left)
}

// pushReads records the reads of one pushCoverage as phases of log, and
// lapse, the first a budget cut.
type pushReads struct {
	log *phaseLog
	// budget is pushCoverage's own.
	budget time.Duration
	lapse  pushLapse
}

// pushStep runs read as phase, one read of pushCoverage bounded by ctx. A
// read is cut when ctx is done by its end. A phase of its own that lapsed
// inside it, such as the workspace open, is the cut, with its budget.
func pushStep[T any](ctx context.Context, r *pushReads, phase string, read func() T) T {
	r.log.begin(phase)
	start := time.Now()
	before := len(r.log.finished())
	v := read()
	cut := r.lapse.cut == "" && ctx.Err() != nil
	if r.lapse.cut == "" {
		for _, p := range r.log.finished()[before:] {
			if p.Lapsed {
				r.lapse = pushLapse{cut: p.Name, budget: time.Duration(p.BudgetMs) * time.Millisecond}
				break
			}
		}
	}
	if cut && r.lapse.cut == "" {
		r.lapse = pushLapse{cut: phase, budget: r.budget}
	}
	r.log.record(timedPhase(phase, time.Since(start), 0, cut))
	return v
}
