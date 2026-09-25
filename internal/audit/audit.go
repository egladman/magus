// Package audit detects cross-project writes: when a spell's downward walk crosses into a descendant
// project. Begin/Finish snapshot and diff descendant trees; no-op when no descendants or read-only.
package audit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"github.com/egladman/magus/types"
)

// fileState records mtime, size and inode for a snapshot entry; seen is marked in-place during
// diff to avoid allocs. Snapshot must not be shared across goroutines: diff() mutates it.
//
// The inode catches a file replaced wholesale with its mtime and size kept, which is what a
// cache replay does on APFS: it removes the file and clonefile(2)s the blob, carrying the
// blob's mtime. It comes from the lstat the walk already makes, so it costs no syscall and
// no read; zero means the platform reports none, and then mtime and size decide alone.
type fileState struct {
	modTimeNs int64
	size      int64
	ino       uint64
	seen      bool
}

// changed reports whether cur differs from s in any recorded property.
func (s fileState) changed(cur fileState) bool {
	return s.modTimeNs != cur.modTimeNs || s.size != cur.size || s.ino != cur.ino
}

type snapshot map[string]fileState

// changeKind classifies a per-file diff result.
type changeKind uint8

const (
	changeAdded changeKind = iota + 1
	changeRemoved
	changeModified
)

// change is a per-path diff result.
type change struct {
	path string
	kind changeKind
}

type changeBucket struct {
	added, modified, removed []string
}

// Audit carries the pre-snapshot needed to detect descendant writes
// performed during a single project's dispatch.
//
// descs holds every descendant of the dispatching project — used for
// per-change attribution via longest-prefix match. roots is the
// minimized subset (topmost only) that take/diff actually walks; a
// nested descendant like api/docs/v2 is reached by recursing into
// api/docs and would be double-walked if listed here as well.
type Audit struct {
	project *types.Project
	descs   []descendant
	roots   []descendant
	snap    snapshot
}

type descendant struct {
	path string // workspace-relative
	dir  string // absolute
}

// Begin returns a non-nil *Audit only when write is true and p has descendant projects not in active dispatch.
// Finish on a nil receiver is a no-op; callers can defer Finish unconditionally.
func Begin(ctx context.Context, p *types.Project, write bool) *Audit {
	if !write || p == nil {
		return nil
	}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil
	}
	descs := descendantsOf(ws, p, types.ActiveDispatchFromContext(ctx))
	if len(descs) == 0 {
		return nil
	}
	roots := topmostRoots(descs)
	snap, err := take(ctx, roots)
	if err != nil {
		slog.WarnContext(ctx, "magus: audit snapshot failed",
			slog.String("project", p.Path),
			slog.Any("err", err))
		return nil
	}
	return &Audit{project: p, descs: descs, roots: roots, snap: snap}
}

// Finish diffs descendant trees against the snapshot and rejects cross-project writes. Nil-safe.
func (a *Audit) Finish(ctx context.Context, target string) error {
	if a == nil {
		return nil
	}
	changes := diff(ctx, a.snap, a.roots)
	if len(changes) == 0 {
		return nil
	}
	return report(ctx, a.project, target, a.descs, changes, "")
}

// Replayed judges the absolute paths a cache hit of p's target restored, with the rule a
// Begin/Finish window applies to a run: a path inside a descendant that is not running a
// target of its own is a write across the boundary. A hit runs no body, so the paths the
// replay wrote are the whole of what it did, and no tree walk is needed to learn them.
// Nil-safe on p; write is the same charm gate Begin takes.
func Replayed(ctx context.Context, p *types.Project, target string, written []string, write bool) error {
	if !write || p == nil || len(written) == 0 {
		return nil
	}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil
	}
	descs := descendantsOf(ws, p, types.ActiveDispatchFromContext(ctx))
	if len(descs) == 0 {
		return nil
	}
	changes := make([]change, 0, len(written))
	for _, path := range written {
		changes = append(changes, change{path: path, kind: changeModified})
	}
	project := p.Path
	if project == "" {
		project = "."
	}
	writer := fmt.Sprintf("the cache replay of %q target %q, restoring the outputs its entry recorded", project, target)
	return report(ctx, p, target, descs, changes, writer)
}

func descendantsOf(ws types.WorkspaceReader, parent *types.Project, active *types.ActiveDispatch) []descendant {
	isRoot := parent.Path == "" || parent.Path == "."
	prefix := parent.Path + "/"
	all := ws.All()
	out := make([]descendant, 0, len(all)/4)
	for _, p := range all {
		if p.Path == parent.Path {
			continue
		}
		if !isRoot && !strings.HasPrefix(p.Path, prefix) {
			continue
		}
		if active.Has(p.Path) || active.Has(p.Dir) {
			continue
		}
		out = append(out, descendant{path: p.Path, dir: p.Dir})
	}
	return out
}

