package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/secret"
)

// RemoteBackend is the store behind the remote tier: an opaque blob store keyed by
// (namespace, key). Build entries use the project path as the namespace and the cache
// key as the key; output bundles and knowledge shards use reserved namespaces no
// project can occupy. The payload's format is the cache's concern, not the store's.
// Implementations must be safe for concurrent use.
type RemoteBackend interface {
	// Name identifies the backend to a human: the spell that provides it ("s3", "gha").
	// It must not probe or dial: the run header calls it before any work, precisely so
	// the header cannot be what makes a run hang.
	Name() string
	// Active reports whether the backend is usable in the current environment.
	// The cache skips every call when it returns false, so a backend gated on its
	// environment (e.g. one that only runs under a specific CI provider) costs nothing
	// per build elsewhere. Implementations should make it cheap and cache any probe.
	Active(ctx context.Context) bool
	// GetArtifact streams the stored bytes for (namespace, key). It returns
	// [ErrRemoteMiss] when nothing is stored, and any other error when the store could
	// not answer, which the cache counts as a failure rather than a miss.
	GetArtifact(ctx context.Context, namespace, key string) (io.ReadCloser, error)
	// PutArtifact stores r under (namespace, key). It returns [ErrRemoteExists] when
	// the key is already stored or another writer is storing it: content addressing
	// makes that the same bytes, so the cache counts it as neither an upload nor a
	// failure.
	PutArtifact(ctx context.Context, namespace, key string, r io.Reader) error
	// HasArtifact reports whether (namespace, key) is stored, without transferring it.
	// A backend that cannot answer returns [errors.ErrUnsupported]; the cache then
	// skips backfilling the remote tier rather than uploading to find out.
	HasArtifact(ctx context.Context, namespace, key string) (bool, error)
}

// ErrRemoteMiss is GetArtifact's answer when the store holds nothing for the key.
var ErrRemoteMiss = errors.New("cache: not in the remote store")

// ErrRemoteExists is PutArtifact's answer when the key is already stored, or is being
// stored by another writer.
var ErrRemoteExists = errors.New("cache: already in the remote store")

// RetentionPolicy describes which remote cache artifacts a prune should evict. The
// two bounds are independent and additive: an artifact is evicted if it is older than
// OlderThan OR falls outside the newest KeepLast. A zero field disables that bound.
type RetentionPolicy struct {
	OlderThan time.Duration // evict artifacts older than this; 0 disables the age bound
	KeepLast  int           // keep only the newest N artifacts; 0 disables the count bound
	DryRun    bool          // report intended deletions without performing them
}

// RemotePruner is an optional capability a [RemoteBackend] may implement to support
// retention-based eviction (`magus config cache prune --remote`). A backend that
// does not implement it cannot be pruned — the cache's built-in eviction governs
// only the local store. PruneArtifacts enumerates the remote store and evicts artifacts
// matching policy; it runs out of band (a maintenance command), never on the build
// hot path.
type RemotePruner interface {
	PruneArtifacts(ctx context.Context, policy RetentionPolicy) error
}

// WithRemoteBackend configures the backend behind the remote tier.
func WithRemoteBackend(t RemoteBackend) Option {
	return func(c *Cache) { c.backend = t }
}

// PruneRemote evicts remote cache artifacts per policy. It errors when no remote
// backend is configured, when the backend does not implement [RemotePruner], or
// when the backend is inactive in this environment (the same gate fetch/push use,
// so a misconfigured prune fails loudly instead of silently no-opping).
func (c *Cache) PruneRemote(ctx context.Context, policy RetentionPolicy) error {
	if c.remote == nil {
		return errors.New("cache: no remote backend configured")
	}
	pruner, ok := c.remote.backend.(RemotePruner)
	if !ok {
		return fmt.Errorf("cache: remote backend %q does not support prune", c.remote.name())
	}
	if !c.remote.backend.Active(ctx) {
		return errors.New("cache: remote backend is not active in this environment")
	}
	return pruner.PruneArtifacts(ctx, policy)
}

