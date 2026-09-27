package cache

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/json"
)

// A SPELL CACHE BUNDLE carries the caches a spell declares (for go, GOCACHE and
// GOMODCACHE) between machines, so a magus miss rebuilds only what changed instead of
// starting its tools cold. It rides the remote tier under spellCacheNamespace and is
// signed and verified exactly like a build entry: a bundle's bytes are executed by the
// next compile, so an unverified one is never restored, and there is no fallback to one.
//
// The layout is a gzip-tar: the index (every file's path, size, digest and mode), its
// signature, then the files. The index comes first so a reader authenticates it before
// a single file is written, and checks each file against it as it streams.

const (
	domainSpellCache = "magus-spell-cache-v1\x00"

	// spellCacheNamespace keeps bundles out of every project's key space: no project
	// path begins with "@".
	spellCacheNamespace = "@spell-cache"
	spellCacheIndexName = "index.json"
	spellCacheSchema    = 1

	// spellCacheLookback is how many daily keys a reader probes, newest first. The store
	// cannot be listed, so "newest" is found by asking for each day in turn.
	spellCacheLookback = 7

	maxSpellCacheIndex = 64 << 20
	maxSpellCacheSig   = 64 << 10

	// restoredAge backdates every restored file. Go re-stamps a cache entry it uses only
	// when the entry is over an hour old, and trims entries unused for five days, so a
	// day-old stamp makes use visible to a later save without inviting Go's trim. A save
	// keeps a use-stamping cache's entries dated after now-restoredAge: every restored
	// entry a run used, and nothing it left alone.
	restoredAge = 24 * time.Hour
)

// spellCachesDir holds, under a cache directory, the caches a box relocates there. The
// local tier's archive leaves it out: those caches travel only as signed bundles.
const spellCachesDir = "spell-caches"

// SpellCacheDir is where a box whose magus keeps its local tier in cacheDir keeps the
// cache env of spell.
func SpellCacheDir(cacheDir, spell, env string) string {
	return filepath.Join(cacheDir, spellCachesDir, spell, env)
}

// SpellCacheKey names a bundle by what makes its entries reusable. Tools and Locks are
// hex digests: a reader wants Tools to match exactly, and prefers Locks to.
type SpellCacheKey struct {
	Spell string `json:"spell"`
	Tools string `json:"tools"`
	Locks string `json:"locks"`
}

// NewSpellCacheKey keys spell's bundle on tools, the lines naming its tools' versions
// and the host they run on, and locks, the contents of its lockfiles keyed by
// workspace-relative path. The order of tools does not matter.
func NewSpellCacheKey(spell string, tools []string, locks map[string][]byte) SpellCacheKey {
	lines := slices.Clone(tools)
	slices.Sort(lines)
	tc := sha256.New()
	for _, l := range slices.Compact(lines) {
		fmt.Fprintf(tc, "%s\n", l)
	}
	sums := sha256.New()
	for _, p := range slices.Sorted(maps.Keys(locks)) {
		sum := sha256.Sum256(locks[p])
		fmt.Fprintf(sums, "%s\x00%x\n", p, sum)
	}
	return SpellCacheKey{Spell: spell, Tools: hex.EncodeToString(tc.Sum(nil)), Locks: hex.EncodeToString(sums.Sum(nil))}
}

// String is the day-independent part of the key, for a log line.
func (k SpellCacheKey) String() string {
	return k.Spell + "-" + short(k.Tools) + "-" + short(k.Locks)
}

func (k SpellCacheKey) remoteKey(day time.Time) string {
	return k.String() + "-" + day.UTC().Format("20060102")
}

// pointerKey names the day's bundle for these tools whatever the lockfiles, so a reader
// whose lockfiles changed can still take what an older set saved.
func (k SpellCacheKey) pointerKey(day time.Time) string {
	return k.Spell + "-" + short(k.Tools) + "-" + day.UTC().Format("20060102")
}

func short(digest string) string {
	if len(digest) > 16 {
		return digest[:16]
	}
	return digest
}

// SpellCacheRoot is one cache a bundle carries, filed in the bundle under Name.
type SpellCacheRoot struct {
	Name string
	Dir  string
	// StampsUse marks a cache whose tool re-dates an entry it uses, so a save keeps
	// only what was used since the restore.
	StampsUse bool
	// Skip are slash globs under Dir that stay out of the bundle.
	Skip []string
}

func (r SpellCacheRoot) skips(rel string) bool {
	return slices.ContainsFunc(r.Skip, func(g string) bool { return doublestar.MatchUnvalidated(g, rel) })
}

