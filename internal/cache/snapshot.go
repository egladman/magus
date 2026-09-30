package cache

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/cache/reflink"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

// snapshot records the project's declared outputs into the local store's blobs and
// returns the manifest describing them, for each tier to store. A project with no
// declared outputs yields an empty manifest (a correct cache hit on rerun).
func (c *Cache) snapshot(ctx context.Context, s Step, hash string, ran time.Duration) (*Manifest, []string, error) {
	root := s.WorkspaceRoot
	matches, err := expandOutputGlobs(s.Outputs, root, s.NestedDirs)
	if err != nil {
		return nil, nil, err
	}
	// Only a target's OWN declaration makes an empty result an error; see Step.OutputsDeclared.
	if len(matches) == 0 && len(s.Outputs) > 0 && s.OutputsDeclared {
		return nil, nil, fmt.Errorf("snapshot: target %q in project %q declared outputs but produced none: %v",
			s.Target, s.ProjectPath, s.Outputs)
	}
	// Each required glob is checked on its own, not folded into the all-or-nothing test
	// above: a target declaring its own outputs alongside a cross-project one passes that
	// test on its own outputs alone, and the missing foreign file goes unnoticed.
	//
	// Found among matches, which exclusions have already narrowed: a required glob whose
	// every file is excluded produced nothing the snapshot keeps.
	kept := make(map[string]bool, len(matches))
	for _, m := range matches {
		kept[m.rel] = true
	}
	for _, g := range s.RequiredOutputs {
		found, err := expandOutputGlobs([]types.Glob{g}, root, s.NestedDirs)
		if err != nil {
			return nil, nil, err
		}
		if !slices.ContainsFunc(found, func(f relAbs) bool { return kept[f.rel] }) {
			return nil, nil, types.DiagnosticErrorf(types.CrossOutputNotProduced,
				"target %q declared an output into another project (%q) but produced no file matching it; check the path the target actually writes",
				s.Target, g.Pattern)
		}
	}
	manifest := &Manifest{
		ProjectPath: s.ProjectPath,
		Hash:        hash,
		Target:      s.Target,
		CreatedAt:   time.Now().UTC(),
		Platform:    c.platform,
		DurationMs:  ran.Milliseconds(),
		Stamps:      stampDigests(root, s.Stamps),
	}
	// Carry the target's return value onto the entry so a hit can replay it; absent
	// for a void target, which is nearly all of them.
	if v, ok := types.RecordedReturn(ctx, s.ProjectPath, s.Target); ok {
		// Stored verbatim on purpose: a HIT replays this value, so masking it here would
		// make the second run differ from the first. The remote copy is redacted at
		// export instead (see exportArtifact).
		manifest.Return = v
	}
	var written []string
	for _, m := range matches {
		rec, err := c.snapshotOne(m.abs, m.rel)
		if err != nil {
			return nil, nil, err
		}
		manifest.Outputs = append(manifest.Outputs, rec)
		written = append(written, m.abs)
	}
	return manifest, written, nil
}

func (c *Cache) snapshotOne(abs, rel string) (OutputRecord, error) {
	info, err := os.Lstat(abs)
	if err != nil {
		return OutputRecord{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(abs)
		if err != nil {
			return OutputRecord{}, err
		}
		return OutputRecord{Path: rel, Symlink: target, Mode: uint32(info.Mode() & 0o777)}, nil
	}
	if info.IsDir() {
		return OutputRecord{}, fmt.Errorf("snapshotOne: %s is a directory (use a glob like %s/**)", rel, rel)
	}
	// Opening a FIFO blocks until a writer appears (no timeout, no diagnostic),
	// so an output glob matching a stray pipe hung the build outright. A replay
	// materializes every blob as a regular file, so a non-regular output cannot
	// round-trip anyway; refusing it is both the fix and the honest contract.
	if !info.Mode().IsRegular() {
		return OutputRecord{}, fmt.Errorf("snapshotOne: %s is not a regular file (%s); a target's declared outputs must be regular files or symlinks", rel, info.Mode().Type())
	}
	// preHash is a cheap fast-path check only: if the CAS already holds a blob
	// under it, we're done without touching abs again. It must never be used
	// to name a blob we actually write below — that name has to come from the
	// bytes copied in this same pass (see the hash-while-copy below), or a
	// write to abs between this hash and a later separate read would store
	// bytes under a hash they don't match, permanently poisoning the CAS
	// entry (the dedup gate trusts an existing blob name forever).
	preHash, err := hashFile(abs)
	if err != nil {
		return OutputRecord{}, err
	}
	hash := preHash
	dst := c.blobPath(preHash)
	if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return OutputRecord{}, err
		}
		tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp.*")
		if err != nil {
			return OutputRecord{}, err
		}
		tmpName := tmp.Name()
		defer func() { _ = os.Remove(tmpName) }()
		h := sha256.New()
		if err := copyToFile(abs, io.MultiWriter(tmp, h)); err != nil {
			_ = tmp.Close()
			return OutputRecord{}, err
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return OutputRecord{}, err
		}
		if err := tmp.Close(); err != nil {
			return OutputRecord{}, err
		}
		// The authoritative hash is the one just computed from the bytes this
		// pass actually copied, not preHash.
		hash = hex.EncodeToString(h.Sum(nil))
		dst = c.blobPath(hash)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return OutputRecord{}, err
		}
		if err := os.Rename(tmpName, dst); err != nil {
			return OutputRecord{}, err
		}
	}
	return OutputRecord{
		Path: rel,
		Blob: hash,
		Mode: uint32(info.Mode() & 0o777),
		Size: info.Size(),
	}, nil
}

