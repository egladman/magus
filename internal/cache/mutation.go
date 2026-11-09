package cache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// sourceFingerprint maps a source file's workspace-relative path to its content hash.
// Content, not mtime: a tool that rewrites identical bytes changed nothing a key can
// see, and reporting it would train readers to ignore MGS4007.
type sourceFingerprint map[string]string

// fingerprintSources returns the per-file view hashStep computes and discards. memo
// stays nil: this runs on a path that EXECUTES a target, where a memoized hash is
// precisely the stale answer.
func (c *Cache) fingerprintSources(ctx context.Context, s *Step) (sourceFingerprint, error) {
	files, hashes, _, err := c.expandAndHashSources(ctx, s, nil)
	if err != nil {
		return nil, err
	}
	fp := make(sourceFingerprint, len(files))
	for i, f := range files {
		fp[f.rel] = hashes[i]
	}
	return fp, nil
}

// mutatedSources returns the changed source files no declared glob claims, sorted.
//
// A source that DISAPPEARED counts: deleting an input is a write, and the key then
// describes a file that is not there. One that appeared does not: a target may
// produce a file a broad source glob would have matched, which is MGS1028's question.
func mutatedSources(before, after sourceFingerprint, updates, ownedOutputs []types.Glob) []string {
	declared := compileGlobs(slices.Concat(updates, ownedOutputs))
	claimed := func(rel string) bool {
		for _, g := range declared {
			if g.Match(rel) {
				return true
			}
		}
		return false
	}

	var out []string
	for rel, was := range before {
		now, still := after[rel]
		if still && now == was {
			continue
		}
		if claimed(rel) {
			continue
		}
		out = append(out, rel)
	}
	slices.Sort(out)
	return out
}

// movedInputs names every hashed input that changed while the step ran, whoever changed it,
// declared updates included.
//
// It must NOT share the exemptions of [mutatedSources], which answers "did this target
// misbehave" and so ignores every declared update and output. This answers "does the key
// still describe the tree it was computed from", where a peer rewriting a generated file
// mid-hash is the whole danger. A declared update counts too: updates are never snapshotted
// or replayed, so an entry filed under the pre-run bytes would, once the file returned to
// them, hit and leave it as the run would not. Only a fixed point records, so a formatter
// caches from its first run that changes nothing and an updater that rewrites its file on
// every run never caches.
func movedInputs(before, after sourceFingerprint) []string {
	return mutatedSources(before, after, nil, nil)
}

// checkSourceMutation reports MGS4007 when a target rewrote its own declared sources
// without declaring them via ctx.writesFiles with editsExistingSource. Callers invoke it only after
// fn succeeded, so it never stacks on top of a target's own failure.
func checkSourceMutation(s *Step, before, after sourceFingerprint) error {
	// A missing fingerprint skips the check; runMiss reports it.
	if before == nil || after == nil {
		return nil
	}
	changed := mutatedSources(before, after, s.Updates, s.OwnedOutputs)
	if len(changed) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.UndeclaredSourceModified,
		"%s:%s modified its declared sources %s; declare them with ctx.writesFiles([...], editsExistingSource: true)",
		s.ProjectPath, s.Target, joinCapped(changed, 5)).
		WithWhy("A step that rewrites its own declared sources changes its cache key as it runs; declare them, or stop writing them.")
}

// fileStamp is a file's mtime and size. Unlike MGS4007 this compares no
// content: rewriting identical bytes is still a write nothing declared.
type fileStamp struct {
	mtimeNs int64
	size    int64
}

// treeStamps maps a workspace-relative path to its stamp.
type treeStamps map[string]fileStamp