// remoteBackendOpener builds a RemoteBackend from an opaque selector string. It is an
// extension point, not a hard dependency: a backend that needs the Buzz VM (the
// spell-backed store) lives in the interp bindings layer and registers itself here
// at init, so this low-level package stays free of the VM. A build that does not
// link such a backend leaves it nil.
var remoteBackendOpener func(ctx context.Context, selector string) (RemoteBackend, error)

// RegisterRemoteBackendOpener installs the opener that [OpenRemoteBackend] delegates to.
// It is meant to be called once, from a backend package's init; a second call
// panics rather than silently shadowing the first.
func RegisterRemoteBackendOpener(fn func(ctx context.Context, selector string) (RemoteBackend, error)) {
	if remoteBackendOpener != nil {
		panic("cache: remote backend opener already registered")
	}
	remoteBackendOpener = fn
}

// OpenRemoteBackend opens the registered remote backend for selector, or errors when no
// opener has been registered (no backend was linked into this binary).
func OpenRemoteBackend(ctx context.Context, selector string) (RemoteBackend, error) {
	if remoteBackendOpener == nil {
		return nil, errors.New("cache: no remote backend registered in this binary; the spell-backed opener is wired by internal/interp/bindings (blank-imported by cmd/magus), so a program that uses this package as a library without that import never registers one")
	}
	return remoteBackendOpener(ctx, selector)
}

// CountingReader totals the bytes actually read through it: what a remote transfer
// moved, rather than what either end claims. Exported for the telemetry wrapper, which
// meters the same streams.
type CountingReader struct {
	io.Reader
	N int64
}

func (c *CountingReader) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.N += int64(n)
	return n, err
}

// remoteStats totals what ONE RUN did with the remote tier.
//
// Run-scoped and carried on the context, not held on Cache: the daemon reuses one
// Cache per workspace across runs and can serve two adopted runs at once, so fields on
// Cache would report the process's history and interleave concurrent runs. Same shape
// and nil tolerance as httpx.Recorder, so uninstrumented paths need no guard.
type remoteStats struct {
	hits, misses, stores, fails atomic.Int64
	down, up                    atomic.Int64
	degraded                    atomic.Bool // a transport failure: the rest of the run is local-only
	postureOnce                 sync.Once
	pending                     sync.WaitGroup // backfills still uploading
}

// Nil-safe METHODS rather than a nil-safe helper taking &s.field: the argument
// expression dereferences before the callee can check, which segfaults on the
// uninstrumented paths this type exists to tolerate.
func (s *remoteStats) hit(n int64) {
	if s != nil {
		s.hits.Add(1)
		s.down.Add(n)
	}
}

func (s *remoteStats) miss() {
	if s != nil {
		s.misses.Add(1)
	}
}

func (s *remoteStats) stored(n int64) {
	if s != nil {
		s.stores.Add(1)
		s.up.Add(n)
	}
}

// downloaded records bytes that transferred before a failure, so a run that pulled
// 400MB of rejected artifacts does not report down=0.
func (s *remoteStats) failed(downloaded int64) {
	if s != nil {
		s.fails.Add(1)
		s.down.Add(downloaded)
	}
}

func (s *remoteStats) degrade() {
	if s != nil {
		s.degraded.Store(true)
	}
}

func (s *remoteStats) isDegraded() bool { return s != nil && s.degraded.Load() }

type remoteStatsKey struct{}

// ContextWithRemoteStats installs run-scoped remote-tier counters on ctx.
func ContextWithRemoteStats(ctx context.Context) context.Context {
	return context.WithValue(ctx, remoteStatsKey{}, &remoteStats{})
}

func remoteStatsFrom(ctx context.Context) *remoteStats {
	s, _ := ctx.Value(remoteStatsKey{}).(*remoteStats)
	return s
}

// RemoteTally is what the remote tier did during one run. Its JSON is the body of the
// report stream's run.remote record.
type RemoteTally struct {
	Hits      int64 `json:"hits"`
	Misses    int64 `json:"misses"`
	Stored    int64 `json:"stored"`
	Failures  int64 `json:"failures"`
	DownBytes int64 `json:"down_bytes"`
	UpBytes   int64 `json:"up_bytes"`
}

