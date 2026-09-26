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
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/json"
)

// A TOOLCHAIN BUNDLE carries a compiler's own caches (for Go, GOCACHE and GOMODCACHE)
// between machines, so a magus miss recompiles only the packages that changed instead
// of the whole tree. It rides the remote tier under toolchainNamespace and is signed
// and verified exactly like a build entry: a bundle's bytes are executed by the next
// compile, so an unverified one is never restored, and there is no fallback to one.
//
// The layout is a gzip-tar: the index (every file's path, size, digest and exec bit),
// its signature, then the files. The index comes first so a reader authenticates it
// before a single file is written, and checks each file against it as it streams.

const (
	domainToolchain = "magus-toolchain-cache-v1\x00"

	// toolchainNamespace keeps bundles out of every project's key space: no project
	// path begins with "@".
	toolchainNamespace = "@toolchain"
	toolchainIndexName = "toolchain.json"
	toolchainSchema    = 1

	// toolchainLookback is how many daily keys a reader probes, newest first. The store
	// cannot be listed, so "newest" is found by asking for each day in turn.
	toolchainLookback = 7

	maxToolchainIndex = 64 << 20
	maxToolchainSig   = 64 << 10

	// restoredAge backdates every restored file. Go re-stamps a cache entry it uses only
	// when the entry is over an hour old, and trims entries unused for five days, so a
	// day-old stamp makes use visible to a later save without inviting Go's trim.
	restoredAge = 24 * time.Hour
)

// ToolchainKey names a bundle by what makes its entries reusable. Toolchain and Modules
// are hex digests: a reader wants Toolchain to match exactly, and prefers Modules to.
type ToolchainKey struct {
	Tool      string `json:"tool"`
	Toolchain string `json:"toolchain"`
	Modules   string `json:"modules"`
}

// goKeyVars are the `go env` variables that change what Go compiles. GOCACHE keys each
// entry on all of them anyway; the bundle key only chooses a bundle likely to hit.
var goKeyVars = []string{"GOVERSION", "GOOS", "GOARCH", "GOAMD64", "GOARM64", "GOARM", "GOEXPERIMENT", "GOFLAGS", "CGO_ENABLED"}

// GoKeyVars returns the `go env` variables [GoToolchainKey] reads.
func GoKeyVars() []string { return slices.Clone(goKeyVars) }

// GoToolchainKey derives the Go bundle key from `go env` values and the contents of
// every go.sum in the workspace, keyed by workspace-relative path. A variable missing
// from env counts as empty.
func GoToolchainKey(env map[string]string, goSums map[string][]byte) ToolchainKey {
	tc := sha256.New()
	for _, v := range goKeyVars {
		fmt.Fprintf(tc, "%s=%s\n", v, env[v])
	}
	mods := sha256.New()
	paths := make([]string, 0, len(goSums))
	for p := range goSums {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		sum := sha256.Sum256(goSums[p])
		fmt.Fprintf(mods, "%s\x00%x\n", p, sum)
	}
	return ToolchainKey{Tool: "go", Toolchain: hex.EncodeToString(tc.Sum(nil)), Modules: hex.EncodeToString(mods.Sum(nil))}
}

// String is the day-independent part of the key, for a log line.
func (k ToolchainKey) String() string {
	return k.Tool + "-" + short(k.Toolchain) + "-" + short(k.Modules)
}

func (k ToolchainKey) remoteKey(day time.Time) string {
	return k.String() + "-" + day.UTC().Format("20060102")
}

// pointerKey names the day's bundle for this toolchain whatever its module set, so a
// reader whose go.sum changed can still take compiles an older module set saved.
func (k ToolchainKey) pointerKey(day time.Time) string {
	return k.Tool + "-" + short(k.Toolchain) + "-" + day.UTC().Format("20060102")
}

func short(digest string) string {
	if len(digest) > 16 {
		return digest[:16]
	}
	return digest
}

// ToolchainRoot is one directory a bundle carries, filed in the bundle under Name.
type ToolchainRoot struct {
	Name string
	Dir  string
	// TrackUse marks a cache that re-stamps an entry's mtime when it is used, so a
	// save can keep only what the run used.
	TrackUse bool
	// ReadOnly restores files without write permission, as Go's module cache keeps them.
	ReadOnly bool
	// Skip reports whether a slash-separated path under Dir stays out of the bundle.
	Skip func(rel string) bool
}

