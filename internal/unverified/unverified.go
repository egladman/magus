// Package unverified reads which files a commit changed that no clean passing run in a
// checkout read.
//
// A run counts for a file when it passed, its tree was clean, its target's declared inputs
// claim the file (or its declared outputs do and it wrote it), and its revision holds the
// file as the commit does. A cache hit records a new attempt over the current revision with
// Cached set, and counts as evidence for the target's declared inputs. Evidence is per
// checkout, the store under <root>/.magus/outputs: a second worktree sees no runs until the
// targets run there, which are cache hits. A clean attempt can be evicted by five later
// attempts of the same key.
package unverified

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// maxCommands is how many `magus run` lines a file names.
const maxCommands = 3

// Input is one read: a checkout and the revision to weigh in it.
type Input struct {
	Driver types.VCSDriver
	// Root is the checkout's top level and Workspace the workspace root inside it.
	Root, Workspace string
	// Base is the ref the merge base is taken with, Revision the full id read.
	Base, Revision string
	// Projects opens the workspace, whose targets' declared inputs and outputs say what a
	// run read. Runs lists the descriptors of the checkout's output store. Both are
	// called once, in the order the report's unknown names them.
	Projects func(context.Context) (types.WorkspaceRepository, error)
	Runs     func() ([]cache.OutputDescriptor, error)
}

// Read weighs the files Revision changed since its merge base with Base against the runs
// of its checkout. It runs under ctx's deadline and, on the first step that fails or meets
// an ended ctx, answers Unknown naming that step, with Changed and Unverified empty.
func Read(ctx context.Context, in Input) types.UnverifiedReport {
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
	// store's directory.
	Dir, Workspace, CacheDir string
	VCS                      types.VCSOptions
	// Projects opens the workspace, as Input.Projects.
	Projects func(context.Context) (types.WorkspaceRepository, error)
}

// Open resolves c's version control and its top level, then reads revision there.
func Open(ctx context.Context, c Checkout, revision string) types.UnverifiedReport {
	unknown := func(why string) types.UnverifiedReport {
		return types.UnverifiedReport{Revision: revision, Unknown: why}
	}
	res, err := vcs.Resolve(ctx, c.Dir, "", c.VCS)
	if err != nil || res.VCS == nil {
		return unknown(fmt.Sprintf("no version control answers in `%s`: %s", c.Dir, cmp.Or(errText(err), "version control is disabled")))
	}
	root, err := res.VCS.Root(ctx, c.Dir)
	if err != nil {
		return unknown(fmt.Sprintf("the checkout root of `%s` is unread: %v", c.Dir, err))
	}
	return Read(ctx, Input{
		Driver: res.VCS, Root: root, Workspace: c.Workspace, Base: res.Base, Revision: revision,
		Projects: c.Projects,
		Runs:     func() ([]cache.OutputDescriptor, error) { return cache.NewOutputStore(c.CacheDir).ListDescriptors() },
	})
}

