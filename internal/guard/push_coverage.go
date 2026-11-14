package guard

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
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
	if res.VCS.Name() == string(backendGit) {
		if err := ev.readGitFiles(ctx, weigh); err != nil {
			return narrowCoverage{unread: fmt.Sprintf("the changed files are unread: %v", err)}
		}
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
	// prints is each run's footprint, by its index in runs, and declared
	// each magusfile target's. Both name glob sets by index in sets.
	prints   []runFootprint
	declared []declaredTarget
	sets     [][]types.Glob
	// held memoizes holding.
	held map[string][]bool
}

// newRunEvidence weighs runs, recorded in the checkout at root, against what
// commit holds. workspace is the workspace inside root.
func newRunEvidence(driver types.VCSDriver, root, workspace string, ws types.WorkspaceRepository, runs []cache.OutputDescriptor, commit string) *runEvidence {
	ev := &runEvidence{
		driver: driver, root: root, workspace: workspace, ws: ws, runs: runs, commit: commit,
		files: map[revisionFile]fileAt{}, revisions: map[string]bool{}, held: map[string][]bool{},
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
	ev.prints = make([]runFootprint, len(ev.runs))
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
		ev.prints[i] = runFootprint{targetFootprint: footprint(p, types.Normalize(bareTarget(d.Target))), known: true, named: paths}
	}
	for _, p := range ev.ws.All() {
		for _, target := range p.MagusfileTargets {
			if target != string(types.TargetCI) {
				ev.declared = append(ev.declared, declaredTarget{project: p.Path, target: target, targetFootprint: footprint(p, target)})
			}
		}
	}
}

// globSet is the index in ev.sets of a set matching as globs do, added when
// none does. Targets of one project often share its sources.
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
		id = len(ev.sets)
		ev.sets = append(ev.sets, globs)
		ids[key.String()] = id
	}
	return id
}

// holding reports, for each of ev.sets, whether it claims rel.
func (ev *runEvidence) holding(rel string) []bool {
	if got, ok := ev.held[rel]; ok {
		return got
	}
	got := make([]bool, len(ev.sets))
	for i, set := range ev.sets {
		got[i] = slices.ContainsFunc(set, func(g types.Glob) bool { return g.Match(rel) })
	}
	ev.held[rel] = got
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
	held := ev.holding(rel)
	for i, d := range ev.runs {
		if d.Failed {
			continue
		}
		if ev.reads(i, held, rel, true) && ev.heldAsCommit(ctx, file, d, false) || ev.writes(i, held) && ev.heldAsCommit(ctx, file, d, true) {
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
	held := ev.holding(rel)
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
		if ev.writes(i, held) {
			add(hint.Run.With(bareTarget(d.Target), d.Project))
			continue
		}
		if !ev.reads(i, held, rel, false) {
			continue
		}
		target := bareTarget(d.Target)
		if len(ev.prints[i].named) > 0 {
			add(hint.Run.With(target, d.Project, "--", projectRelative(d.Project, rel)))
		} else {
			add(hint.Run.With(target, d.Project))
		}
	}
	for _, t := range ev.declared {
		if held[t.inputs] || held[t.outputs] {
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

// reads reports whether run i's target declares rel among its inputs. held
// is holding(rel). scoped also holds a file-scoped run to the paths it named.
func (ev *runEvidence) reads(i int, held []bool, rel string, scoped bool) bool {
	r := ev.prints[i]
	if !r.known || !held[r.inputs] {
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
// file held is holding of.
func (ev *runEvidence) writes(i int, held []bool) bool {
	r := ev.prints[i]
	return r.known && held[r.outputs]
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
		return ev.at(ctx, d.Revision, file) == want
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

// readGitFiles reads every file a run over files would be compared on, in one
// `git cat-file --batch`. A process per file spent a branch of seven hundred
// changed files past the guard's deadline.
func (ev *runEvidence) readGitFiles(ctx context.Context, files []string) error {
	var want []revisionFile
	seen := map[revisionFile]bool{}
	add := func(rev, file string) {
		key := revisionFile{rev: rev, path: file}
		// cat-file reads one object name per line.
		if seen[key] || strings.ContainsAny(rev+file, "\n\r") || !ev.known(ctx, rev) {
			return
		}
		seen[key] = true
		want = append(want, revisionFile{rev: rev, path: file})
	}
	for _, file := range files {
		rel, inside := ev.workspacePath(file)
		if !inside {
			continue
		}
		held := ev.holding(rel)
		for i, d := range ev.runs {
			if d.Failed || !ev.reads(i, held, rel, true) && !ev.writes(i, held) {
				continue
			}
			add(ev.commit, file)
			if d.Revision != "" && !d.Dirty {
				add(d.Revision, file)
			}
		}
	}
	if len(want) == 0 {
		return nil
	}
	got, err := gitFilesAt(ctx, ev.root, want)
	if err != nil {
		return err
	}
	for i, w := range want {
		ev.files[w] = got[i]
	}
	return nil
}

// revisionFile is one repository-relative path at one revision.
type revisionFile struct {
	rev, path string
}

// gitFilesAt reads each of want in the checkout at root, in order, in one
// process. A path its revision lacks is known and not present.
func gitFilesAt(ctx context.Context, root string, want []revisionFile) ([]fileAt, error) {
	var in strings.Builder
	for _, w := range want {
		fmt.Fprintf(&in, "%s:%s\n", w.rev, w.path)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "--batch")
	cmd.Env = append(os.Environ(), "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0")
	cmd.Stdin = strings.NewReader(in.String())
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	r := bufio.NewReader(bytes.NewReader(out))
	got := make([]fileAt, len(want))
	for i, w := range want {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("git cat-file: no answer for %s:%s", w.rev, w.path)
		}
		header = strings.TrimSuffix(header, "\n")
		if strings.HasSuffix(header, " missing") || strings.HasSuffix(header, " ambiguous") {
			got[i] = fileAt{known: true}
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("git cat-file: unread header %q for %s:%s", header, w.rev, w.path)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("git cat-file: unread size in %q: %w", header, err)
		}
		// The body is followed by one newline.
		body := make([]byte, size+1)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, fmt.Errorf("git cat-file: short body for %s:%s: %w", w.rev, w.path, err)
		}
		got[i] = fileAt{body: string(body[:size]), present: true, known: true}
	}
	return got, nil
}
