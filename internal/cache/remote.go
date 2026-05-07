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
	"time"

	"github.com/egladman/magus/internal/codec"
)

// RemoteBackend is a pluggable remote backend for cache artifacts, keyed by (projectPath,
// hash). The local cache consults it on a local miss (before building) and
// populates it after a successful build. The artifact payload is an opaque byte
// stream — its format is the cache's concern, not the store's — so an implementation
// is effectively a content-addressed blob store. Implementations must be safe for
// concurrent use.
type RemoteBackend interface {
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
		return errors.New("cache: remote backend does not support prune")
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
		return nil, errors.New("cache: no remote backend registered in this binary")
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
	if !c.remote.Active(ctx) {
		return false
	}
	r, err := c.remote.GetArtifact(ctx, projectPath, hash)
	if err != nil {
		c.log.Warn("cache.warn", slog.String("msg",
			fmt.Sprintf("remote get %s (%s): %v", projectPath, shortHash(hash), err)))
		return false
	}
	if r == nil {
		return false // remote miss
	}
	defer r.Close()
	if err := c.importArtifact(ctx, r); err != nil {
		c.log.Warn("cache.warn", slog.String("msg",
			fmt.Sprintf("remote import %s (%s): %v", projectPath, shortHash(hash), err)))
		return false
	}
	return true
}

// pushToRemote exports the local artifact for (projectPath, hash) and uploads it.
// Errors are logged but not returned: a failed push is not a build failure.
func (c *Cache) pushToRemote(ctx context.Context, s Spec, hash string) {
	if !c.remote.Active(ctx) {
		return // skip the export entirely when the backend is inactive
	}
	// Trust set configured but no signing key: an unsigned push would be rejected
	// by every verifier, so don't publish. This is what stops a consumer-only
	// machine (a laptop, a PR runner) from writing the shared store at all.
	if c.verifier != nil && c.signer == nil {
		return
	}
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := c.exportArtifact(ctx, s.ProjectPath, hash, pw)
		pw.CloseWithError(err)
		errCh <- err
	}()
	putErr := c.remote.PutArtifact(ctx, s.ProjectPath, hash, pr)
	_ = pr.CloseWithError(putErr)
	exportErr := <-errCh
	if exportErr != nil || putErr != nil {
		c.log.Warn("cache.warn", slog.String("msg",
			fmt.Sprintf("remote push %s (%s): export=%v put=%v", s.ProjectPath, shortHash(hash), exportErr, putErr)))
	}
}

// exportArtifact writes a gzip-tar containing the manifest, its blobs, and (if
// present) the captured build log for (projectPath, hash).
func (c *Cache) exportArtifact(ctx context.Context, projectPath, hash string, w io.Writer) error {
	manifest, err := c.readManifest(projectPath, hash)
	if err != nil {
		return fmt.Errorf("exportArtifact: read manifest: %w", err)
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	addFile := func(absPath string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(c.dir, absPath)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(absPath)
		if err != nil {
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
		_, err = tw.Write(data)
		return err
	}

	if err := addFile(c.manifestPath(projectPath, hash)); err != nil {
		return fmt.Errorf("exportArtifact: manifest: %w", err)
	}

	// Sign the manifest's exact on-disk bytes and ship the detached signature.
	// No signing key → unsigned, which no verifying consumer will accept.
	if c.signer != nil {
		mb, err := os.ReadFile(c.manifestPath(projectPath, hash))
		if err != nil {
			return fmt.Errorf("exportArtifact: read manifest for signing: %w", err)
		}
		sig, err := c.signer.sign(mb)
		if err != nil {
			return fmt.Errorf("exportArtifact: sign: %w", err)
		}
		if err := tw.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg,
			Name:     sigFileName,
			Size:     int64(len(sig)),
			Mode:     0o644,
		}); err != nil {
			return fmt.Errorf("exportArtifact: signature header: %w", err)
		}
		if _, err := tw.Write(sig); err != nil {
			return fmt.Errorf("exportArtifact: signature write: %w", err)
		}
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

	// Include build log if present; errors are silently ignored (log is optional).
	_ = addFile(c.logPath(projectPath, hash))

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
func (c *Cache) importArtifact(ctx context.Context, r io.Reader) error {
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
	)
	// Drop the staged manifest on any failure so a partial import is never usable.
	defer func() {
		if !committed && manifestTmp != "" {
			_ = os.Remove(manifestTmp)
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
		case strings.HasPrefix(rel, "manifests/"):
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
		default:
			// The optional build log, or any other artifact: write atomically as-is.
			if _, err := c.writeCacheFile(tr, clean, &budget); err != nil {
				return err
			}
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
		if err := c.verifier.verify(sigBytes, manifestBytes); err != nil {
			return fmt.Errorf("importArtifact: %w", err)
		}
	}
	var m Manifest
	if err := codec.Unmarshal(manifestBytes, &m); err != nil {
		return fmt.Errorf("importArtifact: parse manifest: %w", err)
	}
	for _, out := range m.Outputs {
		if out.Blob == "" {
			continue // symlink record carries no blob
		}
		if _, ok := seenBlobs[out.Blob]; !ok {
			return fmt.Errorf("importArtifact: manifest references blob %s absent from artifact", shortHash(out.Blob))
		}
	}
	if err := os.Rename(manifestTmp, manifestFinal); err != nil {
		return fmt.Errorf("importArtifact: commit manifest: %w", err)
	}
	committed = true
	return nil
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
