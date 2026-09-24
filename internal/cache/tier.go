package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"time"

	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/json"
)

// A Cache is two tiers under standard two-tier semantics: the local store (L1) and an
// optional remote backend (L2).
//
//   - Reads go L1, then L2. An L1 hit never touches the network.
//   - An L2 hit is verified and promoted into L1, then replayed from L1. With local
//     writes off it replays from a staging directory that is discarded afterwards.
//   - A built entry is stored in L1, then L2, each under its own gate. L2 is never
//     written without L1.
//   - A run that may write L2 backfills it: an L1 hit whose key L2 lacks is pushed
//     asynchronously.
//   - An L2 failure degrades the run to L1 only. It is counted as failed, never as
//     missed, and fails a step only when remote writes were declared required.
//   - Keys are content addressed, so nothing is invalidated; L1 evicts by its size
//     cap, L2 by the backend's retention.
//
// The write gates are decided once, in Open, and live on each tier.

// tier is one level of the cache. Run walks Cache.tiers in order for lookups and stores.
type tier interface {
	name() string
	// writes reports whether this run may store entries in the tier.
	writes() bool
	// lookup returns the entry for hash, or errTierMiss. A tier that failed reports the
	// failure itself before returning it, so the caller only moves on.
	lookup(ctx context.Context, s *Step, hash string) (*entry, error)
	// store files the snapshot, whose blobs are already in the local store. A non-nil
	// error fails the step; a tier whose writes are best effort reports and returns nil.
	store(ctx context.Context, s *Step, snap *Manifest) error
}

// backfiller is a tier that can be handed an entry another tier hit, to store it
// without the run waiting.
type backfiller interface {
	backfill(ctx context.Context, s Step, hash string)
}

// errTierMiss is a lookup that found nothing, or a tier this run does not consult.
var errTierMiss = errors.New("cache: not in this tier")

// entry is a lookup's result: a manifest and the store root holding its blobs and log.
type entry struct {
	manifest *Manifest
	root     string // cache root holding cas/ and logs/ for this entry
	remote   string // backend name when the entry came from the remote tier
	bytes    int64  // bytes transferred to fetch it
	promoted bool   // imported into the local store by this lookup
	release  func() // discards a staging root; nil when there is none
}

func (e *entry) done() {
	if e.release != nil {
		e.release()
	}
}

// localTier is L1: manifests and blobs under the cache directory.
type localTier struct {
	c     *Cache
	write bool
}

func (t *localTier) name() string { return "local" }

func (t *localTier) writes() bool { return t.write }

func (t *localTier) lookup(ctx context.Context, s *Step, hash string) (*entry, error) {
	m, err := t.c.readManifest(s.ProjectPath, hash)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errTierMiss
	}
	if err != nil {
		return nil, err
	}
	if moved := movedStamps(s.WorkspaceRoot, s.Stamps, m.Stamps); len(moved) > 0 {
		slog.DebugContext(ctx, "cache.stamp", slog.String("project", s.ProjectPath),
			slog.String("target", s.Target), slog.Any("moved", moved))
		return nil, errTierMiss
	}
	return &entry{manifest: m, root: t.c.dir}, nil
}

func (t *localTier) store(_ context.Context, s *Step, snap *Manifest) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(t.c.manifestPath(s.ProjectPath, snap.Hash), data); err != nil {
		return fmt.Errorf("magus/cache: store %q: %w", s.ProjectPath, err)
	}
	return nil
}

// remoteTier is L2: a RemoteBackend, read through signed artifacts.
type remoteTier struct {
	c        *Cache
	backend  RemoteBackend
	write    bool
	required bool   // declared explicitly, so a failed store fails the step
	off      string // why writes are off, for the run header; "" when they are on
}

func (t *remoteTier) name() string {
	if n := t.backend.Name(); n != "" {
		return n
	}
	return "remote"
}

func (t *remoteTier) writes() bool { return t.write }

// errRemoteDegraded refuses a required store after the remote tier failed earlier in
// the run: skipping it silently would read as a successful write.
var errRemoteDegraded = errors.New("magus/cache: the remote tier failed earlier in this run and remote writes are required")

// usable reports whether this run may talk to the backend at all, logging the posture
// once per run. A run the remote tier already failed stays local-only.
func (t *remoteTier) usable(ctx context.Context) bool {
	stats := remoteStatsFrom(ctx)
	if stats.isDegraded() {
		return false
	}
	active := t.backend.Active(ctx)
	if stats != nil {
		stats.postureOnce.Do(func() {
			t.c.log.InfoContext(ctx, "cache.remote.posture",
				slog.String("backend", t.name()),
				slog.Bool("active", active),
				slog.Bool("verify", t.c.verifier != nil),
				slog.Bool("sign", t.c.signer != nil))
		})
	}
	return active
}