func errText(err error) string {
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

// ReaderFrom is the Reader pinned on ctx.
func ReaderFrom(ctx context.Context) (Reader, bool) {
	r, ok := ctx.Value(readerKey{}).(Reader)
	return r, ok
}

// evidence matches the checkout's recorded runs to the files a commit changed.
type evidence struct {
	driver types.VCSDriver
	// root is the checkout's top level and workspace the workspace inside it, both with
	// symlinks resolved.
	root, workspace string
	ws              types.WorkspaceRepository
	runs            []cache.OutputDescriptor
	commit          string
	// differing is, per clean run's revision the repository holds, the files it holds
	// unlike commit.
	differing map[string]map[string]bool
	// footprints is each run's, by its index in runs, and declared each magusfile
	// target's. Both name glob sets by index in globSets.
	footprints []runFootprint
	declared   []declaredTarget
	globSets   [][]types.Glob
	// claimed keeps claiming's answer for each file.
	claimed map[string][]bool
}

func newEvidence(driver types.VCSDriver, root, workspace string, ws types.WorkspaceRepository, runs []cache.OutputDescriptor, commit string) *evidence {
	ev := &evidence{
		driver: driver, root: root, workspace: workspace, ws: ws, runs: runs, commit: commit,
		claimed: map[string][]bool{},
	}
	ev.index()
	return ev
}

// targetFootprint names the glob sets one target declares as its inputs and its outputs.
type targetFootprint struct {
	inputs, outputs int
}

// runFootprint is one run's target footprint and the paths its args name. known is false
// when the run names no target of a project the workspace holds.
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

// index roots each target's globs and reads each run's named paths once. Done per changed
// file, it spent a 1104-file push's whole deadline.
func (ev *evidence) index() {
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

// clean reports a run that can count: it passed and its tree was clean at a known revision.
func clean(d cache.OutputDescriptor) bool {
	return !d.Failed && d.Revision != "" && !d.Dirty
}

// covered reports a clean passing run that read file as commit holds it. A run whose
// target outputs hold file and wrote it covers it too. file is repository-relative.
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

// counts reports whether run i read file, as commit holds it.
func (ev *evidence) counts(i int, claims []bool, rel, file string) bool {
	d := ev.runs[i]
	if !clean(d) || !ev.reads(i, claims, rel, true) && !ev.writes(i, claims) {
		return false
	}
	// readRevisions answered every revision the repository holds.
	differing, held := ev.differing[d.Revision]
	return held && !differing[file]
}

// wouldCover names at most three runs that would cover file: targets this store ran over
// it, else magusfile targets whose inputs hold it.
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

// claimingRuns lists the runs whose target claims a file of unverified, with the files of
// changed each claims and reads.
func (ev *evidence) claimingRuns(changed []string, unverified []types.UnverifiedFile) []types.RunEvidence {
	var relevant []int
	for i := range ev.runs {
		if !ev.footprints[i].known {
			continue
		}
		for _, f := range unverified {
			if rel, inside := ev.workspacePath(f.Path); inside && ev.claimsFile(i, ev.claiming(rel)) {
				relevant = append(relevant, i)
				break
			}
		}
	}
	var out []types.RunEvidence
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
			if !ev.claimsFile(i, claims) {
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

// claimsFile reports whether run i's target declares the file among its inputs or outputs.
// claims is claiming of that file.
func (ev *evidence) claimsFile(i int, claims []bool) bool {
	r := ev.footprints[i]
	return r.known && (claims[r.inputs] || claims[r.outputs])
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

// reads reports whether run i's target declares rel among its inputs. claims is
// claiming(rel). scoped also holds a file-scoped run to the paths it named.
func (ev *evidence) reads(i int, claims []bool, rel string, scoped bool) bool {
	r := ev.footprints[i]
	if !r.known || !claims[r.inputs] {
		return false
	}
	if !scoped || len(r.named) == 0 {
		return true
	}
	return slices.ContainsFunc(r.named, func(n string) bool { return n == rel || strings.HasPrefix(rel, n+"/") })
}

// namedPaths are the workspace paths run d's forwarded args name, read against its
// project and then the workspace. None means the run is not file-scoped: `-run Push` names
// no path.
func (ev *evidence) namedPaths(d cache.OutputDescriptor) []string {
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

// writes reports whether run i's target declares, among its outputs, the file claims is
// claiming of.
func (ev *evidence) writes(i int, claims []bool) bool {
	r := ev.footprints[i]
	return r.known && claims[r.outputs]
}

// readRevisions answers what counts compares: which files each clean run's revision holds
// unlike commit. A process per file and revision spent a 1104-file push's deadline.
func (ev *evidence) readRevisions(ctx context.Context, files []string) error {
	compared := map[string][]string{}
	asked := map[[2]string]bool{}
	for _, file := range files {
		rel, inside := ev.workspacePath(file)
		if !inside {
			continue
		}
		claims := ev.claiming(rel)
		for i, d := range ev.runs {
			if !clean(d) || !ev.reads(i, claims, rel, true) && !ev.writes(i, claims) {
				continue
			}
			if key := [2]string{d.Revision, file}; !asked[key] {
				asked[key] = true
				compared[d.Revision] = append(compared[d.Revision], file)
			}
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
	return nil
}

// rootedInputs is target's source footprint in p, rooted at the workspace. It copies
// internal/guard's, which the built-in push rule still reads.
func rootedInputs(p *types.Project, target string) []types.Glob {
	if refs := p.TargetInputs[target]; len(refs) > 0 {
		out := make([]types.Glob, len(refs))
		for i, r := range refs {
			out[i] = r.Rooted(p.Path)
		}
		return out
	}
	out := make([]types.Glob, len(p.Sources))
	for i, g := range p.Sources {
		out[i] = g.Root(p.Path)
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