// stampProjectTree stamps every regular file under s's project directory, less
// nested projects, declared tool caches, pruned dirs and nested checkouts.
func stampProjectTree(s *Step) (treeStamps, error) {
	root := filepath.Join(s.WorkspaceRoot, filepath.FromSlash(s.ProjectPath))
	nested := make(map[string]bool, len(s.NestedDirs))
	for _, d := range s.NestedDirs {
		nested[d] = true
	}
	declared := make(map[string]bool, len(s.WritePaths))
	for _, d := range s.WritePaths {
		declared[filepath.Clean(d)] = true
		// The root may be resolved where the env's path is not (/var on macOS).
		if real, err := filepath.EvalSymlinks(d); err == nil {
			declared[real] = true
		}
	}
	out := make(treeStamps, 256)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			// A peer deleting its temp files mid-walk is routine.
			if path != root && errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		rel, err := filepath.Rel(s.WorkspaceRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path != root && (isIgnoreDir(d.Name(), s.IgnoreDirs) || nested[rel] || declared[path] || isCheckout(path)) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || declared[path] {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			out[rel] = fileStamp{mtimeNs: fi.ModTime().UnixNano(), size: fi.Size()}
		}
		return nil
	})
	return out, err
}

// isCheckout reports a directory holding its own VCS checkout, such as a
// worktree kept inside the tree: its files belong to another tree.
func isCheckout(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// undeclaredWrites returns the created and the modified files no declaration
// claims, each sorted. A deletion is not a write here. A source that already
// existed is left to MGS4007, which compares content.
func undeclaredWrites(before, after treeStamps, sources sourceFingerprint, s *Step) (created, modified []string) {
	claims := compileGlobs(slices.Concat(s.Outputs, s.OwnedOutputs, s.Updates))
	for rel, now := range after {
		was, existed := before[rel]
		if existed && was == now {
			continue
		}
		if _, source := sources[rel]; existed && source {
			continue
		}
		if matchesAny(claims, rel) || slices.Contains(s.Stamps, rel) {
			continue
		}
		if existed {
			modified = append(modified, rel)
		} else {
			created = append(created, rel)
		}
	}
	slices.Sort(created)
	slices.Sort(modified)
	return created, modified
}

// untrackedPaths returns the paths in rels the workspace's VCS does not track.
// A tracked file a person saves mid-run must never fail it, and a target
// rewriting one is MGS4007's. ok is false when the VCS cannot answer.
func untrackedPaths(ctx context.Context, root string, rels []string) (untracked []string, ok bool) {
	if len(rels) == 0 {
		return nil, true
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return nil, false
	}
	tracked, err := res.VCS.TrackedFiles(ctx, root, rels)
	if err != nil {
		return nil, false
	}
	return slices.DeleteFunc(slices.Clone(rels), func(rel string) bool {
		return slices.Contains(tracked, rel)
	}), true
}

// isOwnStdio reports a file this process's stdout or stderr writes to, as
// in `magus run test > test.log` run inside the project.
func isOwnStdio(abs string) bool {
	fi, err := os.Stat(abs)
	if err != nil {
		return false
	}
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		if std, err := f.Stat(); err == nil && os.SameFile(fi, std) {
			return true
		}
	}
	return false
}

func matchesAny(globs []compiledGlob, rel string) bool {
	return slices.ContainsFunc(globs, func(g compiledGlob) bool { return g.Match(rel) })
}

// writeSlack widens a peer's window when matching an mtime against it, since
// filesystems stamp mtimes from a coarse clock.
const writeSlack = 50 * time.Millisecond

// writeWindow is one target run's interval. end is zero while it runs.
type writeWindow struct {
	project string
	label   string
	start   time.Time
	end     time.Time
	claims  []compiledGlob
	stamps  []string
	// parent is the run whose body started this one through ctx.needs.
	parent *writeWindow
}

type writeWindowKey struct{}

func withWriteWindow(ctx context.Context, win *writeWindow) context.Context {
	return context.WithValue(ctx, writeWindowKey{}, win)
}

func enclosingWriteWindow(ctx context.Context) *writeWindow {
	win, _ := ctx.Value(writeWindowKey{}).(*writeWindow)
	return win
}

// encloses reports whether inner ran inside outer's body.
func (outer *writeWindow) encloses(inner *writeWindow) bool {
	for w := inner.parent; w != nil; w = w.parent {
		if w == outer {
			return true
		}
	}
	return false
}

// writeWindows records one Cache's runs. A write made while a related peer
// ran is not blamed on the run that found it. Another magus process cannot
// run the same project at once: it waits on the project lock.
type writeWindows struct {
	mu   sync.Mutex
	runs []*writeWindow
	// released marks runs whose own check is done.
	released map[*writeWindow]bool
}