// fail counts and reports one failed remote operation. A transport failure degrades
// the run to the local tier; a rejected artifact does not, since the store answered.
func (t *remoteTier) fail(ctx context.Context, op string, s *Step, hash string, downloaded int64, degrade bool, err error) {
	stats := remoteStatsFrom(ctx)
	stats.failed(downloaded)
	if degrade {
		stats.degrade()
	}
	t.c.log.WarnContext(ctx, "cache.warn", slog.String("msg",
		fmt.Sprintf("remote %s %s (%s): %v", op, s.ProjectPath, shortHash(hash), err)))
}

func (t *remoteTier) lookup(ctx context.Context, s *Step, hash string) (*entry, error) {
	// A stamped entry vouches for one local tree; see Step.Stamps.
	if len(s.Stamps) > 0 || !t.usable(ctx) {
		return nil, errTierMiss
	}
	// Timed around the whole fetch, import included: GetArtifact returns a stream, so
	// the bytes move while importArtifact reads it.
	start := time.Now()
	defer func() { httpx.RecorderFrom(ctx).Add(time.Since(start)) }()

	r, err := t.backend.GetArtifact(ctx, s.ProjectPath, hash)
	if errors.Is(err, ErrRemoteMiss) {
		remoteStatsFrom(ctx).miss()
		t.c.logRemoteMiss(ctx, s, hash)
		return nil, errTierMiss
	}
	if err != nil {
		t.fail(ctx, "get", s, hash, 0, true, err)
		return nil, err
	}
	defer r.Close()

	root, release, err := t.c.promotionRoot()
	if err != nil {
		t.fail(ctx, "stage", s, hash, 0, false, err)
		return nil, err
	}
	counted := &CountingReader{Reader: r}
	m, err := t.c.importArtifact(ctx, counted, root, s.ProjectPath, hash)
	if err != nil {
		release()
		t.fail(ctx, "import", s, hash, counted.N, false, err)
		return nil, err
	}
	return &entry{
		manifest: m, root: root, remote: t.name(), bytes: counted.N,
		promoted: root == t.c.dir, release: release,
	}, nil
}

func (t *remoteTier) store(ctx context.Context, s *Step, snap *Manifest) error {
	if len(s.Stamps) > 0 {
		return nil
	}
	if remoteStatsFrom(ctx).isDegraded() {
		if t.required {
			return errRemoteDegraded
		}
		return nil
	}
	if !t.usable(ctx) {
		return nil
	}
	err := t.put(ctx, s, snap.Hash)
	if err != nil && t.required {
		return fmt.Errorf("magus/cache: remote writes are required: %w", err)
	}
	return nil
}

// put exports the local entry and uploads it, counting and reporting the outcome.
func (t *remoteTier) put(ctx context.Context, s *Step, hash string) error {
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := t.c.exportArtifact(ctx, s.ProjectPath, hash, pw)
		_ = pw.CloseWithError(err)
		errCh <- err
	}()
	start := time.Now()
	// Counted on the read side: what exportArtifact wrote in would report a full
	// upload for a transfer that died mid-stream.
	counted := &CountingReader{Reader: pr}
	putErr := t.backend.PutArtifact(ctx, s.ProjectPath, hash, counted)
	httpx.RecorderFrom(ctx).Add(time.Since(start))
	_ = pr.CloseWithError(putErr)
	exportErr := <-errCh
	switch {
	case exportErr != nil:
		t.fail(ctx, "store", s, hash, 0, false, exportErr)
		return exportErr
	case errors.Is(putErr, ErrRemoteExists):
		// Another run stored this key, or is storing it now. Content addressing makes
		// either one the same entry, so this is neither a failure nor an upload.
		t.c.log.DebugContext(ctx, "cache.debug", slog.String("msg",
			fmt.Sprintf("remote store %s (%s): already present", s.ProjectPath, shortHash(hash))))
		return nil
	case putErr != nil:
		t.fail(ctx, "store", s, hash, 0, true, putErr)
		return putErr
	}
	remoteStatsFrom(ctx).stored(counted.N)
	t.c.log.InfoContext(ctx, "cache.remote.store",
		slog.String("project", s.ProjectPath),
		slog.String("label", s.Label),
		slog.String("hash", shortHash(hash)),
		slog.Int64("bytes", counted.N),
		slog.Duration("duration", time.Since(start)))
	return nil
}

// backfill pushes an entry this run hit in a lower tier to the remote tier when the
// remote tier lacks it, without delaying the run: RemoteSummary waits for it. It asks
// HasArtifact first so a present entry costs one lookup, not an upload. A backend that
// cannot answer is not backfilled, since uploading every hit to learn the answer costs
// more than the miss it would save.
func (t *remoteTier) backfill(ctx context.Context, s Step, hash string) {
	stats := remoteStatsFrom(ctx)
	if stats == nil || stats.isDegraded() || len(s.Stamps) > 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	stats.pending.Add(1)
	go func() {
		defer stats.pending.Done()
		if !t.usable(ctx) {
			return
		}
		has, err := t.backend.HasArtifact(ctx, s.ProjectPath, hash)
		if errors.Is(err, errors.ErrUnsupported) {
			return
		}
		if err != nil {
			t.fail(ctx, "stat", &s, hash, 0, true, err)
			return
		}
		if has {
			return
		}
		t.c.exportMu.RLock()
		defer t.c.exportMu.RUnlock()
		_ = t.put(ctx, &s, hash)
	}()
}