// RemoteSummary reads the run-scoped remote counters off ctx, after waiting for the
// run's backfill uploads so they are counted. It reports false when no remote is
// configured or ctx carries no counters (a caller outside Magus.Run); a configured
// remote the run never touched reports true with every count zero. A cancelled ctx
// stops the wait and reports what has finished.
func (c *Cache) RemoteSummary(ctx context.Context) (RemoteTally, bool) {
	stats := remoteStatsFrom(ctx)
	if c.remote == nil || stats == nil {
		return RemoteTally{}, false
	}
	waited := make(chan struct{})
	go func() {
		stats.pending.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
	}
	return RemoteTally{
		Hits:      stats.hits.Load(),
		Misses:    stats.misses.Load(),
		Stored:    stats.stores.Load(),
		Failures:  stats.fails.Load(),
		DownBytes: stats.down.Load(),
		UpBytes:   stats.up.Load(),
	}, true
}

// exportArtifact writes a gzip-tar containing the manifest, its blobs, the captured
// build log, and the run's portable-ref sidecars (output descriptor + key inputs) for
// (projectPath, hash). Every non-manifest member is recorded in the signature
// envelope's Members map, so the whole artifact is authenticated rather than just its
// manifest and blobs.
func (c *Cache) exportArtifact(ctx context.Context, projectPath, hash string, w io.Writer) error {
	manifest, err := c.readManifest(projectPath, hash)
	if err != nil {
		return fmt.Errorf("exportArtifact: read manifest: %w", err)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	// Content hash of every extra (non-manifest, non-cas, non-signature) member, in
	// cache-relative path form: exactly what the signature commits to. cas blobs are
	// omitted: the manifest already names each by its content address.
	members := map[string]string{}

	addBytes := func(rel string, data []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     filepath.ToSlash(rel),
			Size:     int64(len(data)),
			Mode:     0o644,
		}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	addFile := func(absPath string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(c.dir, absPath)
		if err != nil {
			return err
		}
		// Stream rather than os.ReadFile: this runs at the end of every cacheable
		// miss, and a blob-sized buffer per export adds up on a large artifact.
		f, err := os.Open(absPath)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     filepath.ToSlash(rel),
			Size:     info.Size(),
			Mode:     0o644,
		}); err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return err
	}

	// addSigned adds an extra member and records its digest for the signature. Unlike
	// addFile it is used for everything outside manifests/ and cas/.
	addSigned := func(rel string, data []byte) error {
		if err := addBytes(rel, data); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		members[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	}

	// The manifest is the one file redacted on the way out rather than on the way in.
	// Its Return field is replayed verbatim into a cache HIT (see cache.go's
	// RecordReturn), so masking it on disk would make a hit differ from a miss: the
	// invariant types/returns.go exists to hold. Export crosses into a SHARED store,
	// which is the trust boundary that actually warrants the scrub, so it happens here
	// and the local entry stays replayable.
	manifestBytes, err := os.ReadFile(c.manifestPath(projectPath, hash))
	if err != nil {
		return fmt.Errorf("exportArtifact: read manifest: %w", err)
	}
	manifestBytes = secret.Redact(ctx, manifestBytes)
	manifestRel, err := filepath.Rel(c.dir, c.manifestPath(projectPath, hash))
	if err != nil {
		return fmt.Errorf("exportArtifact: manifest path: %w", err)
	}
	if err := addBytes(manifestRel, manifestBytes); err != nil {
		return fmt.Errorf("exportArtifact: manifest: %w", err)
	}

	seen := make(map[string]struct{})
	for _, out := range manifest.Outputs {
		if out.Blob == "" {
			continue
		}
		if _, ok := seen[out.Blob]; ok {
			continue
		}
		seen[out.Blob] = struct{}{}
		if err := addFile(c.blobPath(out.Blob)); err != nil {
			return fmt.Errorf("exportArtifact: blob %s: %w", out.Blob, err)
		}
	}

	// The build log, redacted like the manifest because it crosses the same trust
	// boundary. Absent is fine (a quiet target captures nothing); a read error is not
	// worth failing a push over.
	if logBytes, err := os.ReadFile(c.logPath(projectPath, hash)); err == nil {
		rel, relErr := filepath.Rel(c.dir, c.logPath(projectPath, hash))
		if relErr != nil {
			return fmt.Errorf("exportArtifact: log path: %w", relErr)
		}
		if err := addSigned(rel, secret.Redact(ctx, logBytes)); err != nil {
			return fmt.Errorf("exportArtifact: log: %w", err)
		}
	}

	// The portable-ref sidecars: the newest attempt's descriptor and the step's key
	// lines. With these, a machine importing this artifact resolves the SAME ref the
	// producer printed, and can diff its key against the producer's, instead of
	// minting a fresh local ref for identical inputs.
	if c.outputs != nil {
		if desc, err := c.outputs.newestDescriptor(hash); err == nil {
			if data, mErr := json.Marshal(desc); mErr == nil {
				rel := path.Join("outputs", hash, desc.Attempt+descExt)
				if err := addSigned(rel, secret.Redact(ctx, data)); err != nil {
					return fmt.Errorf("exportArtifact: descriptor: %w", err)
				}
			}
		}
		if lines, err := os.ReadFile(filepath.Join(c.dir, "outputs", hash, keyInputsName)); err == nil {
			if err := addSigned(path.Join("outputs", hash, keyInputsName), lines); err != nil {
				return fmt.Errorf("exportArtifact: key inputs: %w", err)
			}
		}
	}

	// Signed LAST, once every member's digest is known: the signature covers the
	// manifest bytes actually shipped above (not the on-disk file; a verifier checks
	// what it received, and the shipped manifest is redacted) plus the member map, so
	// one signature authenticates the whole artifact. No signing key -> unsigned,
	// which no verifying consumer will accept.
	if c.signer != nil {
		sig, err := c.signer.sign(domainArtifact, manifestBytes, members)
		if err != nil {
			return fmt.Errorf("exportArtifact: sign: %w", err)
		}
		if err := addBytes(sigFileName, sig); err != nil {
			return fmt.Errorf("exportArtifact: signature: %w", err)
		}
	}

	return errors.Join(tw.Close(), gz.Close())
}