// expandOutputGlobs expands output globs relative to root; rejects absolute paths and "..".
// A match inside one of nested is dropped unless its glob is rooted inside that project,
// and one its glob's exclusions claim is dropped too, so an excluded file is never stored.
func expandOutputGlobs(globs []types.Glob, root string, nested []string) ([]relAbs, error) {
	for _, g := range globs {
		for _, pattern := range append([]string{g.Pattern}, g.Except...) {
			if filepath.IsAbs(pattern) || strings.Contains(pattern, "..") {
				return nil, fmt.Errorf("output glob must be repo-relative without ..: %q", pattern)
			}
		}
	}
	rootFS := os.DirFS(root)
	seen := map[string]struct{}{}
	var out []relAbs
	for _, g := range globs {
		keep := func(rel string) bool {
			if !types.GlobClaims(g.Pattern, rel, nested) || g.Excludes(rel) {
				return false
			}
			_, dup := seen[rel]
			seen[rel] = struct{}{}
			return !dup
		}
		matches, err := doublestar.Glob(rootFS, g.Pattern)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", g.Pattern, err)
		}
		for _, m := range matches {
			abs := filepath.Join(root, m)
			info, err := os.Lstat(abs)
			if err != nil {
				continue
			}
			if info.IsDir() {
				err := filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() {
						return nil
					}
					rel, _ := filepath.Rel(root, p)
					rel = filepath.ToSlash(rel)
					if keep(rel) {
						out = append(out, relAbs{rel: rel, abs: p})
					}
					return nil
				})
				if err != nil {
					return nil, err
				}
				continue
			}
			if keep(m) {
				out = append(out, relAbs{rel: m, abs: abs})
			}
		}
	}
	slices.SortFunc(out, func(a, b relAbs) int { return cmp.Compare(a.rel, b.rel) })
	return out, nil
}

// ownedOutputs drops the records s's outputs do not own: one inside a nested project that
// no glob of s rooted there claims, and one every claiming glob excludes. A manifest is
// not trusted to have been snapshotted under these rules (a remote tier, an older binary,
// a declaration that gained an exclusion), so a hit enforces them again: replay must
// never write over a file the declaration carves out. It returns m itself when nothing
// drops.
func ownedOutputs(m *Manifest, s Step) *Manifest {
	if len(s.NestedDirs) == 0 && !slices.ContainsFunc(s.Outputs, func(g types.Glob) bool { return len(g.Except) > 0 }) {
		return m
	}
	kept := make([]OutputRecord, 0, len(m.Outputs))
	for _, rec := range m.Outputs {
		if ownsRecord(s, rec.Path) {
			kept = append(kept, rec)
		}
	}
	if len(kept) == len(m.Outputs) {
		return m
	}
	narrowed := *m
	narrowed.Outputs = kept
	return &narrowed
}

// ownsRecord reports whether s's outputs own the recorded path rel. A record no glob
// claims is kept outside a nested project, as a hit always has been.
func ownsRecord(s Step, rel string) bool {
	nested := types.NestedOwner(rel, s.NestedDirs) != ""
	claimed := false
	for _, g := range s.Outputs {
		if !(types.Glob{Pattern: g.Pattern}).Match(rel) || !types.GlobClaims(g.Pattern, rel, s.NestedDirs) {
			continue
		}
		if !g.Excludes(rel) {
			return true
		}
		claimed = true
	}
	return !claimed && !nested
}

// replay restores a manifest's outputs from the local store.
func (c *Cache) replay(ctx context.Context, m *Manifest, root string) ([]string, error) {
	return c.replayFrom(ctx, m, root, c.dir)
}