// promotionRoot is where a remote-tier hit is imported: the local store when this run
// may write it, else a staging directory the returned release removes.
func (c *Cache) promotionRoot() (string, func(), error) {
	if c.local.write {
		return c.dir, func() {}, nil
	}
	dir, err := os.MkdirTemp(c.dir, stagingPrefix)
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// stagingPrefix names a staging directory under the cache root; a crashed run's are
// collected with the other stale files (see collectStale).
const stagingPrefix = "staging-"

// logRemoteMiss names the key a remote-tier lookup missed, as the ref the producing run
// printed, so two machines that should share an entry can be compared by key. At debug
// it adds a digest per key-input class, masked as the stored key inputs are, which says
// WHICH class differs without printing every source path.
func (c *Cache) logRemoteMiss(ctx context.Context, s *Step, hash string) {
	attrs := []any{
		slog.String("project", s.ProjectPath),
		slog.String("label", s.Label),
		slog.String("target", s.Target),
		slog.String("hash", hash),
		slog.String("ref", PortableRef(hash)),
	}
	if c.log.Enabled(ctx, slog.LevelDebug) {
		var lines []string
		// Skipped when the recomputed key differs: those lines describe another key.
		if h, err := c.hashStepInputs(ctx, s, &lines); err == nil && h == hash {
			for _, d := range ClassDigests(MaskKeyInputs(ctx, lines)) {
				attrs = append(attrs, slog.String("inputs."+d.Class, fmt.Sprintf("%s (%d)", d.Digest, d.Count)))
			}
		}
	}
	c.log.InfoContext(ctx, "cache.remote.miss", attrs...)
}

// RemoteNamespace is the remote tier's view for objects outside build entries, such as
// knowledge-graph shards. It applies the tier's decisions rather than bypassing them:
// Put is refused when this run may not write the remote tier, every object is signed
// on the way out, and Get verifies it against the trust set before returning a byte.
type RemoteNamespace struct {
	t  *remoteTier
	ns string
}

// RemoteNamespace returns the remote tier's view of namespace, or nil when the cache
// has no remote tier. namespace must not collide with a project path; the reserved
// ones begin with a character no project path can.
func (c *Cache) RemoteNamespace(namespace string) *RemoteNamespace {
	if c.remote == nil {
		return nil
	}
	return &RemoteNamespace{t: c.remote, ns: namespace}
}

// objectMeta binds a signed object to the (namespace, key) it was stored under, so a
// signature over one shard cannot be served as another.
type objectMeta struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

const (
	objectMetaName    = "object.json"
	objectPayloadName = "object"
)

// Get returns the verified object stored under key, or [ErrRemoteMiss]. An object that
// fails verification is an error, never a miss: the store answered with something no
// trusted producer wrote.
func (n *RemoteNamespace) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if !n.t.backend.Active(ctx) {
		return nil, ErrRemoteMiss
	}
	r, err := n.t.backend.GetArtifact(ctx, n.ns, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	meta, payload, err := n.t.c.readSignedPair(r, domainObject, objectMetaName, objectPayloadName, false)
	if err != nil {
		return nil, fmt.Errorf("remote %s/%s: %w", n.ns, key, err)
	}
	var m objectMeta
	if err := json.Unmarshal(meta, &m); err != nil || m.Namespace != n.ns || m.Key != key {
		return nil, fmt.Errorf("remote %s/%s: object is not bound to this key", n.ns, key)
	}
	return io.NopCloser(bytes.NewReader(payload)), nil
}

// Put signs r and stores it under key. It refuses when this run may not write the
// remote tier, for the reason the run header shows.
func (n *RemoteNamespace) Put(ctx context.Context, key string, r io.Reader) error {
	if !n.t.writes() {
		return fmt.Errorf("remote %s/%s: this run may not write the remote tier (%s)", n.ns, key, n.t.off)
	}
	if !n.t.backend.Active(ctx) {
		return nil
	}
	payload, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	meta, err := json.Marshal(objectMeta{Namespace: n.ns, Key: key})
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := writeSignedPair(n.t.c.signer, &buf, domainObject, objectMetaName, meta, objectPayloadName, payload); err != nil {
		return err
	}
	if err := n.t.backend.PutArtifact(ctx, n.ns, key, &buf); err != nil && !errors.Is(err, ErrRemoteExists) {
		return err
	}
	return nil
}