const spellCacheStaging = ".magus-spell-cache-"

type spellCacheIndex struct {
	Schema  int              `json:"schema"`
	Key     SpellCacheKey    `json:"key"`
	Name    string           `json:"name,omitempty"` // the remote key it was stored under
	Created time.Time        `json:"created"`
	Files   []spellCacheFile `json:"files"`
}

type spellCacheFile struct {
	Path   string      `json:"path"` // "<root name>/<slash path under the root>"
	Size   int64       `json:"size"`
	SHA256 string      `json:"sha256"`
	Mode   fs.FileMode `json:"mode"` // permission bits as saved; go keeps its module cache read-only
}

// SpellCacheStats counts one bundle's files.
type SpellCacheStats struct {
	Files int   // files written to the bundle, or restored from it
	Bytes int64 // their total size, uncompressed
	// Skipped is, on a save, the entries of a use-stamping cache left out as unused; on
	// a restore, the files already present locally and left as they were.
	Skipped int
}

// collectSpellCacheFiles walks roots and digests every file a bundle should carry.
func collectSpellCacheFiles(ctx context.Context, roots []SpellCacheRoot, now time.Time) ([]spellCacheFile, SpellCacheStats, error) {
	var (
		files []spellCacheFile
		stats SpellCacheStats
	)
	cutoff := now.Add(-restoredAge)
	for _, root := range roots {
		err := filepath.WalkDir(root.Dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) && p == root.Dir {
					return filepath.SkipDir
				}
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(root.Dir, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel != "." && strings.HasPrefix(d.Name(), spellCacheStaging) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || root.skips(rel) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if root.StampsUse && !info.ModTime().After(cutoff) {
				stats.Skipped++
				return nil
			}
			sum, err := digestFile(p)
			if err != nil {
				return err
			}
			files = append(files, spellCacheFile{Path: root.Name + "/" + rel, Size: info.Size(), SHA256: sum, Mode: info.Mode().Perm()})
			stats.Files++
			stats.Bytes += info.Size()
			return nil
		})
		if err != nil {
			return nil, SpellCacheStats{}, fmt.Errorf("spell cache bundle: %s: %w", root.Name, err)
		}
	}
	return files, stats, nil
}

func digestFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeSpellCacheBundle writes a signed bundle of roots to w. name is the remote key
// the bundle is stored under, bound into the signed index.
func writeSpellCacheBundle(ctx context.Context, w io.Writer, s *signer, key SpellCacheKey, name string, roots []SpellCacheRoot, now time.Time) (SpellCacheStats, error) {
	if s == nil {
		return SpellCacheStats{}, errors.New("spell cache bundle: needs a signing key (MAGUS_CACHE_SIGNING_KEY); an unsigned bundle is refused on restore")
	}
	files, stats, err := collectSpellCacheFiles(ctx, roots, now)
	if err != nil {
		return SpellCacheStats{}, err
	}
	idx, err := json.Marshal(spellCacheIndex{Schema: spellCacheSchema, Key: key, Name: name, Created: now.UTC(), Files: files})
	if err != nil {
		return SpellCacheStats{}, err
	}
	sig, err := s.sign(domainSpellCache, idx, nil)
	if err != nil {
		return SpellCacheStats{}, err
	}
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return SpellCacheStats{}, err
	}
	tw := tar.NewWriter(gz)
	for _, m := range []struct {
		name string
		data []byte
	}{{spellCacheIndexName, idx}, {sigFileName, sig}} {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: m.name, Size: int64(len(m.data)), Mode: 0o644}); err != nil {
			return SpellCacheStats{}, err
		}
		if _, err := tw.Write(m.data); err != nil {
			return SpellCacheStats{}, err
		}
	}
	dirs := make(map[string]string, len(roots))
	for _, r := range roots {
		dirs[r.Name] = r.Dir
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return SpellCacheStats{}, err
		}
		rootName, rel, _ := strings.Cut(f.Path, "/")
		if err := copySpellCacheFile(tw, filepath.Join(dirs[rootName], filepath.FromSlash(rel)), f); err != nil {
			return SpellCacheStats{}, fmt.Errorf("spell cache bundle: %s: %w", f.Path, err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		return SpellCacheStats{}, err
	}
	return stats, nil
}

func copySpellCacheFile(tw *tar.Writer, src string, f spellCacheFile) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: f.Path, Size: f.Size, Mode: int64(f.Mode)}); err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(tw, h), in, f.Size); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return errors.New("changed while it was bundled")
	}
	return nil
}

