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
	ev := runEvidence{
		driver: res.VCS, root: realPath(root), workspace: realPath(at.workspace), ws: ws, runs: runs, commit: commit,
		files: map[string]fileAt{}, revisions: map[string]bool{},
	}
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
	files           map[string]fileAt
	revisions       map[string]bool
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
	for _, d := range ev.runs {
		if d.Failed {
			continue
		}
		if ev.reads(d, rel, true) && ev.heldAsCommit(ctx, file, d, false) || ev.writes(d, rel) && ev.heldAsCommit(ctx, file, d, true) {
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
	var out []string
	add := func(command string) {
		if len(out) < 3 && !slices.Contains(out, command) {
			out = append(out, command)
		}
	}
	for _, d := range ev.runs {
		if ev.writes(d, rel) {
			add(hint.Run.With(bareTarget(d.Target), d.Project))
			continue
		}
		if !ev.reads(d, rel, false) {
			continue
		}
		target := bareTarget(d.Target)
		if len(ev.namedPaths(d)) > 0 {
			add(hint.Run.With(target, d.Project, "--", projectRelative(d.Project, rel)))
		} else {
			add(hint.Run.With(target, d.Project))
		}
	}
	for _, p := range ev.ws.All() {
		for _, target := range p.MagusfileTargets {
			if target != string(types.TargetCI) && (footprintHolds(p, target, rel) || outputsHold(p, target, rel)) {
				add(hint.Run.With(target, p.Path))
			}
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

// reads reports whether run d's target declares rel among its inputs. scoped
// also holds a file-scoped run to the paths it named.
func (ev *runEvidence) reads(d cache.OutputDescriptor, rel string, scoped bool) bool {
	p := ev.ws.Get(d.Project)
	if p == nil || d.Target == "" || !footprintHolds(p, types.Normalize(bareTarget(d.Target)), rel) {
		return false
	}
	named := ev.namedPaths(d)
	if !scoped || len(named) == 0 {
		return true
	}
	return slices.ContainsFunc(named, func(n string) bool { return n == rel || strings.HasPrefix(rel, n+"/") })
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

// writes reports whether run d's target declares rel among its outputs.
func (ev *runEvidence) writes(d cache.OutputDescriptor, rel string) bool {
	p := ev.ws.Get(d.Project)
	return p != nil && d.Target != "" && outputsHold(p, types.Normalize(bareTarget(d.Target)), rel)
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
	key := fileKey(rev, file)
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

func fileKey(rev, file string) string { return fmt.Sprintf("%s\x00%s", rev, file) }

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
	seen := map[string]bool{}
	add := func(rev, file string) {
		key := fileKey(rev, file)
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
		for _, d := range ev.runs {
			if d.Failed || !ev.reads(d, rel, true) && !ev.writes(d, rel) {
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
		ev.files[fileKey(w.rev, w.path)] = got[i]
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