// importArtifact extracts a gzip-tar artifact (produced by exportArtifact) into the
// store at root, verifying it before any of it becomes usable, and returns the
// manifest it committed. root is the local store when this run may write it, else a
// staging directory.
//
// The store is not trusted to return what it was given:
//
//   - Every CAS blob's bytes must hash to the name it is stored under, so a store
//     serving content not matching its content-address is rejected.
//   - The manifest is committed (renamed into place) only after every blob it
//     references has been verified present, so a corrupt artifact never leaves a
//     readable manifest behind — the import fails and the build runs locally.
//
// Authenticity (that a trusted producer made this artifact) is the signature gate
// below. wantProject and wantHash are the (project, key) the artifact was REQUESTED
// for: every path it writes is checked against them and parseManifest requires the
// manifest to name them, so a signed artifact fetched for one key can never file
// itself under another.
func (c *Cache) importArtifact(ctx context.Context, r io.Reader, root, wantProject, wantHash string) (*Manifest, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("importArtifact: gzip: %w", err)
	}
	defer gz.Close()

	var (
		manifest      stagedMember // staged; renamed into place only on success
		manifestBytes []byte
		sigBytes      []byte                      // signature.json, buffered for verification (never persisted)
		seenBlobs     = make(map[string]struct{}) // verified blob hashes present in the tar
		committed     bool
		// Extra members (log, descriptor, key inputs) are STAGED, never written to
		// their final path during the scan: they are unauthenticated until the
		// signature gate below, and a rejected artifact must leave nothing behind.
		extras      []stagedMember
		extraDigest = map[string]string{} // store-relative path -> content sha256, for the signature
	)
	defer func() {
		if committed {
			return
		}
		if manifest.tmp != "" {
			_ = os.Remove(manifest.tmp)
		}
		dropStagedExtras(extras)
	}()

	// The store is untrusted, so cap the whole archive — not each member — against a
	// decompression bomb: budget is the remaining byte allowance shared across all
	// members, and members are counted so a flood of tiny ones can't exhaust inodes.
	// These writes happen before the signature gate, so the cap must be pre-auth.
	budget := c.importLimit()
	members := 0
	manifestRel := path.Join("manifests", flattenPath(wantProject), wantHash+".json")
	logRel := path.Join("logs", flattenPath(wantProject), wantHash+".log")

	tr := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("importArtifact: tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if members++; members > maxImportMembers {
			return nil, fmt.Errorf("importArtifact: artifact has too many members (>%d)", maxImportMembers)
		}
		clean, err := safePathIn(root, hdr.Name)
		if err != nil {
			return nil, err
		}
		// Classify on the sanitized path, not the raw header name, so a crafted name
		// (e.g. "manifests/../cas/x") can't be filed under one namespace while it
		// writes to another.
		rel, err := filepath.Rel(root, clean)
		if err != nil {
			return nil, fmt.Errorf("importArtifact: rel: %w", err)
		}
		rel = filepath.ToSlash(rel)
		switch {
		case rel == sigFileName:
			// Detached signature: buffer for verification, never persist to the cache.
			// One per artifact — a duplicate is a malformed/hostile archive.
			if sigBytes != nil {
				return nil, errors.New("importArtifact: artifact has more than one signature")
			}
			buf, err := readCapped(tr, &budget)
			if err != nil {
				return nil, fmt.Errorf("importArtifact: read signature: %w", err)
			}
			sigBytes = buf
		case strings.HasPrefix(rel, "cas/"):
			sum, err := writeCacheFile(tr, clean, path.Base(rel), &budget)
			if err != nil {
				return nil, err
			}
			seenBlobs[sum] = struct{}{}
		case rel == manifestRel:
			// One per artifact — a duplicate would shadow the first and muddy "one
			// signature authenticates the whole artifact". Buffered as it is staged:
			// the signature covers these exact bytes.
			if manifest.tmp != "" {
				return nil, errors.New("importArtifact: artifact has more than one manifest")
			}
			var buf bytes.Buffer
			tmp, _, err := stageCacheFile(io.TeeReader(tr, &buf), clean, "", &budget)
			if err != nil {
				return nil, err
			}
			manifest = stagedMember{tmp: tmp, final: clean}
			manifestBytes = buf.Bytes()
		case rel == logRel || strings.HasPrefix(rel, path.Join("outputs", wantHash)+"/"):
			// The build log and the portable-ref sidecars, both scoped to the entry
			// being imported so an artifact cannot write over another key's records.
			tmp, sum, err := stageCacheFile(tr, clean, "", &budget)
			if err != nil {
				return nil, err
			}
			extras = append(extras, stagedMember{tmp: tmp, final: clean})
			extraDigest[rel] = sum
		default:
			return nil, fmt.Errorf("importArtifact: artifact carries an out-of-scope member %q", rel)
		}
	}

	if manifest.tmp == "" {
		return nil, errors.New("importArtifact: artifact has no manifest")
	}
	// Authenticity gate: with a trust set configured, refuse any artifact that isn't
	// signed by a trusted key over this manifest — before committing it, so an
	// unsigned/untrusted/tampered artifact degrades to a local build, never a replay.
	if c.verifier != nil {
		if sigBytes == nil {
			return nil, errors.New("importArtifact: artifact is unsigned; refusing (trust set configured)")
		}
		legacy, err := c.verifier.verify(domainArtifact, sigBytes, manifestBytes, extraDigest)
		if err != nil {
			return nil, fmt.Errorf("importArtifact: %w", err)
		}
		if legacy {
			// compat: see sigAlg in signing.go. A pre-domain producer signed only the
			// manifest, so its log is unauthenticated: keep the entry (it still
			// replays) and drop the extras rather than reject an artifact every
			// released magus produces. Delete this branch with sigAlg.
			dropStagedExtras(extras)
			extras = nil
		}
	} else {
		// No trust set (an explicitly insecure remote): nothing authenticates the
		// extras, so drop them rather than write unverified bytes where a later run
		// would replay them as this machine's own output.
		dropStagedExtras(extras)
		extras = nil
	}
	m, err := c.parseManifest(manifestBytes, wantProject, wantHash)
	if err != nil {
		return nil, fmt.Errorf("importArtifact: %w", err)
	}
	for _, out := range m.Outputs {
		if out.Blob == "" {
			continue // symlink record carries no blob
		}
		if _, ok := seenBlobs[out.Blob]; !ok {
			return nil, fmt.Errorf("importArtifact: manifest references blob %s absent from artifact", shortHash(out.Blob))
		}
	}
	// Commit the authenticated extras first, then the manifest: the manifest landing
	// is what makes the entry replayable, so everything it implies must already be in
	// place. A failed extra rename is not fatal: the entry still replays, it just
	// resolves under a locally-minted ref.
	for _, e := range extras {
		_ = os.Rename(e.tmp, e.final)
	}
	if err := os.Rename(manifest.tmp, manifest.final); err != nil {
		return nil, fmt.Errorf("importArtifact: commit manifest: %w", err)
	}
	committed = true
	return m, nil
}

