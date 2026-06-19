package cache

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"
)

// keyVersion is bumped when the set of hashed fields changes, forcing a full rebuild.
const keyVersion = 3

// hashStep computes the cache key for a Step (version, path, target, sources,
// env, deps, spell version, tool versions). Sources use an mtime fast-path.
func (c *Cache) hashStep(ctx context.Context, s *Step) (string, error) {
	h := sha256.New()

	// Build each key line in a reused scratch buffer and write it straight to the
	// hash. This avoids fmt.Fprintf's format-string parsing and per-line interface
	// boxing (~1 alloc/line) on the cache-key hot path; the byte layout is byte-for-
	// byte identical to the prior fmt formatting, so existing cache keys stay valid
	// and keyVersion need not change.
	//
	// ultra-opt: fmt.Fprintf -> append+Write on the key serialization path.
	// BenchmarkHashStep (200 deps/40 env/20 tools): -41% sec/op (71.6µ->42.1µ),
	// -47% B/op, -97% allocs/op (274->9), p=0.000 n=10. This hot path overrides the
	// usual fmt-for-legibility preference; TestHashKeyByteLayout pins the exact
	// bytes so the optimization cannot silently change cache keys.
	var buf []byte
	writeLine := func(parts ...string) {
		buf = buf[:0]
		for _, p := range parts {
			buf = append(buf, p...)
		}
		buf = append(buf, '\n')
		_, _ = h.Write(buf)
	}

	buf = append(buf, "keyVersion:"...)
	buf = strconv.AppendInt(buf, keyVersion, 10)
	buf = append(buf, '\n')
	_, _ = h.Write(buf)

	writeLine("projectPath:", s.ProjectPath)
	if s.Target != "" {
		writeLine("target:", s.Target)
	}
	// Active charms (sorted by the caller) change behaviour, so they key the
	// cache. Empty Charms adds nothing, so charm-less runs hash as before.
	for _, c := range s.Charms {
		writeLine("charm:", c)
	}

	files, err := expandSources(s.Sources, s.WorkspaceRoot, s.Outputs)
	if err != nil {
		return "", err
	}

	// Parallel file hashing with mtime fast-path.
	hashes, modes, err := c.hashFiles(ctx, files)
	if err != nil {
		return "", err
	}
	for i, f := range files {
		// Fold the executable bit (not full perms — that would miss across
		// machines with different umasks) so chmod +x on a source script,
		// which changes no content, invalidates the key.
		x := "0"
		if modes[i]&0o111 != 0 {
			x = "1"
		}
		writeLine("src:", f.rel, ":", hashes[i], ":", x)
	}

	env := append([]string(nil), s.EnvAllow...)
	slices.Sort(env)
	for _, k := range env {
		// Distinguish unset from set-to-empty: both return "" from Getenv.
		if v, ok := os.LookupEnv(k); ok {
			writeLine("env:", k, "=", v)
		} else {
			writeLine("env:", k, ":unset")
		}
	}

	deps := append([]string(nil), s.Deps...)
	slices.Sort(deps)
	for _, d := range deps {
		writeLine("dep:", d)
	}

	if s.SpellDefVersion != "" {
		writeLine("spellDefVersion:", s.SpellDefVersion)
	}

	tools := append([]string(nil), s.ToolVersions...)
	slices.Sort(tools)
	for _, v := range tools {
		writeLine("tool:", v)
	}

	result := hex.EncodeToString(h.Sum(nil))

	return result, nil
}