// spellCacheWant is what a reader accepts: the key and the name the bundle was fetched
// under. anyLocks accepts a bundle of other lockfiles.
type spellCacheWant struct {
	key      SpellCacheKey
	name     string
	anyLocks bool
}

// readSpellCacheBundle authenticates a bundle and restores it into roots. Nothing is
// placed until every file has been checked against the signed index: a bundle that
// fails at any point leaves the roots as they were.
func readSpellCacheBundle(ctx context.Context, r io.Reader, v *verifier, want spellCacheWant, roots []SpellCacheRoot, limit int64, now time.Time) (SpellCacheStats, error) {
	if v == nil {
		return SpellCacheStats{}, errors.New("no trust set configured; refusing an unauthenticated spell cache bundle")
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return SpellCacheStats{}, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	idxBytes, err := readLeadMember(tr, spellCacheIndexName, maxSpellCacheIndex)
	if err != nil {
		return SpellCacheStats{}, err
	}
	sig, err := readLeadMember(tr, sigFileName, maxSpellCacheSig)
	if err != nil {
		return SpellCacheStats{}, err
	}
	if err := v.verify(domainSpellCache, sig, idxBytes, nil); err != nil {
		return SpellCacheStats{}, err
	}
	var idx spellCacheIndex
	if err := json.Unmarshal(idxBytes, &idx); err != nil {
		return SpellCacheStats{}, fmt.Errorf("index: %w", err)
	}
	if err := want.check(idx); err != nil {
		return SpellCacheStats{}, err
	}

	rootsByName := make(map[string]SpellCacheRoot, len(roots))
	for _, root := range roots {
		rootsByName[root.Name] = root
	}
	entries := make(map[string]spellCacheFile, len(idx.Files))
	var total int64
	for _, f := range idx.Files {
		rootName, rel, ok := strings.Cut(f.Path, "/")
		if _, known := rootsByName[rootName]; !ok || !known || !localRel(rel) {
			return SpellCacheStats{}, fmt.Errorf("index names an unsafe or unknown path %q", f.Path)
		}
		if _, dup := entries[f.Path]; dup || f.Size < 0 || f.Mode&^fs.ModePerm != 0 {
			return SpellCacheStats{}, fmt.Errorf("index entry %q is malformed", f.Path)
		}
		entries[f.Path] = f
		total += f.Size
	}
	if total > limit {
		return SpellCacheStats{}, fmt.Errorf("bundle holds %d bytes, over the %d-byte import limit", total, limit)
	}

	staging := make(map[string]string, len(roots))
	defer func() {
		for _, dir := range staging {
			_ = os.RemoveAll(dir)
		}
	}()
	for _, root := range roots {
		if err := os.MkdirAll(root.Dir, 0o755); err != nil {
			return SpellCacheStats{}, err
		}
		// Inside the root so the commit is a rename on one filesystem.
		dir, err := os.MkdirTemp(root.Dir, spellCacheStaging+"*")
		if err != nil {
			return SpellCacheStats{}, err
		}
		staging[root.Name] = dir
	}

	stamp := now.Add(-restoredAge)
	seen := make(map[string]struct{}, len(entries))
	for {
		if err := ctx.Err(); err != nil {
			return SpellCacheStats{}, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return SpellCacheStats{}, fmt.Errorf("tar: %w", err)
		}
		f, ok := entries[hdr.Name]
		if !ok || hdr.Typeflag != tar.TypeReg {
			return SpellCacheStats{}, fmt.Errorf("member %q is not in the signed index", hdr.Name)
		}
		if _, dup := seen[hdr.Name]; dup {
			return SpellCacheStats{}, fmt.Errorf("member %q appears twice", hdr.Name)
		}
		if hdr.Size != f.Size {
			return SpellCacheStats{}, fmt.Errorf("member %q is %d bytes, the index says %d", hdr.Name, hdr.Size, f.Size)
		}
		seen[hdr.Name] = struct{}{}
		rootName, rel, _ := strings.Cut(f.Path, "/")
		if err := stageSpellCacheFile(tr, filepath.Join(staging[rootName], filepath.FromSlash(rel)), f, stamp); err != nil {
			return SpellCacheStats{}, fmt.Errorf("member %q: %w", hdr.Name, err)
		}
	}
	if len(seen) != len(entries) {
		return SpellCacheStats{}, fmt.Errorf("bundle is truncated: %d of %d indexed files present", len(seen), len(entries))
	}

	var stats SpellCacheStats
	for _, f := range idx.Files {
		rootName, rel, _ := strings.Cut(f.Path, "/")
		dst := filepath.Join(rootsByName[rootName].Dir, filepath.FromSlash(rel))
		// A present file is the tool's own, content addressed or immutable once written.
		if _, err := os.Lstat(dst); err == nil {
			stats.Skipped++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return stats, err
		}
		if err := os.Rename(filepath.Join(staging[rootName], filepath.FromSlash(rel)), dst); err != nil {
			// Go leaves an extracted module's directory read-only; one already there is
			// complete, so the bundle's copy of it is not needed.
			if errors.Is(err, fs.ErrPermission) {
				stats.Skipped++
				continue
			}
			return stats, err
		}
		stats.Files++
		stats.Bytes += f.Size
	}
	return stats, nil
}

func (w spellCacheWant) check(idx spellCacheIndex) error {
	switch {
	case idx.Schema != spellCacheSchema:
		return fmt.Errorf("unsupported bundle schema %d (this magus reads %d)", idx.Schema, spellCacheSchema)
	case idx.Key.Spell != w.key.Spell || idx.Key.Tools != w.key.Tools:
		return fmt.Errorf("bundle is for %s-%s, not %s-%s", idx.Key.Spell, short(idx.Key.Tools), w.key.Spell, short(w.key.Tools))
	case !w.anyLocks && idx.Key.Locks != w.key.Locks:
		return fmt.Errorf("bundle is for lockfiles %s, not %s", short(idx.Key.Locks), short(w.key.Locks))
	case idx.Name != w.name:
		return fmt.Errorf("bundle was signed for key %q, not %q", idx.Name, w.name)
	}
	return nil
}

// localRel reports whether a slash path stays below its root.
func localRel(rel string) bool {
	return rel != "" && filepath.IsLocal(filepath.FromSlash(rel)) && path.Clean(rel) == rel
}

func readLeadMember(tr *tar.Reader, name string, max int64) ([]byte, error) {
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("tar: %w", err)
	}
	if hdr.Name != name || hdr.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("expected %s, found %q; an unsigned or foreign archive", name, hdr.Name)
	}
	budget := max
	return readCapped(tr, &budget)
}