func (w *writeWindows) open(s *Step, parent *writeWindow) *writeWindow {
	win := &writeWindow{
		project: s.ProjectPath,
		label:   fmt.Sprintf("%s:%s", s.ProjectPath, s.Target),
		start:   time.Now(),
		claims:  compileGlobs(slices.Concat(s.Outputs, s.Updates)),
		stamps:  s.Stamps,
		parent:  parent,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.runs = append(w.runs, win)
	return win
}

func (w *writeWindows) finish(win *writeWindow) {
	w.mu.Lock()
	defer w.mu.Unlock()
	win.end = time.Now()
}

// release drops every finished run that no unreleased run overlaps, since no
// check still to come can ask about it.
func (w *writeWindows) release(win *writeWindow) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if win.end.IsZero() {
		win.end = time.Now()
	}
	if w.released == nil {
		w.released = make(map[*writeWindow]bool)
	}
	w.released[win] = true
	var earliest time.Time
	for _, r := range w.runs {
		if !w.released[r] && (earliest.IsZero() || r.start.Before(earliest)) {
			earliest = r.start
		}
	}
	w.runs = slices.DeleteFunc(w.runs, func(r *writeWindow) bool {
		gone := w.released[r] && (earliest.IsZero() || r.end.Add(writeSlack).Before(earliest))
		if gone {
			delete(w.released, r)
		}
		return gone
	})
}

// peersAt answers who else may have written rel at t. claimed is a running
// related peer declaring rel; suspects are the related peers running at t that
// do not.
//
// A run enclosing win, or enclosed by it, is never a suspect. A chained
// target's undeclared write is its own, and its own check reports it.
func (w *writeWindows) peersAt(win *writeWindow, rel string, t time.Time) (claimed bool, suspects []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, p := range w.runs {
		if p == win || !relatedProjects(p.project, win.project) {
			continue
		}
		if t.Before(p.start.Add(-writeSlack)) || (!p.end.IsZero() && t.After(p.end.Add(writeSlack))) {
			continue
		}
		if matchesAny(p.claims, rel) || slices.Contains(p.stamps, rel) {
			return true, nil
		}
		if p.encloses(win) || win.encloses(p) {
			continue
		}
		suspects = append(suspects, p.label)
	}
	slices.Sort(suspects)
	return false, slices.Compact(suspects)
}

// relatedProjects reports two projects whose directories may hold each
// other's writes: the same one, or one nested under the other.
func relatedProjects(a, b string) bool {
	under := func(child, parent string) bool {
		return parent == "." || strings.HasPrefix(child, parent+"/")
	}
	return a == b || under(a, b) || under(b, a)
}

// checkUndeclaredWrites reports MGS4011 when the run wrote a file in its
// project that nothing declares. Callers invoke it only after fn succeeded.
// A write made while a related peer ran is logged, not blamed.
func (c *Cache) checkUndeclaredWrites(ctx context.Context, s *Step, win *writeWindow, before treeStamps, sources sourceFingerprint) error {
	if before == nil {
		return nil
	}
	after, err := stampProjectTree(s)
	if err != nil {
		c.log.DebugContext(ctx, "cache.debug", slog.String("msg",
			fmt.Sprintf("undeclared write check skipped for %s: %v", s.ProjectPath, err)))
		return nil
	}
	created, modified := undeclaredWrites(before, after, sources, s)
	untracked, ok := untrackedPaths(ctx, s.WorkspaceRoot, modified)
	if !ok {
		c.log.WarnContext(ctx, types.FormatDiagnostic(types.UndeclaredOutputWritten, fmt.Sprintf(
			"%s:%s: the VCS could not say which of %s it modified are tracked, so they are not checked: %s",
			s.ProjectPath, s.Target, pluralFiles(len(modified)), joinCapped(modified, 5))))
	}
	var blamed, unattributed, suspects []string
	for _, rel := range slices.Concat(created, untracked) {
		if isOwnStdio(filepath.Join(s.WorkspaceRoot, filepath.FromSlash(rel))) {
			continue
		}
		claimed, who := c.writers.peersAt(win, rel, time.Unix(0, after[rel].mtimeNs))
		switch {
		case claimed:
		case len(who) > 0:
			unattributed = append(unattributed, rel)
			suspects = append(suspects, who...)
		default:
			blamed = append(blamed, rel)
		}
	}
	if len(unattributed) > 0 {
		slices.Sort(suspects)
		c.log.WarnContext(ctx, types.FormatDiagnostic(types.UndeclaredOutputWritten, fmt.Sprintf(
			"%s:%s: not attributing %s no target declares, since %s also ran as they were written: %s",
			s.ProjectPath, s.Target, pluralFiles(len(unattributed)),
			joinComma(slices.Compact(suspects)), joinCapped(unattributed, 5))))
	}
	if len(blamed) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.UndeclaredOutputWritten,
		"%s:%s wrote %s it does not declare: %s; declare them with ctx.writesFiles([...]), or write them outside the tree",
		s.ProjectPath, s.Target, pluralFiles(len(blamed)), joinCapped(blamed, 5)).
		WithWhy("A file no target declares is never cached, replayed or cleaned, and a target with no outputs must be a check.")
}

