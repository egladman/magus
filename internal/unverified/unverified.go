// Package unverified reads which files a commit changed that no clean passing run in a
// checkout read.
//
// A run counts for a file when it passed, read the file (or wrote it), and the file held,
// when it ran, what the commit holds, whatever else in the tree was uncommitted. It read
// the file when its target's declared inputs claim it, held to the paths its command names
// when it names any; a target that declares no inputs read only the paths its command
// names. A clean run's revision says what the file held; for a run over uncommitted edits
// the tree must hold the commit's copy, unwritten since the run started. A cache hit
// records a new attempt over the current tree with Cached set, and counts as a run of the
// target's declared inputs. Evidence is per
// checkout, the store under <root>/.magus/outputs: a second worktree sees no runs until the
// targets run there, which are cache hits. A clean attempt can be evicted by five later
// attempts of the same key.
package unverified

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

// maxCommands is how many `magus run` lines a file names.
const maxCommands = 3

// readInput is one read: a checkout and the revision to weigh in it.
type readInput struct {
	Driver types.VCSDriver
	// Root is the checkout's top level and Workspace the workspace root inside it.
	Root, Workspace string
	// Base is the ref the merge base is taken with, Revision the full id read.
	Base, Revision string
	// Projects opens the workspace, whose targets' declared inputs and outputs say what a
	// run read. Runs lists the descriptors of the checkout's output store. Both are
	// called once, in the order the report's unknown names them.
	Projects func(context.Context) (Projects, error)
	Runs     func() ([]cache.OutputDescriptor, error)
}

// weigh reads the files Revision changed since its merge base with Base against the runs
// of its checkout. It runs under ctx's deadline and, on the first step that fails or meets
// an ended ctx, answers Unknown naming that step, with Changed and Unverified empty.
func weigh(ctx context.Context, in readInput) types.UnverifiedReport {
	short := in.Revision[:min(len(in.Revision), 12)]
	unknown := func(step string, err error) types.UnverifiedReport {
		return types.UnverifiedReport{Revision: in.Revision, Base: in.Base, Unknown: fmt.Sprintf("%s: %v", step, err)}
	}
	step := fmt.Sprintf("the files `%s` changed since its merge base with `%s` are unread", short, in.Base)
	changed, err := in.Driver.RangeFiles(ctx, in.Root, in.Base, in.Revision, nil)
	if err != nil || ctx.Err() != nil {
		return unknown(step, cmp.Or(err, ctx.Err()))
	}
	slices.Sort(changed)
	changed = slices.Compact(changed)
	report := types.UnverifiedReport{Revision: in.Revision, Base: in.Base, Changed: changed}
	if len(changed) == 0 {
		return report
	}
	step = "the workspace did not open, so no target's inputs are known"
	ws, err := in.Projects(ctx)
	if err != nil || ctx.Err() != nil {
		return unknown(step, cmp.Or(err, ctx.Err()))
	}
	step = "the output store is unread"
	runs, err := in.Runs()
	if err != nil || ctx.Err() != nil {
		return unknown(step, cmp.Or(err, ctx.Err()))
	}
	slices.SortStableFunc(runs, func(a, b cache.OutputDescriptor) int {
		return cmp.Or(cmp.Compare(b.TimestampMs, a.TimestampMs), cmp.Compare(b.PersistedNs, a.PersistedNs))
	})
	ev := newEvidence(in.Driver, realPath(in.Root), realPath(in.Workspace), ws, runs, in.Revision)
	step = fmt.Sprintf("comparing the runs' revisions with `%s`", short)
	if err := ev.readRevisions(ctx, changed); err != nil || ctx.Err() != nil {
		return unknown(step, cmp.Or(err, ctx.Err()))
	}
	for _, file := range changed {
		if !ev.covered(file) {
			report.Unverified = append(report.Unverified, types.UnverifiedFile{Path: file, Commands: ev.wouldCover(file)})
		}
	}
	report.Runs = ev.claimingRuns(changed, report.Unverified)
	return report
}

// Checkout is where a read happens: the checkout holding Dir, its workspace and the output
// store recorded there.
type Checkout struct {
	// Dir is any directory of the checkout, Workspace the workspace root and CacheDir the
	// store's directory. CacheDir is empty for a checkout no workspace holds, which has
	// recorded no run.
	Dir, Workspace, CacheDir string
	VCS                      types.VCSOptions
	// Projects opens the workspace, as readInput.Projects.
	Projects func(context.Context) (Projects, error)
}