// topmostRoots returns the minimal set of roots covering descs; nested entries are elided from walks.
func topmostRoots(descs []descendant) []descendant {
	out := make([]descendant, 0, len(descs))
outer:
	for _, d := range descs {
		for _, r := range out {
			if strings.HasPrefix(d.dir, r.dir+string(filepath.Separator)) {
				continue outer
			}
		}
		// Also evict any already-added root that is nested under d.
		kept := out[:0]
		for _, r := range out {
			if strings.HasPrefix(r.dir, d.dir+string(filepath.Separator)) {
				continue
			}
			kept = append(kept, r)
		}
		out = append(kept, d)
	}
	return out
}

// walkFiles iterates regular files under root; buf passed to fn is reused (callers must copy to retain).
// Skips tool/VCS metadata dirs (see isMetaDir); no symlink follow; checks ctx cancellation between directories.
func walkFiles(ctx context.Context, root string, fn func(buf []byte, st fileState)) error {
	// 256 B handles paths up to /tmp/... TempDir + small subtree without
	// realloc; longer paths grow naturally via append.
	buf := make([]byte, 0, 256)
	buf = append(buf, root...)
	buf = ensureSpare(buf, 1) // spare byte for lstatFile's null terminator (Linux)
	return walkDir(ctx, buf, fn)
}

func ensureSpare(buf []byte, n int) []byte {
	if cap(buf)-len(buf) >= n {
		return buf
	}
	grown := make([]byte, len(buf), len(buf)+n+64)
	copy(grown, buf)
	return grown
}

// walkDir is the recursive worker for walkFiles; buf must have cap > len on entry (lstat null-terminator).
func walkDir(ctx context.Context, buf []byte, fn func(buf []byte, st fileState)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var errAcc error
	base := len(buf)
	err := readDirEnts(unsafe.String(unsafe.SliceData(buf), len(buf)), func(name []byte, kind dirEntKind) {
		if errAcc != nil {
			return
		}
		if kind == dentDir && isMetaDir(name) {
			return
		}
		buf = append(buf, filepath.Separator)
		buf = append(buf, name...)
		buf = ensureSpare(buf, 1)
		switch kind {
		case dentDir:
			if err := walkDir(ctx, buf, fn); err != nil {
				errAcc = err
			}
		case dentRegular:
			if st, ok := lstatFile(buf); ok {
				fn(buf, st)
			}
		}
		buf = buf[:base]
	})
	if err != nil {
		return err
	}
	return errAcc
}

// isMetaDir reports whether name is a tool or VCS metadata directory the audit
// skips wholesale. magus and the VCSes write into these during normal operation
// (e.g. a nested project's .magus cache/logs populated by a child magus run), so
// their churn is bookkeeping, not a cross-project source write. This is DELIBERATELY
// only the metadata subset: it does NOT skip the language/dependency dirs in
// project.IgnoreDirs (vendor, node_modules, ...), because a write into a vendored
// tree IS a cross-project source write the audit must see. switch string(name) is
// allocation-free.
func isMetaDir(name []byte) bool {
	switch string(name) {
	case ".git", ".hg", ".jj", ".magus", ".build":
		return true
	}
	return false
}