// keyStillDescribesInputs reports whether this step's hashed inputs are unchanged, and names
// what moved when they are not.
//
// Callers REFUSE THE ENTRY, never the run: the work succeeded and its output is good, and
// only filing it under this key is unsafe, since a later run that really does hash to it
// would replay bytes built from different sources. Failing instead would punish the target
// that was read rather than the one that wrote.
//
// Neither side may be nil: runMiss makes the entry unstorable instead.
func keyStillDescribesInputs(before, after sourceFingerprint) ([]string, bool) {
	moved := movedInputs(before, after)
	return moved, len(moved) == 0
}

// reportUnrecorded says why a run was not recorded. A moved declared update is the step
// doing its job short of a fixed point, which every run that changes a file does, so alone
// it is logged at debug; any other moved input gets [movedInputsNotice].
func (c *Cache) reportUnrecorded(ctx context.Context, s Step, hash string, moved []string) {
	updates := compileGlobs(s.Updates)
	others := slices.DeleteFunc(slices.Clone(moved), func(rel string) bool {
		return slices.ContainsFunc(updates, func(g compiledGlob) bool { return g.Match(rel) })
	})
	if len(others) > 0 {
		c.log.WarnContext(ctx, movedInputsNotice(s, hash, others))
		return
	}
	c.log.DebugContext(ctx, "cache.debug", slog.String("msg", fmt.Sprintf(
		"not recording %s:%s under %s: it rewrote %s it declares as updates, and a hit leaves them as they are: %s",
		s.ProjectPath, s.Target, shortHash(hash), pluralFiles(len(moved)), joinCapped(moved, 5))))
}

// movedInputsNotice explains why the run was not cached. A moved input matching a declared
// output was rewritten by this step's own chain after the key was taken, so the step never
// caches (MGS4010); any other moved input came from outside and the next run caches.
func movedInputsNotice(s Step, hash string, moved []string) string {
	declared := compileGlobs(s.OwnedOutputs)
	var generated []string
	for _, rel := range moved {
		for _, g := range declared {
			if g.Match(rel) {
				generated = append(generated, rel)
				break
			}
		}
	}
	if len(generated) == 0 {
		return fmt.Sprintf("magus/cache: not recording %s:%s under %s: %s changed while it ran, so the key no longer describes its inputs: %s",
			s.ProjectPath, s.Target, shortHash(hash), pluralFiles(len(moved)), joinCapped(moved, 5))
	}
	return types.FormatDiagnostic(types.SelfInvalidatingKey, fmt.Sprintf(
		"%s:%s is never cached: its key reads %s its own run generates after the key is taken: %s. "+
			"Generate them before this target's key, as a skip_cache target it composes or a target it depends on",
		s.ProjectPath, s.Target, pluralFiles(len(generated)), joinCapped(generated, 5)))
}

func pluralFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// joinCapped names at most max paths, then says how many it withheld: one path is
// enough to find the writing tool, and a formatter would otherwise print hundreds.
func joinCapped(paths []string, max int) string {
	if len(paths) <= max {
		return joinComma(paths)
	}
	return fmt.Sprintf("%s (and %d more)", joinComma(paths[:max]), len(paths)-max)
}

func joinComma(paths []string) string {
	out := ""
	for i, p := range paths {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