// stageSpellCacheFile writes one file owner-writable so the digest check can fail and
// the staging directory still be removed, then gives it the saved mode.
func stageSpellCacheFile(r io.Reader, dst string, f spellCacheFile, stamp time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, h), r)
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return errors.New("content does not match the signed index")
	}
	return errors.Join(os.Chmod(dst, f.Mode|0o400), os.Chtimes(dst, stamp, stamp))
}

// SpellCacheResult is what a remote save or restore did.
type SpellCacheResult struct {
	SpellCacheStats
	// Key is the remote key stored, found already stored, or restored; "" on a miss.
	Key string
	// Exact reports a restore whose lockfiles matched; false took another set's.
	Exact bool
	// Present reports a save that found the day's key already stored and wrote nothing.
	Present bool
	// Inactive reports a restore that found the remote backend inactive here.
	Inactive bool
	// Transferred is the compressed size moved over the wire.
	Transferred int64
	// Refused lists each bundle that failed verification, and why.
	Refused []string
}

// SaveSpellCache signs a bundle of roots and stores it under today's key, plus a
// pointer that lets a reader with other lockfiles find it. The day's first bundle
// stands: a key that is already stored is left alone, without the bundle being built.
func (c *Cache) SaveSpellCache(ctx context.Context, key SpellCacheKey, roots []SpellCacheRoot) (SpellCacheResult, error) {
	return c.saveSpellCache(ctx, key, roots, time.Now())
}

