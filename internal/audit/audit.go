// Package audit detects cross-project writes: when a spell's downward walk crosses into a descendant
// project. Begin/Finish snapshot and diff descendant trees; no-op when no descendants or read-only.
package audit

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/egladman/magus/types"
)

// fileState records mtime+size for a snapshot entry; seen is marked in-place during diff to avoid allocs.
// Snapshot must not be shared across goroutines: diff() mutates it.
type fileState struct {
	modTimeNs int64
	size      int64
	seen      bool
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

// Audit carries the pre-snapshot needed to detect descendant writes
// performed during a single project's dispatch.
//
// descs holds every descendant of the dispatching project — used for
// per-change attribution via longest-prefix match. roots is the
// minimised subset (topmost only) that take/diff actually walks; a
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

// Finish diffs descendant trees against the snapshot and warns on cross-project writes. Nil-safe.
func (a *Audit) Finish(ctx context.Context, target string) {
	if a == nil {
		return
	}
	changes := diff(ctx, a.snap, a.roots)
	if len(changes) == 0 {
		return
	}
	report(ctx, a.project, target, a.descs, changes)
}

func descendantsOf(ws types.WorkspaceReader, parent *types.Project, active map[string]struct{}) []descendant {
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
		if _, dispatching := active[p.Path]; dispatching {
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
// Skips .git; does not follow symlinks; checks ctx cancellation between directories.
func walkFiles(ctx context.Context, root string, fn func(buf []byte, modTimeNs, size int64)) error {
	// 256 B handles paths up to /tmp/... TempDir + small subtree without
	// realloc; longer paths grow naturally via append.
	buf := make([]byte, 0, 256)
	buf = append(buf, root...)
	buf = ensureSpare(buf, 1) // spare byte for lstatMtimeSize's null terminator (Linux)
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
func walkDir(ctx context.Context, buf []byte, fn func(buf []byte, modTimeNs, size int64)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var errAcc error
	base := len(buf)
	err := readDirEnts(unsafe.String(unsafe.SliceData(buf), len(buf)), func(name []byte, kind dirEntKind) {
		if errAcc != nil {
			return
		}
		if kind == dentDir && isDotGit(name) {
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
			if mt, sz, ok := lstatMtimeSize(buf); ok {
				fn(buf, mt, sz)
			}
		}
		buf = buf[:base]
	})
	if err != nil {
		return err
	}
	return errAcc
}

// isDotGit returns true for ".git" (the wholesale-skipped directory).
func isDotGit(name []byte) bool {
	return len(name) == 4 && name[0] == '.' && name[1] == 'g' && name[2] == 'i' && name[3] == 't'
}

// take walks each descendant root once and records (mtime, size) per
// regular file. .git is skipped wholesale; symlinks are not followed.
// Missing roots are tolerated. roots must already be deduped against
// nesting via topmostRoots so each file is walked exactly once.
func take(ctx context.Context, roots []descendant) (snapshot, error) {
	snap := make(snapshot, 256)
	var errs []error
	for _, d := range roots {
		err := walkFiles(ctx, d.dir, func(buf []byte, mt, sz int64) {
			// Map insertion requires a stable key; copy buf into a new string.
			snap[string(buf)] = fileState{modTimeNs: mt, size: sz}
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
		_ = walkFiles(ctx, d.dir, func(buf []byte, mt, sz int64) {
			// Stack-local string view avoids one alloc per map probe; heap string only for change entries.
			key := unsafe.String(unsafe.SliceData(buf), len(buf))
			prev, existed := pre[key]
			if !existed {
				out = append(out, change{path: string(buf), kind: changeAdded})
				return
			}
			if prev.modTimeNs != mt || prev.size != sz {
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

// reportCap bounds per-bucket file list length to prevent log inflation.
const reportCap = 50

// report buckets changes by descendant project and warns; attribution uses innermost dir prefix.
func report(ctx context.Context, p *types.Project, target string, descs []descendant, changes []change) {
	type bucket struct {
		added, modified, removed []string
	}
	by := make(map[string]*bucket, len(descs))
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
			b = &bucket{}
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
	for desc, b := range by {
		attrs := []any{
			slog.String("project", p.Path),
			slog.String("target", target),
			slog.String("descendant", desc),
		}
		if n := len(b.modified); n > 0 {
			attrs = append(attrs, slog.Any("modified", capPaths(b.modified)))
			if n > reportCap {
				attrs = append(attrs, slog.Int("modified_total", n))
			}
		}
		if n := len(b.added); n > 0 {
			attrs = append(attrs, slog.Any("added", capPaths(b.added)))
			if n > reportCap {
				attrs = append(attrs, slog.Int("added_total", n))
			}
		}
		if n := len(b.removed); n > 0 {
			attrs = append(attrs, slog.Any("removed", capPaths(b.removed)))
			if n > reportCap {
				attrs = append(attrs, slog.Int("removed_total", n))
			}
		}
		slog.WarnContext(ctx,
			types.FormatDiagnostic(types.DescendantBoundaryCrossed, "descendant project boundary crossed"),
			attrs...)
	}
}

func capPaths(paths []string) []string {
	if len(paths) <= reportCap {
		return paths
	}
	return paths[:reportCap]
}