// replayFrom restores a manifest's outputs into root from the blobs under store (the
// local store, or a remote hit's staging root): reflink, else byte copy.
func (c *Cache) replayFrom(ctx context.Context, m *Manifest, root, store string) ([]string, error) {
	var paths []string
	for _, rec := range m.Outputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dst := filepath.Join(root, filepath.FromSlash(rec.Path))
		// A manifest can be attacker-controlled (an insecure remote, or a signed
		// supply-chain artifact whose recorded outputs magus never authored), so
		// nothing here may write or delete outside root. Refuse a destination that
		// escapes root, or whose parent chain traverses a symlink, checked before
		// the Remove and MkdirAll below, either of which would otherwise follow a
		// symlink an earlier record planted (record "link"->"../" then "link/evil").
		if err := ensureReplayDstSafe(root, dst, rec.Path); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if rec.Symlink != "" {
			// A symlink target is unchecked bytes from the manifest; a later record
			// writing through it would escape root. Refuse one that resolves outside.
			if err := ensureSymlinkTargetInRoot(root, dst, rec.Symlink); err != nil {
				return nil, err
			}
		}
		if err := replayRecord(rec, dst, store); err != nil {
			return nil, fmt.Errorf("replay %s: %w", rec.Path, err)
		}
		paths = append(paths, dst)
	}
	return paths, nil
}

// replayRecord materializes one output record at dst so that dst is never absent: a
// file already holding the recorded bytes (or a link already naming the recorded
// target) is left alone, and anything else is staged beside dst and renamed over it.
// A sibling target compiling against this tree meets the old bytes or the new ones,
// never a missing path: removing dst first opens a window in which `go build` fails
// with "no matching files found" for a go:embed of a file the replay is about to
// restore.
func replayRecord(rec OutputRecord, dst, store string) error {
	if replayCurrent(rec, dst) {
		return nil
	}
	tmp, err := stagingName(dst)
	if err != nil {
		return err
	}
	if rec.Symlink != "" {
		err = os.Symlink(rec.Symlink, tmp)
	} else {
		err = replayBlob(blobPathIn(store, rec.Blob), tmp)
		if err == nil && rec.Mode != 0 {
			_ = file.Chmod(tmp, os.FileMode(rec.Mode&0o777)) // best-effort (no-op on wasm)
		}
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// replayCurrent reports whether dst already is what rec records: the same link target,
// or a regular file with the recorded bytes and mode. Leaving such a file untouched also
// keeps its mtime, so nothing keyed on it goes dirty.
func replayCurrent(rec OutputRecord, dst string) bool {
	info, err := os.Lstat(dst)
	if err != nil {
		return false
	}
	if rec.Symlink != "" {
		target, err := os.Readlink(dst)
		return err == nil && target == rec.Symlink
	}
	if !info.Mode().IsRegular() || info.Size() != rec.Size {
		return false
	}
	if rec.Mode != 0 && runtime.GOOS != "windows" && uint32(info.Mode()&0o777) != rec.Mode&0o777 {
		return false
	}
	sum, err := hashFile(dst)
	return err == nil && sum == rec.Blob
}

// stagingName reserves an unused path in dst's directory, so the rename that follows
// stays on one filesystem and replaces dst in a single step.
func stagingName(dst string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".replay.*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := errors.Join(f.Close(), os.Remove(name)); err != nil {
		return "", err
	}
	return name, nil
}

// ensureReplayDstSafe refuses a replay destination that escapes root or whose
// parent chain traverses a symlink. Both would let a write or delete land
// outside the workspace when the manifest is hostile. A parent component that
// does not yet exist stops the walk: MkdirAll creates the rest as real dirs.
func ensureReplayDstSafe(root, dst, recPath string) error {
	rel, err := filepath.Rel(root, dst)
	if err != nil {
		return fmt.Errorf("replay: output path %q: %w", recPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("replay: refusing output path %q: escapes workspace root", recPath)
	}
	cur := root
	comps := strings.Split(rel, string(filepath.Separator))
	for _, comp := range comps[:len(comps)-1] { // parents only; the base is created last
		if comp == "" || comp == "." {
			continue
		}
		cur = filepath.Join(cur, comp)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("replay: refusing output path %q: parent %q is a symlink", recPath, cur)
		}
	}
	return nil
}

// ensureSymlinkTargetInRoot refuses a symlink whose target resolves outside
// root. A relative target is interpreted against the link's own directory, the
// same way the OS resolves it.
func ensureSymlinkTargetInRoot(root, dst, target string) error {
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(dst), target)
	}
	resolved = filepath.Clean(resolved)
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return fmt.Errorf("replay: symlink target %q: %w", target, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("replay: refusing symlink target %q: escapes workspace root", target)
	}
	return nil
}

// replayBlob materializes blob at dst (dst must not exist). Tries reflink (CoW) → copy.
// Both yield a file with an independent inode, so a downstream in-place rewrite (or the
// caller's chmod) cannot mutate the shared CAS blob. Hard-linking is deliberately not used:
// it would alias the blob inode and silently poison the cache on the next in-place write.
func replayBlob(blob, dst string) error {
	if err := reflink.Clone(blob, dst); err == nil {
		return nil
	}
	return copyFile(blob, dst)
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

// copyToFile copies src into dst (an already-open file, or a MultiWriter
// wrapping one to hash while copying).
func copyToFile(src string, dst io.Writer) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(dst, in)
	return err
}