// stagedMember is an artifact file written to a temp path during the tar scan and
// renamed into place only after the signature authenticates it.
type stagedMember struct {
	tmp   string // staged path
	final string // where it belongs once authenticated
}

// dropStagedExtras removes the staged temp files of extras that will never be
// committed. The temp names are unique per import, so an uncollected drop would
// accumulate rather than be overwritten.
func dropStagedExtras(extras []stagedMember) {
	for _, e := range extras {
		_ = os.Remove(e.tmp)
	}
}

// maxImportMembers caps the number of files in a single remote artifact, so a flood
// of tiny members can't exhaust inodes. Far above any legitimate artifact (manifest +
// signature + one blob per output + log).
const maxImportMembers = 1 << 20

// errImportTooLarge is returned when an archive's extracted size exceeds the import
// limit — the decompression-bomb guard.
var errImportTooLarge = errors.New("importArtifact: artifact exceeds import size limit")

// readCapped reads one tar member fully, drawing from the shared archive budget and
// failing if the member would push the running total past the import limit.
func readCapped(r io.Reader, budget *int64) ([]byte, error) {
	buf, err := io.ReadAll(io.LimitReader(r, *budget+1))
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > *budget {
		return nil, errImportTooLarge
	}
	*budget -= int64(len(buf))
	return buf, nil
}