func (c *Cache) saveSpellCache(ctx context.Context, key SpellCacheKey, roots []SpellCacheRoot, now time.Time) (SpellCacheResult, error) {
	if c.remote == nil {
		return SpellCacheResult{}, errors.New("cache: no remote backend configured")
	}
	if !c.remote.writes() {
		return SpellCacheResult{}, fmt.Errorf("cache: saving a spell cache bundle writes the remote tier, which this run may not write (%s)", c.remote.off)
	}
	if !c.remote.backend.Active(ctx) {
		return SpellCacheResult{}, errors.New("cache: remote backend is not active in this environment")
	}
	name := key.remoteKey(now)
	res := SpellCacheResult{Key: name}
	if has, err := c.remote.backend.HasArtifact(ctx, spellCacheNamespace, name); err == nil && has {
		res.Present = true
		return res, nil
	}

	pr, pw := io.Pipe()
	counted := &CountingReader{Reader: pr}
	statsCh := make(chan SpellCacheStats, 1)
	errCh := make(chan error, 1)
	go func() {
		stats, err := writeSpellCacheBundle(ctx, pw, c.signer, key, name, roots, now)
		_ = pw.CloseWithError(err)
		statsCh <- stats
		errCh <- err
	}()
	putErr := c.remote.backend.PutArtifact(ctx, spellCacheNamespace, name, counted)
	_ = pr.CloseWithError(putErr)
	res.SpellCacheStats = <-statsCh
	if err := <-errCh; err != nil {
		return res, err
	}
	switch {
	case errors.Is(putErr, ErrRemoteExists):
		res.Present = true
		return res, nil
	case putErr != nil:
		return res, putErr
	}
	res.Transferred = counted.N

	ptr, err := json.Marshal(spellCachePointer{Key: name})
	if err != nil {
		return res, err
	}
	if err := c.RemoteNamespace(spellCacheNamespace).Put(ctx, key.pointerKey(now), strings.NewReader(string(ptr))); err != nil {
		return res, fmt.Errorf("cache: stored %s but not its pointer: %w", name, err)
	}
	return res, nil
}

type spellCachePointer struct {
	Key string `json:"key"`
}

// RestoreSpellCache restores the newest verified bundle for key into roots: first one
// of the same lockfiles, then one of any lockfiles from the same tools, each newest day
// first over the last week. A bundle that fails verification is refused, recorded in
// Refused and logged, and the search moves to the next one. Finding nothing is not an
// error: the result's Key is "".
func (c *Cache) RestoreSpellCache(ctx context.Context, key SpellCacheKey, roots []SpellCacheRoot) (SpellCacheResult, error) {
	return c.restoreSpellCache(ctx, key, roots, time.Now())
}

func (c *Cache) restoreSpellCache(ctx context.Context, key SpellCacheKey, roots []SpellCacheRoot, now time.Time) (SpellCacheResult, error) {
	if c.remote == nil {
		return SpellCacheResult{}, errors.New("cache: no remote backend configured")
	}
	if c.verifier == nil {
		return SpellCacheResult{}, errors.New("cache: no trust set configured; a spell cache bundle is restored only once verified")
	}
	// A job the store gives no credentials, a fork's pull request say, builds cold
	// rather than failing over a cache.
	if !c.remote.backend.Active(ctx) {
		return SpellCacheResult{Inactive: true}, nil
	}
	var res SpellCacheResult
	tried := map[string]struct{}{}
	try := func(want spellCacheWant) (bool, error) {
		tried[want.name] = struct{}{}
		rc, err := c.remote.backend.GetArtifact(ctx, spellCacheNamespace, want.name)
		if errors.Is(err, ErrRemoteMiss) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		defer rc.Close()
		counted := &CountingReader{Reader: rc}
		stats, err := readSpellCacheBundle(ctx, counted, c.verifier, want, roots, c.importLimit(), now)
		if err != nil {
			res.Refused = append(res.Refused, want.name+": "+err.Error())
			c.log.WarnContext(ctx, "cache.spell_cache.refused", slog.String("key", want.name), slog.String("error", err.Error()))
			return false, nil
		}
		res.SpellCacheStats, res.Key, res.Transferred, res.Exact = stats, want.name, counted.N, !want.anyLocks
		return true, nil
	}
	for i := range spellCacheLookback {
		ok, err := try(spellCacheWant{key: key, name: key.remoteKey(now.AddDate(0, 0, -i))})
		if ok || err != nil {
			return res, err
		}
	}
	ns := c.RemoteNamespace(spellCacheNamespace)
	for i := range spellCacheLookback {
		pk := key.pointerKey(now.AddDate(0, 0, -i))
		rc, err := ns.Get(ctx, pk)
		if errors.Is(err, ErrRemoteMiss) {
			continue
		}
		if err != nil {
			res.Refused = append(res.Refused, pk+": "+err.Error())
			c.log.WarnContext(ctx, "cache.spell_cache.refused", slog.String("key", pk), slog.String("error", err.Error()))
			continue
		}
		var ptr spellCachePointer
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err == nil {
			err = json.Unmarshal(data, &ptr)
		}
		if err != nil || !strings.HasPrefix(ptr.Key, key.Spell+"-"+short(key.Tools)+"-") {
			continue
		}
		if _, done := tried[ptr.Key]; done {
			continue
		}
		ok, err := try(spellCacheWant{key: key, name: ptr.Key, anyLocks: true})
		if ok || err != nil {
			return res, err
		}
	}
	return res, nil
}