// GoToolchainRoots returns the roots of a Go bundle.
func GoToolchainRoots(gocache, gomodcache string) []ToolchainRoot {
	return []ToolchainRoot{
		{
			Name:     "gocache",
			Dir:      gocache,
			TrackUse: true,
			// Entries live in two-hex-digit shards; README, trim.txt and fuzz/ do not.
			Skip: func(rel string) bool {
				dir, _, ok := strings.Cut(rel, "/")
				return !ok || len(dir) != 2 || !isHex(dir)
			},
		},
		{
			Name:     "gomodcache",
			Dir:      gomodcache,
			ReadOnly: true,
			// A build reads the extracted tree and its .ziphash, never the zip, so zips
			// would double the bundle for nothing. VCS clones serve only direct fetches.
			Skip: func(rel string) bool {
				switch {
				case strings.HasPrefix(rel, "cache/vcs/"),
					strings.HasPrefix(rel, "cache/download/") && strings.HasSuffix(rel, ".zip"):
					return true
				}
				for _, ext := range []string{".lock", ".partial", ".tmp"} {
					if strings.HasSuffix(rel, ext) {
						return true
					}
				}
				return false
			},
		},
	}
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}

const toolchainStaging = ".magus-toolchain-"

type toolchainIndex struct {
	Schema  int             `json:"schema"`
	Key     ToolchainKey    `json:"key"`
	Name    string          `json:"name,omitempty"` // the remote key it was stored under
	Created time.Time       `json:"created"`
	Files   []toolchainFile `json:"files"`
}

type toolchainFile struct {
	Path   string `json:"path"` // "<root name>/<slash path under the root>"
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Exec   bool   `json:"exec,omitempty"`
}

// ToolchainStats counts one bundle's files.
type ToolchainStats struct {
	Files int   // files written to the bundle, or restored from it
	Bytes int64 // their total size, uncompressed
	// Skipped is, on a save, the tracked-cache entries left out as unused; on a
	// restore, the files already present locally and left as they were.
	Skipped int
}

// collectToolchainFiles walks roots and digests every file a bundle should carry.
func collectToolchainFiles(ctx context.Context, roots []ToolchainRoot, usedWithin time.Duration, now time.Time) ([]toolchainFile, ToolchainStats, error) {
	var (
		files []toolchainFile
		stats ToolchainStats
	)
	cutoff := now.Add(-usedWithin)
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
				if rel != "." && strings.HasPrefix(d.Name(), toolchainStaging) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || (root.Skip != nil && root.Skip(rel)) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if root.TrackUse && usedWithin > 0 && info.ModTime().Before(cutoff) {
				stats.Skipped++
				return nil
			}
			sum, err := digestFile(p)
			if err != nil {
				return err
			}
			files = append(files, toolchainFile{
				Path: root.Name + "/" + rel, Size: info.Size(), SHA256: sum, Exec: info.Mode()&0o111 != 0,
			})
			stats.Files++
			stats.Bytes += info.Size()
			return nil
		})
		if err != nil {
			return nil, ToolchainStats{}, fmt.Errorf("toolchain bundle: %s: %w", root.Name, err)
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

// writeToolchainBundle writes a signed bundle of roots to w. name is the remote key the
// bundle is stored under, bound into the signed index; "" for a file.
func writeToolchainBundle(ctx context.Context, w io.Writer, s *signer, key ToolchainKey, name string, roots []ToolchainRoot, usedWithin time.Duration, now time.Time) (ToolchainStats, error) {
	if s == nil {
		return ToolchainStats{}, errors.New("toolchain bundle: needs a signing key (MAGUS_CACHE_SIGNING_KEY); an unsigned bundle is refused on restore")
	}
	files, stats, err := collectToolchainFiles(ctx, roots, usedWithin, now)
	if err != nil {
		return ToolchainStats{}, err
	}
	idx, err := json.Marshal(toolchainIndex{Schema: toolchainSchema, Key: key, Name: name, Created: now.UTC(), Files: files})
	if err != nil {
		return ToolchainStats{}, err
	}
	sig, err := s.sign(domainToolchain, idx, nil)
	if err != nil {
		return ToolchainStats{}, err
	}
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return ToolchainStats{}, err
	}
	tw := tar.NewWriter(gz)
	for _, m := range []struct {
		name string
		data []byte
	}{{toolchainIndexName, idx}, {sigFileName, sig}} {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: m.name, Size: int64(len(m.data)), Mode: 0o644}); err != nil {
			return ToolchainStats{}, err
		}
		if _, err := tw.Write(m.data); err != nil {
			return ToolchainStats{}, err
		}
	}
	dirs := make(map[string]string, len(roots))
	for _, r := range roots {
		dirs[r.Name] = r.Dir
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return ToolchainStats{}, err
		}
		rootName, rel, _ := strings.Cut(f.Path, "/")
		if err := copyToolchainFile(tw, filepath.Join(dirs[rootName], filepath.FromSlash(rel)), f); err != nil {
			return ToolchainStats{}, fmt.Errorf("toolchain bundle: %s: %w", f.Path, err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		return ToolchainStats{}, err
	}
	return stats, nil
}