// Projects is what a read asks of a workspace: every project, and one by its path.
type Projects interface {
	All() []*types.Project
	Get(path string) *types.Project
}

// NoProjects are the projects of a checkout no magus workspace holds. A read over them
// declares no target, so every changed file is one no target reads.
var NoProjects Projects = noProjects{}

type noProjects struct{}

func (noProjects) All() []*types.Project     { return nil }
func (noProjects) Get(string) *types.Project { return nil }

// Open resolves c's version control and its top level, then reads revision there.
func Open(ctx context.Context, c Checkout, revision string) types.UnverifiedReport {
	unknown := func(why string) types.UnverifiedReport {
		return types.UnverifiedReport{Revision: revision, Unknown: why}
	}
	res, err := vcs.Resolve(ctx, c.Dir, "", c.VCS)
	if err != nil || res.VCS == nil {
		return unknown(fmt.Sprintf("no version control answers in `%s`: %s", c.Dir, cmp.Or(errorMessage(err), "version control is disabled")))
	}
	root, err := res.VCS.Root(ctx, c.Dir)
	if err != nil {
		return unknown(fmt.Sprintf("the checkout root of `%s` is unread: %v", c.Dir, err))
	}
	// A worktree nested under the workspace's directory is a checkout of its own, with an
	// output store of its own.
	if rel, err := filepath.Rel(realPath(root), realPath(c.Workspace)); err != nil || !filepath.IsLocal(rel) {
		return unknown(fmt.Sprintf("`%s` is in the checkout `%s`, which does not hold the workspace `%s` whose runs this reads; read it from that checkout", c.Dir, root, c.Workspace))
	}
	return weigh(ctx, readInput{
		Driver: res.VCS, Root: root, Workspace: c.Workspace, Base: res.Base, Revision: revision,
		Projects: c.Projects,
		Runs: func() ([]cache.OutputDescriptor, error) {
			if c.CacheDir == "" {
				return []cache.OutputDescriptor{}, nil
			}
			return cache.NewOutputStore(c.CacheDir).ListDescriptors()
		},
	})
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Reader answers a read for the checkout holding dir. A guard rule has no workspace on its
// context, so the guard pins one for the rule's member to call.
type Reader func(ctx context.Context, dir, revision string) (types.UnverifiedReport, error)

type readerKey struct{}

// WithReader pins r on ctx.
func WithReader(ctx context.Context, r Reader) context.Context {
	return context.WithValue(ctx, readerKey{}, r)
}

// PinnedReader is the Reader pinned on ctx.
func PinnedReader(ctx context.Context) (Reader, bool) {
	r, ok := ctx.Value(readerKey{}).(Reader)
	return r, ok
}

// evidence matches the checkout's recorded runs to the files a commit changed.
type evidence struct {
	driver types.VCSDriver
	// root is the checkout's top level and workspace the workspace inside it, both with
	// symlinks resolved.
	root, workspace string
	ws              Projects
	runs            []cache.OutputDescriptor
	commit          string
	// differing is, per clean run's revision the repository holds, the files it holds
	// unlike commit. committed is commit's copy of each file a run with uncommitted edits
	// read, absent for a file commit lacks.
	differing map[string]map[string]bool
	committed map[string]committedFile
	// footprints is each run's, by its index in runs, and declared each magusfile
	// target's. Both name glob sets by index in globSets.
	footprints []runFootprint
	declared   []declaredTarget
	globSets   [][]types.Glob
	// claimed keeps claiming's answer for each file.
	claimed map[string][]bool
}

// committedFile is one file as commit holds it.
type committedFile struct {
	body    string
	present bool
}

func newEvidence(driver types.VCSDriver, root, workspace string, ws Projects, runs []cache.OutputDescriptor, commit string) *evidence {
	ev := &evidence{
		driver: driver, root: root, workspace: workspace, ws: ws, runs: runs, commit: commit,
		committed: map[string]committedFile{}, claimed: map[string][]bool{},
	}
	ev.index()
	return ev
}

// targetFootprint names the glob sets one target declares as its inputs and its outputs.
// declaresInputs is false for a target whose body names no input: its run reads only the
// paths its command names.
type targetFootprint struct {
	inputs, outputs int
	declaresInputs  bool
}

// runFootprint is one run's target footprint and the paths its args name. known is false
// when the run names no target of a project the workspace holds. scoped is set when the
// args name a path, existing or not: such a run read only named.
type runFootprint struct {
	targetFootprint
	known  bool
	scoped bool
	named  []string
}

// declaredTarget is a magusfile target wouldCover may name.
type declaredTarget struct {
	project, target string
	targetFootprint
}

// scope is the paths a run's args name and whether they name any.
type scope struct {
	named  []string
	scoped bool
}

// index roots each target's globs and reads each run's named paths once. Done per changed
// file, it spent a 1104-file push's whole deadline.
func (ev *evidence) index() {
	setIDs := map[string]int{}
	targets := map[[2]string]targetFootprint{}
	footprint := func(p *types.Project, target string) targetFootprint {
		key := [2]string{p.Path, target}
		fp, ok := targets[key]
		if !ok {
			inputs := rootedInputs(p, target)
			fp = targetFootprint{
				inputs:         ev.globSet(setIDs, inputs),
				outputs:        ev.globSet(setIDs, rootedOutputs(p, target)),
				declaresInputs: len(inputs) > 0,
			}
			targets[key] = fp
		}
		return fp
	}
	scopes := map[[2]string]scope{}
	ev.footprints = make([]runFootprint, len(ev.runs))
	for i, d := range ev.runs {
		p := ev.ws.Get(d.Project)
		if p == nil || d.Target == "" {
			continue
		}
		key := [2]string{d.Project, strings.Join(d.ExtraArgs, "\x00")}
		sc, ok := scopes[key]
		if !ok {
			sc = ev.namedPaths(d)
			scopes[key] = sc
		}
		ev.footprints[i] = runFootprint{
			targetFootprint: footprint(p, types.Normalize(bareTarget(d.Target))),
			known:           true, scoped: sc.scoped, named: sc.named,
		}
	}
	for _, p := range ev.ws.All() {
		for _, target := range p.MagusfileTargets {
			if target != string(types.TargetCI) {
				ev.declared = append(ev.declared, declaredTarget{project: p.Path, target: target, targetFootprint: footprint(p, target)})
			}
		}
	}
}

// globSet is the index in ev.globSets of a set matching as globs do, added when none does.
// Targets of one project often share its sources.
func (ev *evidence) globSet(ids map[string]int, globs []types.Glob) int {
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
func (ev *evidence) claiming(rel string) []bool {
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

// clean reports a run whose tree held no uncommitted edit at a known revision: its revision
// alone says what each file held when it ran.
func clean(d cache.OutputDescriptor) bool {
	return d.Revision != "" && !d.Dirty
}

// covered reports a passing run that read file as commit holds it. A run whose target
// outputs hold file and wrote it covers it too. file is repository-relative.
func (ev *evidence) covered(file string) bool {
	rel, inside := ev.workspacePath(file)
	if !inside {
		return false
	}
	claims := ev.claiming(rel)
	for i := range ev.runs {
		if ev.counts(i, claims, rel, file) {
			return true
		}
	}
	return false
}

// counts reports whether run i passed and read or wrote file as commit holds it.
func (ev *evidence) counts(i int, claims []bool, rel, file string) bool {
	d := ev.runs[i]
	if d.Failed {
		return false
	}
	read, wrote := ev.reads(i, claims, rel), ev.writes(i, claims)
	if !read && !wrote {
		return false
	}
	if clean(d) {
		// readRevisions answered every revision the repository holds.
		differing, held := ev.differing[d.Revision]
		return held && !differing[file]
	}
	return ev.unchangedSince(d, file, wrote && !read)
}

// unchangedSince reports whether the tree holds file as commit does and has not written it
// since run d started, or, for a run that only wrote it, since d finished: then the file
// was, when d read it, what commit holds. A deletion dates only its directory.
func (ev *evidence) unchangedSince(d cache.OutputDescriptor, file string, wrote bool) bool {
	want, asked := ev.committed[file]
	if !asked {
		return false
	}
	started := time.UnixMilli(d.TimestampMs - d.DurationMs)
	if wrote {
		started = time.UnixMilli(d.TimestampMs)
	}
	full := filepath.Join(ev.root, filepath.FromSlash(file))
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		dir, err := os.Lstat(filepath.Dir(full))
		return !want.present && err == nil && !dir.ModTime().After(started)
	}
	if err != nil || !want.present || info.ModTime().After(started) {
		return false
	}
	body, err := os.ReadFile(full)
	return err == nil && string(body) == want.body
}

// wouldCover names at most three runs that would cover file: targets this store ran over
// it, else magusfile targets whose declared inputs or outputs hold it. A target that
// declares no inputs is never named: which files it reads depends on the paths a command
// passes it, so none of its runs says file is its.
func (ev *evidence) wouldCover(file string) []string {
	rel, inside := ev.workspacePath(file)
	if !inside {
		return nil
	}
	claims := ev.claiming(rel)
	var out []string
	add := func(command string) {
		if len(out) < maxCommands && !slices.Contains(out, command) {
			out = append(out, command)
		}
	}
	for i, d := range ev.runs {
		if len(out) == maxCommands {
			return out
		}
		r := ev.footprints[i]
		target := bareTarget(d.Target)
		switch {
		case ev.writes(i, claims):
			add(hint.Run.With(target, d.Project))
		case !r.known || !r.declaresInputs || !claims[r.inputs]:
		case r.scoped:
			add(hint.Run.With(target, d.Project, "--", projectRelative(d.Project, rel)))
		default:
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

// claimingRuns lists the runs whose target claims a file of unverified, with the files of
// changed each claims and reads.
func (ev *evidence) claimingRuns(changed []string, unverified []types.UnverifiedFile) []types.RunEvidence {
	var relevant []int
	for i := range ev.runs {
		if !ev.footprints[i].known {
			continue
		}
		for _, f := range unverified {
			if rel, inside := ev.workspacePath(f.Path); inside && ev.claimsFile(i, ev.claiming(rel), rel) {
				relevant = append(relevant, i)
				break
			}
		}
	}
	if len(relevant) == 0 {
		return nil
	}
	out := make([]types.RunEvidence, 0, len(relevant))
	for _, i := range relevant {
		d := ev.runs[i]
		run := types.RunEvidence{
			Ref: d.Ref, Project: d.Project, Target: bareTarget(d.Target), Revision: d.Revision,
			Dirty: d.Dirty, Failed: d.Failed, Cached: d.Cached,
		}
		for _, file := range changed {
			rel, inside := ev.workspacePath(file)
			if !inside {
				continue
			}
			claims := ev.claiming(rel)
			if !ev.claimsFile(i, claims, rel) {
				continue
			}
			run.Claims = append(run.Claims, file)
			if ev.counts(i, claims, rel, file) {
				run.Reads = append(run.Reads, file)
			}
		}
		out = append(out, run)
	}
	return out
}

// claimsFile reports whether run i's target declares rel among its inputs or outputs, or,
// for a target that declares no inputs, whether the run's command named it. claims is
// claiming(rel).
func (ev *evidence) claimsFile(i int, claims []bool, rel string) bool {
	r := ev.footprints[i]
	return r.known && (claims[r.inputs] || claims[r.outputs] || !r.declaresInputs && r.names(rel))
}

// workspacePath is file, repository-relative, relative to the workspace.
func (ev *evidence) workspacePath(file string) (string, bool) {
	prefix, err := filepath.Rel(ev.root, ev.workspace)
	if err != nil {
		return "", false
	}
	if prefix = filepath.ToSlash(prefix); prefix == "." {
		return file, true
	}
	return strings.CutPrefix(file, prefix+"/")
}

// reads reports whether run i read rel: its target declares rel among its inputs and the
// run named no path or one holding rel, or its target declares no inputs and the run named
// a path holding rel. claims is claiming(rel).
func (ev *evidence) reads(i int, claims []bool, rel string) bool {
	r := ev.footprints[i]
	switch {
	case !r.known:
		return false
	case !r.declaresInputs:
		return r.names(rel)
	}
	return claims[r.inputs] && (!r.scoped || r.names(rel))
}

// names reports whether the run's args name rel or a directory holding it.
func (r runFootprint) names(rel string) bool {
	return slices.ContainsFunc(r.named, func(n string) bool { return n == "." || n == rel || strings.HasPrefix(rel, n+"/") })
}

// namedPaths are the workspace paths run d's forwarded args name, read against its
// project and then the workspace, spelled exactly as the tree spells them: on a disk that
// ignores case, `-run Docs` names no `docs/`. An arg shaped as a path (holding a slash or
// starting with a dot) scopes the run even when it names nothing that exists, so a typo
// does not widen the run to every file. `-run Push` names no path and scopes nothing.
func (ev *evidence) namedPaths(d cache.OutputDescriptor) scope {
	var sc scope
	for _, arg := range d.ExtraArgs {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		if strings.Contains(arg, "/") || strings.HasPrefix(arg, ".") {
			sc.scoped = true
		}
		for _, c := range []string{path.Join(d.Project, arg), path.Clean(arg)} {
			if filepath.IsLocal(filepath.FromSlash(c)) && spelledAsIs(ev.workspace, c) {
				sc.named = append(sc.named, c)
				sc.scoped = true
				break
			}
		}
	}
	return sc
}

// spelledAsIs reports whether rel, slash-separated, names an entry under root with every
// element spelled as the directory lists it.
func spelledAsIs(root, rel string) bool {
	if rel == "." {
		return true
	}
	dir := root
	for _, name := range strings.Split(rel, "/") {
		entries, err := os.ReadDir(dir)
		if err != nil || !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == name }) {
			return false
		}
		dir = filepath.Join(dir, name)
	}
	return true
}

// writes reports whether run i's target declares, among its outputs, the file claims is
// claiming of.
func (ev *evidence) writes(i int, claims []bool) bool {
	r := ev.footprints[i]
	return r.known && claims[r.outputs]
}

// readRevisions answers what counts compares: which files each clean run's revision holds
// unlike commit, and commit's copy of each file a run with uncommitted edits read. A
// process per file and revision spent a 1104-file push's deadline.
func (ev *evidence) readRevisions(ctx context.Context, files []string) error {
	compared := map[string][]string{}
	asked := map[[2]string]bool{}
	var edited []string
	for _, file := range files {
		rel, inside := ev.workspacePath(file)
		if !inside {
			continue
		}
		claims := ev.claiming(rel)
		read := false
		for i, d := range ev.runs {
			if d.Failed || !ev.reads(i, claims, rel) && !ev.writes(i, claims) {
				continue
			}
			if !clean(d) {
				read = true
				continue
			}
			if key := [2]string{d.Revision, file}; !asked[key] {
				asked[key] = true
				compared[d.Revision] = append(compared[d.Revision], file)
			}
		}
		if read {
			edited = append(edited, file)
		}
	}
	differing, err := vcs.DifferingPathsByRevision(ctx, ev.driver, ev.root, ev.commit, compared)
	if err != nil {
		return err
	}
	ev.differing = make(map[string]map[string]bool, len(differing))
	for rev, paths := range differing {
		ev.differing[rev] = make(map[string]bool, len(paths))
		for _, p := range paths {
			ev.differing[rev][p] = true
		}
	}
	return ev.readCommitFiles(ctx, edited)
}

// readCommitFiles reads commit's copy of each of files, through one batch where the driver
// opens one.
func (ev *evidence) readCommitFiles(ctx context.Context, files []string) error {
	if len(files) == 0 {
		return nil
	}
	opener, ok := ev.driver.(vcs.ObjectBatchOpener)
	if !ok {
		for _, file := range files {
			body, err := ev.driver.ReadFileAt(ctx, ev.root, ev.commit, file)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// ReadFileAt errs on a path the revision lacks.
			ev.committed[file] = committedFile{body: body, present: err == nil}
		}
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
			ev.committed[file] = committedFile{}
		case err != nil:
			return errors.Join(fmt.Errorf("read %s at %s: %w", file, ev.commit, err), batch.Close())
		default:
			ev.committed[file] = committedFile{body: body, present: true}
		}
	}
	return batch.Close()
}

// rootedInputs is the inputs target's body declares in p, rooted at the workspace, none
// for a target that declares none. internal/guard's copy falls back to the project's
// sources, which every target's cache key holds; read as what a run read, that fallback
// let a clean build verify every file of the project.
func rootedInputs(p *types.Project, target string) []types.Glob {
	refs := p.TargetInputs[target]
	out := make([]types.Glob, len(refs))
	for i, r := range refs {
		out[i] = r.Rooted(p.Path)
	}
	return out
}

// rootedOutputs is target's own declared outputs in p, rooted at the workspace. Project
// outputs do not count: any clean run would then cover them.
func rootedOutputs(p *types.Project, target string) []types.Glob {
	refs := p.TargetOutputs[target]
	out := make([]types.Glob, len(refs))
	for i, r := range refs {
		out[i] = r.Rooted(p.Path)
	}
	return out
}

// bareTarget strips the charms a stored run's target carries (`lint:rw`).
func bareTarget(stored string) string {
	name, _, _ := strings.Cut(stored, ":")
	return name
}

// projectRelative is rel, workspace-relative, as project's args read it.
func projectRelative(project, rel string) string {
	if project == "." {
		return rel
	}
	return strings.TrimPrefix(rel, project+"/")
}

// realPath resolves symlinks through the nearest existing ancestor, so a directory that is
// gone still compares equal to its registration.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}
