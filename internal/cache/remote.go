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
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/secret"
)

// RemoteBackend is a pluggable remote backend for cache artifacts, keyed by (projectPath,
// hash). The local cache consults it on a local miss (before building) and
// populates it after a successful build. The artifact payload is an opaque byte
// stream — its format is the cache's concern, not the store's — so an implementation
// is effectively a content-addressed blob store. Implementations must be safe for
// concurrent use.
type RemoteBackend interface {
	// Name identifies the backend to a human: the spell that provides it ("s3", "gha").
	// It must not probe or dial - the run header calls it before any work, precisely so
	// the header cannot be what makes a run hang.
	Name() string
	// Active reports whether the backend is usable in the current environment.
	// The cache skips both fetch and push when it returns false, so a backend
	// gated on its environment (e.g. one that only runs under a specific CI
	// provider) costs nothing per build elsewhere. Implementations should make it
	// cheap — the cache may call it once per build — and cache any probe.
	Active(ctx context.Context) bool
	// GetArtifact streams the stored artifact for (projectPath, hash). Returns (nil, nil)
	// when no artifact is present.
	GetArtifact(ctx context.Context, projectPath, hash string) (io.ReadCloser, error)
	// PutArtifact stores the artifact bytes for (projectPath, hash) from r.
	PutArtifact(ctx context.Context, projectPath, hash string, r io.Reader) error
}

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

// WithRemoteBackend configures a remote backend that is consulted on local miss.
func WithRemoteBackend(t RemoteBackend) Option {
	return func(c *Cache) { c.remote = t }
}