func copyToolchainFile(tw *tar.Writer, src string, f toolchainFile) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := int64(0o644)
	if f.Exec {
		mode = 0o755
	}
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: f.Path, Size: f.Size, Mode: mode}); err != nil {
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

// toolchainWant is what a reader accepts: the key and, for a remote read, the name the
// bundle was fetched under. anyModules accepts a bundle of another module set.
type toolchainWant struct {
	key        ToolchainKey
	name       string
	anyModules bool
}

// readToolchainBundle authenticates a bundle and restores it into roots. Nothing is
// placed until every file has been checked against the signed index: a bundle that
// fails at any point leaves the roots as they were.
func readToolchainBundle(ctx context.Context, r io.Reader, v *verifier, want toolchainWant, roots []ToolchainRoot, limit int64, now time.Time) (ToolchainStats, error) {
	if v == nil {
		return ToolchainStats{}, errors.New("no trust set configured; refusing an unauthenticated toolchain bundle")
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return ToolchainStats{}, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	idxBytes, err := readLeadMember(tr, toolchainIndexName, maxToolchainIndex)
	if err != nil {
		return ToolchainStats{}, err
	}
	sig, err := readLeadMember(tr, sigFileName, maxToolchainSig)
	if err != nil {
		return ToolchainStats{}, err
	}
	if err := v.verify(domainToolchain, sig, idxBytes, nil); err != nil {
		return ToolchainStats{}, err
	}
	var idx toolchainIndex
	if err := json.Unmarshal(idxBytes, &idx); err != nil {
		return ToolchainStats{}, fmt.Errorf("index: %w", err)
	}
	if err := want.check(idx); err != nil {
		return ToolchainStats{}, err
	}

	rootsByName := make(map[string]ToolchainRoot, len(roots))
	for _, root := range roots {
		rootsByName[root.Name] = root
	}
	entries := make(map[string]toolchainFile, len(idx.Files))
	var total int64
	for _, f := range idx.Files {
		rootName, rel, ok := strings.Cut(f.Path, "/")
		if _, known := rootsByName[rootName]; !ok || !known || !localRel(rel) {
			return ToolchainStats{}, fmt.Errorf("index names an unsafe or unknown path %q", f.Path)
		}
		if _, dup := entries[f.Path]; dup || f.Size < 0 {
			return ToolchainStats{}, fmt.Errorf("index entry %q is malformed", f.Path)
		}
		entries[f.Path] = f
		total += f.Size
	}
	if total > limit {
		return ToolchainStats{}, fmt.Errorf("bundle holds %d bytes, over the %d-byte import limit", total, limit)
	}

	staging := make(map[string]string, len(roots))
	defer func() {
		for _, dir := range staging {
			_ = os.RemoveAll(dir)
		}
	}()
	for _, root := range roots {
		if err := os.MkdirAll(root.Dir, 0o755); err != nil {
			return ToolchainStats{}, err
		}
		// Inside the root so the commit is a rename on one filesystem.
		dir, err := os.MkdirTemp(root.Dir, toolchainStaging+"*")
		if err != nil {
			return ToolchainStats{}, err
		}
		staging[root.Name] = dir
	}

	stamp := now.Add(-restoredAge)
	seen := make(map[string]struct{}, len(entries))
	for {
		if err := ctx.Err(); err != nil {
			return ToolchainStats{}, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ToolchainStats{}, fmt.Errorf("tar: %w", err)
		}
		f, ok := entries[hdr.Name]
		if !ok || hdr.Typeflag != tar.TypeReg {
			return ToolchainStats{}, fmt.Errorf("member %q is not in the signed index", hdr.Name)
		}
		if _, dup := seen[hdr.Name]; dup {
			return ToolchainStats{}, fmt.Errorf("member %q appears twice", hdr.Name)
		}
		if hdr.Size != f.Size {
			return ToolchainStats{}, fmt.Errorf("member %q is %d bytes, the index says %d", hdr.Name, hdr.Size, f.Size)
		}
		seen[hdr.Name] = struct{}{}
		rootName, rel, _ := strings.Cut(f.Path, "/")
		if err := stageToolchainFile(tr, filepath.Join(staging[rootName], filepath.FromSlash(rel)), f, rootsByName[rootName].ReadOnly, stamp); err != nil {
			return ToolchainStats{}, fmt.Errorf("member %q: %w", hdr.Name, err)
		}
	}
	if len(seen) != len(entries) {
		return ToolchainStats{}, fmt.Errorf("bundle is truncated: %d of %d indexed files present", len(seen), len(entries))
	}

	var stats ToolchainStats
	for _, f := range idx.Files {
		rootName, rel, _ := strings.Cut(f.Path, "/")
		dst := filepath.Join(rootsByName[rootName].Dir, filepath.FromSlash(rel))
		// A present file is Go's own, content addressed or immutable once extracted.
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

func (w toolchainWant) check(idx toolchainIndex) error {
	switch {
	case idx.Schema != toolchainSchema:
		return fmt.Errorf("unsupported bundle schema %d (this magus reads %d)", idx.Schema, toolchainSchema)
	case idx.Key.Tool != w.key.Tool || idx.Key.Toolchain != w.key.Toolchain:
		return fmt.Errorf("bundle is for toolchain %s-%s, not %s-%s", idx.Key.Tool, short(idx.Key.Toolchain), w.key.Tool, short(w.key.Toolchain))
	case !w.anyModules && idx.Key.Modules != w.key.Modules:
		return fmt.Errorf("bundle is for module set %s, not %s", short(idx.Key.Modules), short(w.key.Modules))
	case w.name != "" && idx.Name != w.name:
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

func stageToolchainFile(r io.Reader, dst string, f toolchainFile, readOnly bool, stamp time.Time) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if f.Exec {
		mode = 0o755
	}
	if readOnly {
		mode &^= 0o222
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), r)
	if err = errors.Join(err, out.Close()); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return errors.New("content does not match the signed index")
	}
	return errors.Join(os.Chmod(dst, mode), os.Chtimes(dst, stamp, stamp))
}

// WriteToolchainBundle writes a bundle of roots to w, signed with the Ed25519 seed.
// usedWithin > 0 keeps only the entries of a use-tracking root touched that recently.
func WriteToolchainBundle(ctx context.Context, w io.Writer, seed []byte, key ToolchainKey, roots []ToolchainRoot, usedWithin time.Duration) (ToolchainStats, error) {
	s, err := newSigner(seed)
	if err != nil {
		return ToolchainStats{}, err
	}
	return writeToolchainBundle(ctx, w, s, key, "", roots, usedWithin, time.Now())
}

// ReadToolchainBundle verifies a bundle against the trusted Ed25519 public keys and
// restores it into roots. It refuses a bundle for another toolchain or module set.
func ReadToolchainBundle(ctx context.Context, r io.Reader, trusted [][]byte, key ToolchainKey, roots []ToolchainRoot) (ToolchainStats, error) {
	v, err := newVerifier(trusted)
	if err != nil {
		return ToolchainStats{}, err
	}
	return readToolchainBundle(ctx, r, v, toolchainWant{key: key}, roots, defaultMaxImportBytes, time.Now())
}

// ToolchainResult is what a remote save or restore did.
type ToolchainResult struct {
	ToolchainStats
	// Key is the remote key stored, found already stored, or restored; "" on a miss.
	Key string
	// Exact reports a restore whose module set matched; false took an older one's.
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

// SaveToolchain signs a bundle of roots and stores it under today's key, plus a pointer
// that lets a reader with another module set find it. The day's first bundle stands:
// a key that is already stored is left alone, without the bundle being built.
func (c *Cache) SaveToolchain(ctx context.Context, key ToolchainKey, roots []ToolchainRoot, usedWithin time.Duration) (ToolchainResult, error) {
	return c.saveToolchain(ctx, key, roots, usedWithin, time.Now())
}

func (c *Cache) saveToolchain(ctx context.Context, key ToolchainKey, roots []ToolchainRoot, usedWithin time.Duration, now time.Time) (ToolchainResult, error) {
	if c.remote == nil {
		return ToolchainResult{}, errors.New("cache: no remote backend configured")
	}
	if !c.remote.writes() {
		return ToolchainResult{}, fmt.Errorf("cache: saving a toolchain bundle writes the remote tier, which this run may not write (%s)", c.remote.off)
	}
	if !c.remote.backend.Active(ctx) {
		return ToolchainResult{}, errors.New("cache: remote backend is not active in this environment")
	}
	name := key.remoteKey(now)
	res := ToolchainResult{Key: name}
	if has, err := c.remote.backend.HasArtifact(ctx, toolchainNamespace, name); err == nil && has {
		res.Present = true
		return res, nil
	}

	pr, pw := io.Pipe()
	counted := &CountingReader{Reader: pr}
	statsCh := make(chan ToolchainStats, 1)
	errCh := make(chan error, 1)
	go func() {
		stats, err := writeToolchainBundle(ctx, pw, c.signer, key, name, roots, usedWithin, now)
		_ = pw.CloseWithError(err)
		statsCh <- stats
		errCh <- err
	}()
	putErr := c.remote.backend.PutArtifact(ctx, toolchainNamespace, name, counted)
	_ = pr.CloseWithError(putErr)
	res.ToolchainStats = <-statsCh
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

	ptr, err := json.Marshal(toolchainPointer{Key: name})
	if err != nil {
		return res, err
	}
	if err := c.RemoteNamespace(toolchainNamespace).Put(ctx, key.pointerKey(now), strings.NewReader(string(ptr))); err != nil {
		return res, fmt.Errorf("cache: stored %s but not its pointer: %w", name, err)
	}
	return res, nil
}

type toolchainPointer struct {
	Key string `json:"key"`
}

// RestoreToolchain restores the newest verified bundle for key into roots: first one of
// the same module set, then one of any module set from the same toolchain, each newest
// day first over the last week. A bundle that fails verification is refused, recorded
// in Refused and logged, and the search moves to the next one. Finding nothing is not
// an error: the result's Key is "".
func (c *Cache) RestoreToolchain(ctx context.Context, key ToolchainKey, roots []ToolchainRoot) (ToolchainResult, error) {
	return c.restoreToolchain(ctx, key, roots, time.Now())
}

func (c *Cache) restoreToolchain(ctx context.Context, key ToolchainKey, roots []ToolchainRoot, now time.Time) (ToolchainResult, error) {
	if c.remote == nil {
		return ToolchainResult{}, errors.New("cache: no remote backend configured")
	}
	if c.verifier == nil {
		return ToolchainResult{}, errors.New("cache: no trust set configured; a toolchain bundle is restored only once verified")
	}
	// A job the store gives no credentials, a fork's pull request say, builds cold
	// rather than failing over a cache.
	if !c.remote.backend.Active(ctx) {
		return ToolchainResult{Inactive: true}, nil
	}
	var res ToolchainResult
	tried := map[string]struct{}{}
	try := func(want toolchainWant) (bool, error) {
		tried[want.name] = struct{}{}
		rc, err := c.remote.backend.GetArtifact(ctx, toolchainNamespace, want.name)
		if errors.Is(err, ErrRemoteMiss) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		defer rc.Close()
		counted := &CountingReader{Reader: rc}
		stats, err := readToolchainBundle(ctx, counted, c.verifier, want, roots, c.importLimit(), now)
		if err != nil {
			res.Refused = append(res.Refused, want.name+": "+err.Error())
			c.log.WarnContext(ctx, "cache.toolchain.refused", slog.String("key", want.name), slog.String("error", err.Error()))
			return false, nil
		}
		res.ToolchainStats, res.Key, res.Transferred, res.Exact = stats, want.name, counted.N, !want.anyModules
		return true, nil
	}
	for i := range toolchainLookback {
		ok, err := try(toolchainWant{key: key, name: key.remoteKey(now.AddDate(0, 0, -i))})
		if ok || err != nil {
			return res, err
		}
	}
	ns := c.RemoteNamespace(toolchainNamespace)
	for i := range toolchainLookback {
		pk := key.pointerKey(now.AddDate(0, 0, -i))
		rc, err := ns.Get(ctx, pk)
		if errors.Is(err, ErrRemoteMiss) {
			continue
		}
		if err != nil {
			res.Refused = append(res.Refused, pk+": "+err.Error())
			c.log.WarnContext(ctx, "cache.toolchain.refused", slog.String("key", pk), slog.String("error", err.Error()))
			continue
		}
		var ptr toolchainPointer
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err == nil {
			err = json.Unmarshal(data, &ptr)
		}
		if err != nil || !strings.HasPrefix(ptr.Key, key.Tool+"-"+short(key.Toolchain)+"-") {
			continue
		}
		if _, done := tried[ptr.Key]; done {
			continue
		}
		ok, err := try(toolchainWant{key: key, name: ptr.Key, anyModules: true})
		if ok || err != nil {
			return res, err
		}
	}
	return res, nil
}