// writeCacheFile is stageCacheFile plus the commit: dst is replaced only once its
// bytes passed the size cap and, when wantSum is set, the content-address check, so a
// corrupt blob never destroys the valid one other manifests still reference.
func writeCacheFile(r io.Reader, dst, wantSum string, budget *int64) (string, error) {
	tmp, sum, err := stageCacheFile(r, dst, wantSum, budget)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return sum, nil
}

// stageCacheFile streams r to a uniquely named temp file beside dst and returns that
// path for the caller to rename (or remove) once dst's bytes are trusted, plus their
// SHA-256 hex. It draws from budget, so a whole archive is bounded against a
// decompression bomb, and refuses content that does not hash to wantSum when set.
// The name is unique per call: the store is shared machine-wide, and a fixed name
// would let a concurrent importer swap its bytes under this one's check.
func stageCacheFile(r io.Reader, dst, wantSum string, budget *int64) (string, string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", "", fmt.Errorf("magus/cache: stage %s: %w", dst, err)
	}
	f, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".import-*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("magus/cache: stage %s: %w", dst, err)
	}
	tmp := f.Name()
	fail := func(err error) (string, string, error) {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, *budget+1))
	if err != nil {
		return fail(fmt.Errorf("magus/cache: stage %s: %w", dst, err))
	}
	if n > *budget {
		return fail(errImportTooLarge)
	}
	*budget -= n
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", "", err
	}
	// CreateTemp makes the file 0600; everything in the store is 0644.
	if err := file.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("magus/cache: stage %s: %w", dst, err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if wantSum != "" && sum != wantSum {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("magus/cache: blob %s content hashes to %s", wantSum, sum)
	}
	return tmp, sum, nil
}