// PruneRemote evicts remote cache artifacts per policy. It errors when no remote
// backend is configured, when the backend does not implement [RemotePruner], or
// when the backend is inactive in this environment (the same gate fetch/push use,
// so a misconfigured prune fails loudly instead of silently no-opping).
func (c *Cache) PruneRemote(ctx context.Context, policy RetentionPolicy) error {
	if c.remote == nil {
		return errors.New("cache: no remote backend configured")
	}
	pruner, ok := c.remote.(RemotePruner)
	if !ok {
		return fmt.Errorf("cache: remote backend %q does not support prune", c.remote.Name())
	}
	if !c.remote.Active(ctx) {
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

// fetchFromRemote pulls the artifact for (projectPath, hash) from the remote backend
// into the local cache so the normal hit path can replay it. It returns true when
// an artifact was imported. Errors are logged and treated as a miss — a remote
// failure must never fail a build.
//
// It populates the local cache even when the cache is read-only (mutable=false):
// read-only suppresses creating *new* artifacts from local builds, not restoring an
// existing one from the shared store — which is exactly what a PR CI run wants
// (read remote hits, but never publish).
func (c *Cache) fetchFromRemote(ctx context.Context, projectPath, hash string) bool {
	if !c.remoteActive(ctx) {
		return false
	}
	// Timed around the WHOLE fetch, not around GetArtifact alone: the call returns a
	// stream, so the bytes actually move while importArtifact reads it. Timing the call
	// would report a fast fetch for a slow download. Active() is excluded because a
	// backend is asked to make it cheap and cache its probe.
	start := time.Now()
	defer func() { httpx.RecorderFrom(ctx).Add(time.Since(start)) }()

	stats := remoteStatsFrom(ctx)
	r, err := c.remote.GetArtifact(ctx, projectPath, hash)
	if err != nil {
		// Counted: a transport error is the case the summary exists for, and leaving it
		// out let a dead remote end the run on "failures=0" at Info.
		stats.failed(0)
		c.log.WarnContext(ctx, "cache.warn", slog.String("msg",
			fmt.Sprintf("remote get %s (%s): %v", projectPath, shortHash(hash), err)))
		return false
	}
	if r == nil {
		stats.miss()
		return false
	}
	defer r.Close()
	// Counted here rather than trusting the backend: an empty artifact and a 40MB one
	// both "succeed". observability's wrapper counts the same bytes but only when
	// telemetry is on, and this line has to be true either way.
	counted := &countingReader{Reader: r}
	if err := c.importArtifact(ctx, counted, projectPath, hash); err != nil {
		stats.failed(counted.n)
		c.log.WarnContext(ctx, "cache.warn", slog.String("msg",
			fmt.Sprintf("remote import %s (%s): %v", projectPath, shortHash(hash), err)))
		return false
	}
	stats.hit(counted.n)
	// INFO because a reader asking "is the remote doing anything" has no reason to
	// suspect a verbosity flag.
	c.log.InfoContext(ctx, "cache.remote.hit",
		slog.String("project", projectPath),
		slog.String("hash", shortHash(hash)),
		slog.Int64("bytes", counted.n),
		slog.Duration("duration", time.Since(start)))
	return true
}

// countingReader totals bytes actually read.
type countingReader struct {
	io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	c.n += int64(n)
	return n, err
}

// remoteStats totals what ONE RUN did with the remote cache.
//
// Run-scoped and carried on the context, not held on Cache: the daemon reuses one
// Cache per workspace across runs and can serve two adopted runs at once, so fields on
// Cache would report the process's history and interleave concurrent runs. Same shape
// and nil tolerance as httpx.Recorder, so uninstrumented paths need no guard.
type remoteStats struct {
	hits, misses, puts, fails atomic.Int64
	down, up                  atomic.Int64
	postureOnce, readonlyOnce sync.Once
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

func (s *remoteStats) published(n int64) {
	if s != nil {
		s.puts.Add(1)
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

type remoteStatsKey struct{}

// WithRemoteStats installs run-scoped remote-cache counters on ctx.
func WithRemoteStats(ctx context.Context) context.Context {
	return context.WithValue(ctx, remoteStatsKey{}, &remoteStats{})
}

func remoteStatsFrom(ctx context.Context) *remoteStats {
	s, _ := ctx.Value(remoteStatsKey{}).(*remoteStats)
	return s
}

// remoteActive reports whether the backend engaged, logging the posture once per run
// as a side effect. The inactive case earns that line: in the header a dormant backend
// and a working one are identical, and this is the probe LogCache refuses to make.
func (c *Cache) remoteActive(ctx context.Context) bool {
	active := c.remote.Active(ctx)
	if s := remoteStatsFrom(ctx); s != nil {
		s.postureOnce.Do(func() { c.logPosture(ctx, active) })
	}
	return active
}

func (c *Cache) logPosture(ctx context.Context, active bool) {
	name := c.remote.Name()
	if name == "" {
		name = "remote"
	}
	c.log.InfoContext(ctx, "cache.remote.posture",
		slog.String("backend", name),
		slog.Bool("active", active),
		slog.Bool("verify", c.verifier != nil),
		slog.Bool("sign", c.signer != nil))
}

// pushToRemote exports the local artifact for (projectPath, hash) and uploads it.
// Errors are logged but not returned: a failed push is not a build failure.
func (c *Cache) pushToRemote(ctx context.Context, s Step, hash string) {
	if !c.remoteActive(ctx) {
		return // skip the export entirely when the backend is inactive
	}
	// Trust set configured but no signing key: an unsigned push would be rejected
	// by every verifier, so don't publish. This is what stops a consumer-only
	// machine (a laptop, a PR runner) from writing the shared store at all.
	//
	// Said out loud because it is also the half-finished CI setup: trust set declared,
	// signing secret forgotten, store stays empty while every run reports success.
	stats := remoteStatsFrom(ctx)
	if c.verifier != nil && c.signer == nil {
		if stats != nil {
			stats.readonlyOnce.Do(func() { c.log.InfoContext(ctx, "cache.remote.readonly") })
		}
		return
	}
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := c.exportArtifact(ctx, s.ProjectPath, hash, pw)
		_ = pw.CloseWithError(err)
		errCh <- err
	}()
	putStart := time.Now()
	// Counted on the read side: what exportArtifact wrote in would report a full
	// upload for a transfer that died mid-stream.
	counted := &countingReader{Reader: pr}
	putErr := c.remote.PutArtifact(ctx, s.ProjectPath, hash, counted)
	// The upload streams from the pipe, so PutArtifact spans the transfer and this is
	// the real wait. Waiting to publish is still waiting.
	httpx.RecorderFrom(ctx).Add(time.Since(putStart))
	_ = pr.CloseWithError(putErr)
	exportErr := <-errCh
	if exportErr != nil || putErr != nil {
		stats.failed(0)
		c.log.WarnContext(ctx, "cache.warn", slog.String("msg",
			fmt.Sprintf("remote push %s (%s): export=%v put=%v", s.ProjectPath, shortHash(hash), exportErr, putErr)))
		return
	}
	stats.published(counted.n)
	c.log.InfoContext(ctx, "cache.remote.push",
		slog.String("project", s.ProjectPath),
		slog.String("hash", shortHash(hash)),
		slog.Int64("bytes", counted.n),
		slog.Duration("duration", time.Since(putStart)))
}

// LogRemoteSummary accounts for what the remote cache did this run, reading the
// run-scoped counters off ctx. It is the only place the ZERO case gets stated: a run
// that never touched a configured remote says so rather than saying nothing, and
// silence is what made that indistinguishable from working.
func (c *Cache) LogRemoteSummary(ctx context.Context) {
	stats := remoteStatsFrom(ctx)
	if c.remote == nil || stats == nil {
		return
	}
	fails := stats.fails.Load()
	attrs := []any{
		slog.Int64("hits", stats.hits.Load()),
		slog.Int64("misses", stats.misses.Load()),
		slog.Int64("published", stats.puts.Load()),
		slog.Int64("failures", fails),
		slog.Int64("down_bytes", stats.down.Load()),
		slog.Int64("up_bytes", stats.up.Load()),
	}
	// Warn so a run whose remote degraded cannot end on a line that reads like success.
	if fails > 0 {
		c.log.WarnContext(ctx, "cache.remote.summary", attrs...)
		return
	}
	c.log.InfoContext(ctx, "cache.remote.summary", attrs...)
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
	// cache-relative path form - exactly what the signature commits to. cas blobs are
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
	// RecordReturn), so masking it on disk would make a hit differ from a miss - the
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
	// manifest bytes actually shipped above (not the on-disk file - a verifier checks
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

// importArtifact extracts a gzip-tar artifact (produced by exportArtifact) into the local
// cache directory, verifying its integrity before any of it becomes usable.
//
// The store is not trusted to return what it was given. Two checks enforce that:
//
//   - Every CAS blob's bytes must hash to the name it is stored under, so a store
//     serving content not matching its content-address is rejected.
//   - The manifest is committed (renamed into place) only after every blob it
//     references has been verified present, so a corrupt artifact never leaves a
//     readable manifest behind — the import fails and the build runs locally.
//
// Authenticity (that a trusted producer made this artifact) is the signature gate
// below; these checks only guarantee what lands on disk is internally consistent.
// wantProject and wantHash are the (project, key) the artifact was REQUESTED for.
// Every path it writes is checked against them, and the parsed manifest must name
// them too, so a signed artifact fetched for one key can never file itself under
// another - the check that makes a signature mean "this entry", not merely "some
// trusted producer signed something".
func (c *Cache) importArtifact(ctx context.Context, r io.Reader, wantProject, wantHash string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("importArtifact: gzip: %w", err)
	}
	defer gz.Close()

	var (
		manifestTmp   string // staged manifest; renamed into place only on success
		manifestFinal string
		manifestBytes []byte
		sigBytes      []byte                      // signature.json, buffered for verification (never persisted)
		seenBlobs     = make(map[string]struct{}) // verified blob hashes present in the tar
		committed     bool
		// Extra members (log, descriptor, key inputs) are STAGED, never written to
		// their final path during the scan: they are unauthenticated until the
		// signature gate below, and a rejected artifact must leave nothing behind.
		extras      []stagedMember
		extraDigest = map[string]string{} // cache-relative path -> content sha256, for the signature
	)
	// Drop the staged manifest and any staged extras on failure so a partial import
	// is never usable and a rejected artifact leaves no trace.
	defer func() {
		if committed {
			return
		}
		if manifestTmp != "" {
			_ = os.Remove(manifestTmp)
		}
		for _, e := range extras {
			_ = os.Remove(e.tmp)
		}
	}()

	// The store is untrusted, so cap the whole archive — not each member — against a
	// decompression bomb: budget is the remaining byte allowance shared across all
	// members, and members are counted so a flood of tiny ones can't exhaust inodes.
	// These writes happen before the signature gate, so the cap must be pre-auth.
	budget := c.importLimit()
	members := 0

	tr := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("importArtifact: tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if members++; members > maxImportMembers {
			return fmt.Errorf("importArtifact: artifact has too many members (>%d)", maxImportMembers)
		}
		clean, err := c.safeCachePath(hdr.Name)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
			return fmt.Errorf("importArtifact: mkdir: %w", err)
		}
		// Classify on the sanitized path, not the raw header name, so a crafted name
		// (e.g. "manifests/../cas/x") can't be filed under one namespace while it
		// writes to another.
		rel, err := filepath.Rel(c.dir, clean)
		if err != nil {
			return fmt.Errorf("importArtifact: rel: %w", err)
		}
		rel = filepath.ToSlash(rel)
		switch {
		case rel == sigFileName:
			// Detached signature: buffer for verification, never persist to the cache.
			// One per artifact — a duplicate is a malformed/hostile archive.
			if sigBytes != nil {
				return errors.New("importArtifact: artifact has more than one signature")
			}
			buf, err := readCapped(tr, &budget)
			if err != nil {
				return fmt.Errorf("importArtifact: read signature: %w", err)
			}
			sigBytes = buf
		case strings.HasPrefix(rel, "cas/"):
			sum, err := c.writeCacheFile(tr, clean, &budget)
			if err != nil {
				return err
			}
			if want := path.Base(rel); sum != want {
				_ = os.Remove(clean)
				return fmt.Errorf("importArtifact: blob %s content hashes to %s", want, sum)
			}
			seenBlobs[sum] = struct{}{}
		case rel == path.Join("manifests", flattenPath(wantProject), wantHash+".json"):
			// Buffer + stage the manifest; commit is deferred until its blobs verify.
			// One per artifact — a duplicate would shadow the first (leaking its temp
			// file) and muddy "one signature authenticates the whole artifact".
			if manifestBytes != nil {
				return errors.New("importArtifact: artifact has more than one manifest")
			}
			buf, err := readCapped(tr, &budget)
			if err != nil {
				return fmt.Errorf("importArtifact: read manifest: %w", err)
			}
			manifestBytes = buf
			manifestFinal = clean
			manifestTmp = clean + ".import.tmp"
			if err := os.WriteFile(manifestTmp, buf, 0o644); err != nil {
				return fmt.Errorf("importArtifact: stage manifest: %w", err)
			}
		case rel == path.Join("logs", flattenPath(wantProject), wantHash+".log") ||
			strings.HasPrefix(rel, path.Join("outputs", wantHash)+"/"):
			// The build log and the portable-ref sidecars, both scoped to the entry
			// being imported so an artifact cannot write over another key's records.
			// Staged beside their final path and committed only after the signature
			// covers them, so an unsigned or tampered extra never lands where a later
			// run would read it. The staging suffix carries the member index, so two
			// members can never contend for one temp path.
			tmp := fmt.Sprintf("%s.import-%d.tmp", clean, len(extras))
			sum, err := c.writeCacheFile(tr, tmp, &budget)
			if err != nil {
				return err
			}
			extras = append(extras, stagedMember{tmp: tmp, final: clean})
			extraDigest[rel] = sum
		default:
			return fmt.Errorf("importArtifact: artifact carries an out-of-scope member %q", rel)
		}
	}

	if manifestBytes == nil {
		return errors.New("importArtifact: artifact has no manifest")
	}
	// Authenticity gate: with a trust set configured, refuse any artifact that isn't
	// signed by a trusted key over this manifest — before committing it, so an
	// unsigned/untrusted/tampered artifact degrades to a local build, never a replay.
	if c.verifier != nil {
		if sigBytes == nil {
			return errors.New("importArtifact: artifact is unsigned; refusing (trust set configured)")
		}
		legacy, err := c.verifier.verify(domainArtifact, sigBytes, manifestBytes, extraDigest)
		if err != nil {
			return fmt.Errorf("importArtifact: %w", err)
		}
		if legacy {
			// compat: see sigAlg in signing.go. A pre-domain producer signed only the
			// manifest, so its log is unauthenticated: keep the entry (it still
			// replays) and drop the extras rather than reject an artifact every
			// released magus produces. Delete this branch with sigAlg.
			extras = nil
		}
	} else {
		// No trust set (an explicitly insecure remote): nothing authenticates the
		// extras, so drop them rather than write unverified bytes where a later run
		// would replay them as this machine's own output.
		extras = nil
	}
	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return fmt.Errorf("importArtifact: parse manifest: %w", err)
	}
	// Bind the signed bytes to the identity they are being filed under. Without this
	// a signature only says "a trusted key signed some JSON": an output bundle's
	// metadata, re-tarred as a manifest, unmarshals to an all-zero Manifest with no
	// outputs, passes every remaining check, and replays as a successful entry - so a
	// published FAILING run would become a teammate's cached pass.
	if m.ProjectPath != wantProject || m.Hash != wantHash {
		return fmt.Errorf("importArtifact: manifest names %q/%s but was served for %q/%s",
			m.ProjectPath, shortHash(m.Hash), wantProject, shortHash(wantHash))
	}
	// The remote store is shared across machines and CI is Linux while local dev
	// may not be - src: lines are content hashes, so two platforms compute the
	// SAME digest for the same commit, and this is the only gate stopping one
	// platform's pass from being imported and replayed as a pass for code the
	// importing platform never compiled. Same empty-matches-anything convention
	// as readManifest, for the same reason: an artifact exported before this
	// field existed should not be rejected outright.
	if m.Platform != "" && m.Platform != c.platform {
		return fmt.Errorf("importArtifact: manifest platform %q does not match running platform %q; refusing import",
			m.Platform, c.platform)
	}
	for _, out := range m.Outputs {
		if out.Blob == "" {
			continue // symlink record carries no blob
		}
		if _, ok := seenBlobs[out.Blob]; !ok {
			return fmt.Errorf("importArtifact: manifest references blob %s absent from artifact", shortHash(out.Blob))
		}
	}
	// Commit the authenticated extras first, then the manifest: the manifest landing
	// is what makes the entry replayable, so everything it implies must already be in
	// place. A failed extra rename is not fatal - the entry still replays, it just
	// resolves under a locally-minted ref.
	for _, e := range extras {
		if err := os.MkdirAll(filepath.Dir(e.final), 0o755); err != nil {
			continue
		}
		_ = os.Rename(e.tmp, e.final)
	}
	if err := os.Rename(manifestTmp, manifestFinal); err != nil {
		return fmt.Errorf("importArtifact: commit manifest: %w", err)
	}
	committed = true
	return nil
}

// stagedMember is an extra artifact file written to a temp path during the tar scan
// and renamed into place only after the signature authenticates it.
type stagedMember struct {
	tmp   string // staged path
	final string // where it belongs once authenticated
}

// maxImportMembers caps the number of files in a single remote artifact, so a flood
// of tiny members can't exhaust inodes. Far above any legitimate artifact (manifest +
// signature + one blob per output + log).
const maxImportMembers = 1 << 20

// errImportTooLarge is returned when a remote artifact's total extracted size exceeds
// the import limit — the decompression-bomb guard, applied across the whole archive.
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

// writeCacheFile streams r to dst atomically (temp + rename), drawing from the
// shared archive budget so the whole artifact — not each member — is bounded against a
// decompression bomb. It returns the SHA-256 hex of the bytes written, which lets a
// CAS blob be checked against the name it is stored under; the build log ignores it.
func (c *Cache) writeCacheFile(r io.Reader, dst string, budget *int64) (string, error) {
	tmp := dst + ".import.tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("importArtifact: create: %w", err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, *budget+1))
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("importArtifact: write: %w", err)
	}
	if n > *budget {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", errImportTooLarge
	}
	*budget -= n
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