// hashFiles returns one sha256 per file: mtime fast-path → io_uring (Linux) → goroutine pool.
func (c *Cache) hashFiles(ctx context.Context, files []relAbs) ([]string, []os.FileMode, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}

	c.mtimes.load(ctx)

	hashes := make([]string, len(files))
	modes := make([]os.FileMode, len(files))

	// Pre-read fingerprints for Tier-1 mtime fast-path.
	type fingerprint struct {
		mtime int64
		size  int64
		ok    bool
	}
	fps := make([]fingerprint, len(files))
	for i, f := range files {
		mt, sz, md, err := statMtime(f.abs)
		if err != nil {
			continue
		}
		modes[i] = md
		fps[i] = fingerprint{mt, sz, true}
		if h, ok := c.mtimes.get(f.abs, fps[i].mtime, fps[i].size); ok {
			hashes[i] = h
		}
	}

	if c.hashFilesIoUring(files, hashes) {
		// Re-stat: if (mtime,size) changed between Tier-1 stat and io_uring read,
		// skip the store to avoid recording a stale fingerprint.
		for i, f := range files {
			if hashes[i] == "" || !fps[i].ok {
				continue
			}
			mt, sz, _, err := statMtime(f.abs)
			if err != nil {
				continue
			}
			if mt != fps[i].mtime || sz != fps[i].size {
				continue
			}
			c.mtimes.set(f.abs, hashes[i], fps[i].mtime, fps[i].size)
		}
		return hashes, modes, nil
	}

	// Tier 3: goroutine pool for remaining files.
	workers := runtime.GOMAXPROCS(0)
	if workers > len(files) {
		workers = len(files)
	}

	// Each index is enqueued exactly once and consumed by one worker, so the
	// concurrent writes to hashes[i] below target disjoint indices and need no
	// further synchronization (the channel provides the happens-before edge).
	idxCh := make(chan int, len(files))
	for i, h := range hashes {
		if h == "" {
			idxCh <- i
		}
	}
	close(idxCh)

	g, gctx := errgroup.WithContext(ctx)

	// Pin workers to LLC share-groups to keep sha256 state and buffers in L3.
	groups := cpuGroups()
	if len(groups) <= 1 {
		for range workers {
			g.Go(func() error {
				for i := range idxCh {
					if gctx.Err() != nil {
						return gctx.Err()
					}
					h, err := c.hashFileWithMtime(files[i].abs)
					if err != nil {
						return err
					}
					hashes[i] = h
				}
				return nil
			})
		}
	} else {
		for w := range workers {
			cpus := groups[w%len(groups)]
			g.Go(func() error {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				if unpin, err := pinThread(cpus); err == nil {
					defer unpin()
				}
				for i := range idxCh {
					if gctx.Err() != nil {
						return gctx.Err()
					}
					h, err := c.hashFileWithMtime(files[i].abs)
					if err != nil {
						return err
					}
					hashes[i] = h
				}
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	return hashes, modes, nil
}

// hashFileWithMtime returns the sha256 of abs using the mtime store as a fast-path.
func (c *Cache) hashFileWithMtime(abs string) (string, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("hashFile %q: %w", abs, err)
	}
	mtime := info.ModTime().UnixNano()
	size := info.Size()

	if h, ok := c.mtimes.get(abs, mtime, size); ok {
		return h, nil
	}

	h, err := hashFile(abs)
	if err != nil {
		return "", err
	}
	c.mtimes.set(abs, h, mtime, size)
	return h, nil
}

// hashFile returns the sha256 of the file at path, hex-encoded.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hashFile %q: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// relAbs pairs a workspace-relative file path with its absolute form.
type relAbs struct{ rel, abs string }

// expandSources turns source globs into a sorted slice of (rel, abs) pairs.
// Uses a single WalkDir pass with compiled matchers; prunes well-known ignore dirs early.
// Output globs are excluded from the walk (output tree is never an input).
func expandSources(globs []string, root string, exclude []string) ([]relAbs, error) {
	if len(globs) == 0 {
		return nil, nil
	}
	normalised := make([]string, len(globs))
	for i, g := range globs {
		normalised[i] = filepath.ToSlash(g)
	}
	pats := compileGlobs(normalised)

	var exclPats []compiledGlob
	var prunePrefixes []string
	if len(exclude) > 0 {
		exclNorm := make([]string, len(exclude))
		for i, g := range exclude {
			exclNorm[i] = filepath.ToSlash(g)
			if p := staticDirPrefix(exclNorm[i]); p != "" {
				prunePrefixes = append(prunePrefixes, p)
			}
		}
		exclPats = compileGlobs(exclNorm)
	}

	// rootLen is the number of bytes to strip from WalkDir paths to get the
	// workspace-relative path. root is always clean (no trailing separator)
	// except for the filesystem root ("/"), which magus never uses as a workspace.
	rootLen := len(root)

	// ultra-opt: pre-size out to len(globs) as an allocation floor. Spell Sources
	// are dominated by exact-path globs (package.json, lockfiles, *.mod) that each
	// match ~1 file, so len(globs) is a good first estimate and skips the early
	// 0→1→2→…→256 reallocs of nil-slice growth. Larger trees still amortize via
	// the usual doubling. Negligible on huge trees (the per-file rel strings
	// dominate allocs there) but a free win on the common small-Sources case.
	out := make([]relAbs, 0, len(globs))

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && isIgnoreDir(name) {
				return fs.SkipDir
			}
			if len(prunePrefixes) > 0 && path != root {
				// Zero-alloc relative path: path is always root+sep+rel inside WalkDir.
				rel := filepath.ToSlash(path[rootLen+1:])
				for _, pre := range prunePrefixes {
					if rel == pre || strings.HasPrefix(rel, pre+"/") {
						return fs.SkipDir
					}
				}
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		// Zero-alloc relative path: path is always root+sep+rel inside WalkDir.
		rel := filepath.ToSlash(path[rootLen+1:])
		for _, ep := range exclPats {
			if ep.Match(rel) {
				return nil
			}
		}
		for _, p := range pats {
			if p.Match(rel) {
				// WalkDir guarantees unique paths and we return after first match,
				// so no dedup map is needed.
				out = append(out, relAbs{rel: rel, abs: path})
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("expandSources walk: %w", err)
	}
	slices.SortFunc(out, func(a, b relAbs) int { return cmp.Compare(a.rel, b.rel) })
	return out, nil
}

// staticDirPrefix returns the static directory prefix of glob before the first
// metacharacter, used to skip output trees wholesale during source expansion.
func staticDirPrefix(glob string) string {
	idx := strings.IndexAny(glob, "*?[{")
	if idx < 0 {
		return ""
	}
	p := glob[:idx]
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// isIgnoreDir reports whether name is a well-known directory to skip during source expansion.
func isIgnoreDir(name string) bool {
	switch name {
	case ".git", ".hg", ".jj", ".magus", ".build",
		"vendor", "node_modules", "target", "gen":
		return true
	}
	return false
}

// shortHash returns the first 8 hex characters of h, for log lines.
func shortHash(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