// take walks each descendant root once and records (mtime, size, inode) per
// regular file. Tool/VCS metadata dirs are skipped wholesale; symlinks are not followed.
// Missing roots are tolerated. roots must already be deduped against
// nesting via topmostRoots so each file is walked exactly once.
func take(ctx context.Context, roots []descendant) (snapshot, error) {
	snap := make(snapshot, 256)
	var errs []error
	for _, d := range roots {
		err := walkFiles(ctx, d.dir, func(buf []byte, st fileState) {
			// Map insertion requires a stable key; copy buf into a new string.
			snap[string(buf)] = st
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return snap, errors.Join(errs...)
	}
	return snap, nil
}

// diff re-walks roots and compares each regular file against pre.
func diff(ctx context.Context, pre snapshot, roots []descendant) []change {
	var out []change
	for _, d := range roots {
		_ = walkFiles(ctx, d.dir, func(buf []byte, st fileState) {
			key := string(buf)
			prev, existed := pre[key]
			if !existed {
				out = append(out, change{path: string(buf), kind: changeAdded})
				return
			}
			if prev.changed(st) {
				out = append(out, change{path: string(buf), kind: changeModified})
			}
			prev.seen = true
			pre[key] = prev
		})
	}
	for path, st := range pre {
		if !st.seen {
			out = append(out, change{path: path, kind: changeRemoved})
		}
	}
	return out
}

// reportCap bounds each reported path list.
const reportCap = 50

// dirOf returns the on-disk dir recorded for a descendant path.
func dirOf(descs []descendant, path string) (string, bool) {
	for _, d := range descs {
		if d.path == path {
			return d.dir, true
		}
	}
	return "", false
}

// report buckets changes by descendant project and returns actionable failures.
// A non-empty writer names who wrote; otherwise report infers one from p's declared outputs.
func report(ctx context.Context, p *types.Project, target string, descs []descendant, changes []change, writer string) error {
	by := make(map[string]*changeBucket, len(descs))
	for _, c := range changes {
		bestIdx, bestLen := -1, -1
		for i, d := range descs {
			if !strings.HasPrefix(c.path, d.dir+string(filepath.Separator)) {
				continue
			}
			if len(d.dir) > bestLen {
				bestLen = len(d.dir)
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			continue
		}
		d := descs[bestIdx]
		b := by[d.path]
		if b == nil {
			b = &changeBucket{}
			by[d.path] = b
		}
		rel, err := filepath.Rel(d.dir, c.path)
		if err != nil {
			rel = c.path
		}
		switch c.kind {
		case changeAdded:
			b.added = append(b.added, rel)
		case changeModified:
			b.modified = append(b.modified, rel)
		case changeRemoved:
			b.removed = append(b.removed, rel)
		}
	}
	descPaths := make([]string, 0, len(by))
	for desc := range by {
		descPaths = append(descPaths, desc)
	}
	sort.Strings(descPaths)

	project := p.Path
	if project == "" {
		project = "."
	}
	// Re-check the dispatch set HERE, not at Begin. descendantsOf runs before the
	// target body, so a project reached through a cross-project dependency has not been
	// marked yet; by the time its writes appear it is running a target of its own, and
	// those writes are its business. Checking only up front blamed the parent for them.
	active := types.ActiveDispatchFromContext(ctx)
	errs := make([]error, 0, len(descPaths))
	for _, desc := range descPaths {
		b := by[desc]
		if active.Has(desc) {
			continue
		}
		if d, ok := dirOf(descs, desc); ok && active.Has(d) {
			continue
		}
		summary := changeSummary(b)
		message := fmt.Sprintf("project %q target %q wrote into descendant project %q: %s", project, target, desc, summary)
		if writer != "" {
			message += "\nwriter: " + writer
		} else if claimer, glob, ok := outputClaim(p, desc, b); ok {
			message += fmt.Sprintf("\nwriter: likely %q target %q, whose declared output %q matches these files", project, claimer, glob)
		}
		message += fmt.Sprintf("\nfix: move this work to %q or exclude that path from the parent target", desc)
		types.EmitDiagnostic(ctx, types.DiagnosticEvent{
			Code:    types.DescendantBoundaryCrossed,
			Message: message,
			Unit:    project + ":" + target,
		})
		errs = append(errs, types.DiagnosticErrorf(types.DescendantBoundaryCrossed, "%s", message))
	}
	return errors.Join(errs...)
}

// outputClaim names the parent's target whose declared output glob matches a changed
// file in desc. MGS3001 fires on the target that opened the window, which for a
// composer is not the step that wrote: `generate` was blamed for mocks its composed
// mocks-generate restored from cache, and the report sent the reader hunting a
// concurrent writer that did not exist.
func outputClaim(p *types.Project, desc string, b *changeBucket) (target, glob string, ok bool) {
	var changed []string
	for _, rels := range [][]string{b.added, b.modified, b.removed} {
		for _, rel := range rels {
			changed = append(changed, desc+"/"+filepath.ToSlash(rel))
		}
	}
	names := make([]string, 0, len(p.TargetOutputs))
	for name := range p.TargetOutputs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, ref := range p.TargetOutputs[name] {
			if ref.Project != "" && ref.Project != p.Path {
				continue
			}
			rooted := types.RootGlob(p.Path, ref.Glob)
			for _, c := range changed {
				if types.MatchesAnyGlob([]string{rooted}, c) {
					return name, ref.Glob, true
				}
			}
		}
	}
	return "", "", false
}

func changeSummary(b *changeBucket) string {
	parts := make([]string, 0, 3)
	if len(b.modified) > 0 {
		parts = append(parts, changePart("modified", b.modified))
	}
	if len(b.added) > 0 {
		parts = append(parts, changePart("added", b.added))
	}
	if len(b.removed) > 0 {
		parts = append(parts, changePart("removed", b.removed))
	}
	return strings.Join(parts, " ")
}

func changePart(kind string, paths []string) string {
	paths = append([]string(nil), paths...)
	sort.Strings(paths)
	if len(paths) <= reportCap {
		return fmt.Sprintf("%s=%v", kind, paths)
	}
	return fmt.Sprintf("%s=%v (%d total)", kind, paths[:reportCap], len(paths))
}
