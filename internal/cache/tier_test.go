package cache

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingBackend wraps a real store and counts every call, so a test can say the
// network was not touched rather than infer it. putErr, when set, is what every put
// answers.
type countingBackend struct {
	RemoteBackend
	gets, puts, has atomic.Int64
	putErr          error
}

func (b *countingBackend) GetArtifact(ctx context.Context, ns, key string) (io.ReadCloser, error) {
	b.gets.Add(1)
	return b.RemoteBackend.GetArtifact(ctx, ns, key)
}

func (b *countingBackend) PutArtifact(ctx context.Context, ns, key string, r io.Reader) error {
	b.puts.Add(1)
	if b.putErr != nil {
		_, _ = io.Copy(io.Discard, r)
		return b.putErr
	}
	return b.RemoteBackend.PutArtifact(ctx, ns, key, r)
}

func (b *countingBackend) HasArtifact(ctx context.Context, ns, key string) (bool, error) {
	b.has.Add(1)
	return b.RemoteBackend.HasArtifact(ctx, ns, key)
}

func newCountingBackend(t *testing.T) (*countingBackend, string) {
	t.Helper()
	dir := t.TempDir()
	fs, err := NewFSRemoteBackend(dir)
	require.NoError(t, err)
	return &countingBackend{RemoteBackend: fs}, dir
}

// openTiered opens a cache over backend signed with one key and trusting it, then
// applies opts.
func openTiered(t *testing.T, backend RemoteBackend, sign bool, opts ...Option) *Cache {
	t.Helper()
	pub, seed := genKeypair(t)
	all := []Option{WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub})}
	if sign {
		all = append(all, WithSigningKey(seed))
	}
	c, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"), append(all, opts...)...)
	require.NoError(t, err)
	return c
}

// storedIn reports whether the FS store at dir holds the entry for hash.
func storedIn(t *testing.T, dir, hash string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, flattenPath("test/pkg"), hash+".tar.gz"))
	return err == nil
}

// Rule 1, priority: an L1 hit never reads L2. A run that may not write L2 does not
// touch the network at all.
func TestTierL1HitNeverReadsL2(t *testing.T) {
	backend, _ := newCountingBackend(t)
	c := openTiered(t, backend, false)
	root := t.TempDir()
	_, ran := buildIn(t, ContextWithRemoteStats(t.Context()), root, c)
	require.True(t, ran)
	gets := backend.gets.Load()

	r, ran := buildIn(t, ContextWithRemoteStats(t.Context()), root, c)
	assert.True(t, r.Hit)
	assert.False(t, ran)
	assert.Equal(t, gets, backend.gets.Load(), "an L1 hit fetched from L2")
	assert.Zero(t, backend.has.Load()+backend.puts.Load(), "a read-only L2 was touched")
}

// Rule 2, read-through with promotion: an L2 hit lands in L1, so the next run is an
// L1 hit with no L2 involved.
func TestTierL2HitIsPromotedIntoL1(t *testing.T) {
	backend, _ := newCountingBackend(t)
	pub, seed := genKeypair(t)
	producer, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}), WithSigningKey(seed))
	require.NoError(t, err)
	_, ran := buildIn(t, ContextWithRemoteStats(t.Context()), t.TempDir(), producer)
	require.True(t, ran)

	consumer, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}))
	require.NoError(t, err)
	root := t.TempDir()
	r, ran := buildIn(t, ContextWithRemoteStats(t.Context()), root, consumer)
	require.True(t, r.Hit)
	require.False(t, ran)
	_, err = consumer.readManifest("test/pkg", r.Hash)
	require.NoError(t, err, "the L2 hit was not promoted into L1")

	gets := backend.gets.Load()
	r, _ = buildIn(t, ContextWithRemoteStats(t.Context()), root, consumer)
	assert.True(t, r.Hit)
	assert.Equal(t, gets, backend.gets.Load(), "the second run went to L2 again")
}

// Rule 2, promotion is governed by the local write gate: with local writes off an L2
// hit replays from staging, and nothing of it remains in the cache directory.
func TestTierL2HitWithLocalWriteOffIsNotPersisted(t *testing.T) {
	backend, _ := newCountingBackend(t)
	pub, seed := genKeypair(t)
	producer, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}), WithSigningKey(seed))
	require.NoError(t, err)
	_, ran := buildIn(t, ContextWithRemoteStats(t.Context()), t.TempDir(), producer)
	require.True(t, ran)

	dir := filepath.Join(t.TempDir(), ".magus")
	consumer, err := Open(t.Context(), dir,
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}), WithLocalWrite(false))
	require.NoError(t, err)
	root := t.TempDir()
	r, ran := buildIn(t, ContextWithRemoteStats(t.Context()), root, consumer)
	require.True(t, r.Hit, "the L2 hit replays from staging")
	require.False(t, ran)
	got, err := os.ReadFile(filepath.Join(root, "test", "pkg", "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "built", string(got))

	_, err = consumer.readManifest("test/pkg", r.Hash)
	assert.ErrorIs(t, err, os.ErrNotExist, "a read-only run persisted the L2 hit")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), stagingPrefix), "staging %s was left behind", e.Name())
	}
}

// Rule 3, write-through: a built entry goes to L1 then L2, each under its own gate, and
// L2 is never written without L1.
func TestTierWriteThrough(t *testing.T) {
	for name, tc := range map[string]struct {
		opts         []Option
		wantL1, want bool
	}{
		"both":       {wantL1: true, want: true},
		"local only": {opts: []Option{WithRemoteWrite(false)}, wantL1: true},
		"neither":    {opts: []Option{WithLocalWrite(false)}},
		"no signer":  {opts: nil, wantL1: true},
	} {
		t.Run(name, func(t *testing.T) {
			backend, dir := newCountingBackend(t)
			c := openTiered(t, backend, name != "no signer", tc.opts...)
			r, ran := buildIn(t, ContextWithRemoteStats(t.Context()), t.TempDir(), c)
			require.True(t, ran)
			_, err := c.readManifest("test/pkg", r.Hash)
			assert.Equal(t, tc.wantL1, err == nil, "L1")
			assert.Equal(t, tc.want, storedIn(t, dir, r.Hash), "L2")
		})
	}
}

// Rule 3, the gates are decided in Open, and a declaration that cannot be honored is
// an error there rather than a quiet no-op later.
func TestTierOpenRefusesAnUnhonorableRemoteWrite(t *testing.T) {
	backend, _ := newCountingBackend(t)
	pub, _ := genKeypair(t)
	_, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}),
		WithLocalWrite(false), WithRemoteWrite(true))
	assert.ErrorContains(t, err, "local writes are off")

	_, err = Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}), WithRemoteWrite(true))
	assert.ErrorContains(t, err, "no signing key")
}

// Rule 4, backfill: on a run that may write L2, an L1 hit whose key L2 lacks is stored
// in L2 without the run waiting, and one L2 already holds costs a lookup, not an upload.
func TestTierBackfillsL2FromAnL1Hit(t *testing.T) {
	backend, dir := newCountingBackend(t)
	root := t.TempDir()
	c := openTiered(t, backend, true, WithRemoteWrite(false))
	r, ran := buildIn(t, ContextWithRemoteStats(t.Context()), root, c)
	require.True(t, ran)
	require.False(t, storedIn(t, dir, r.Hash))

	// Same cache directory, now allowed to write L2.
	writer, err := Open(t.Context(), c.dir, WithRemoteBackend(backend),
		WithTrustedKeys(c.trustedKeys), WithSigningKey(c.signingSeed))
	require.NoError(t, err)
	ctx := ContextWithRemoteStats(t.Context())
	r, ran = buildIn(t, ctx, root, writer)
	require.True(t, r.Hit)
	require.False(t, ran)
	tally, ok := writer.RemoteSummary(ctx)
	require.True(t, ok)
	assert.Equal(t, int64(1), tally.Stored, "the L1 hit was not backfilled")
	assert.True(t, storedIn(t, dir, r.Hash))

	puts := backend.puts.Load()
	ctx = ContextWithRemoteStats(t.Context())
	buildIn(t, ctx, root, writer)
	writer.RemoteSummary(ctx)
	assert.Equal(t, puts, backend.puts.Load(), "an entry L2 holds was uploaded again")
}

// Rule 5, failure isolation: an unreachable L2 is one failure, never a miss, and the
// rest of the run stays local-only instead of paying the timeout again.
func TestTierUnreachableL2DegradesTheRun(t *testing.T) {
	gets := &atomic.Int64{}
	c := openTiered(t, errRemote{gets: gets}, true)
	ctx := ContextWithRemoteStats(t.Context())
	_, ran := buildIn(t, ctx, t.TempDir(), c)
	require.True(t, ran)
	_, ran = buildIn(t, ctx, t.TempDir(), c)
	require.False(t, ran, "the second workspace hits L1")

	stats := remoteStatsFrom(ctx)
	assert.Equal(t, int64(1), stats.fails.Load(), "one failure, then the run stops asking")
	assert.Zero(t, stats.misses.Load(), "an error is not the store saying it has nothing")
	assert.Equal(t, int64(1), gets.Load())
}

// Rule 5, a declared remote write is a requirement: its failure fails the step.
func TestTierRequiredRemoteWriteFailsTheStep(t *testing.T) {
	backend, _ := newCountingBackend(t)
	backend.putErr = errors.New("unreachable")
	c := openTiered(t, backend, true, WithRemoteWrite(true))
	root := t.TempDir()
	writeMain(t, root, "package main")
	touchOut(t, root)
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	_, err := c.Run(ContextWithRemoteStats(t.Context()), step, func(context.Context) error {
		return os.WriteFile(filepath.Join(root, "test", "pkg", "out.txt"), []byte("built"), 0o644)
	})
	assert.ErrorContains(t, err, "remote writes are required")
}

// Rule 6, each tier evicts independently: L1 evicting an entry leaves L2's copy, which
// the next run restores.
func TestTierL1EvictionLeavesL2(t *testing.T) {
	backend, dir := newCountingBackend(t)
	c := openTiered(t, backend, true)
	root := t.TempDir()
	r, _ := buildIn(t, ContextWithRemoteStats(t.Context()), root, c)
	c.evictOldest(t.Context(), 1)
	_, err := c.readManifest("test/pkg", r.Hash)
	require.Error(t, err, "L1 evicted the entry")
	assert.True(t, storedIn(t, dir, r.Hash), "L1 eviction reached L2")

	ctx := ContextWithRemoteStats(t.Context())
	r, ran := buildIn(t, ctx, root, c)
	assert.True(t, r.Hit)
	assert.False(t, ran)
	assert.Equal(t, int64(1), remoteStatsFrom(ctx).hits.Load(), "restored from L2")
}

// A remote hit is ONE event, cache.hit carrying the tier, counted only once its replay
// succeeded.
func TestTierRemoteHitIsOneEventAfterReplay(t *testing.T) {
	backend, _ := newCountingBackend(t)
	pub, seed := genKeypair(t)
	producer, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}), WithSigningKey(seed))
	require.NoError(t, err)
	buildIn(t, ContextWithRemoteStats(t.Context()), t.TempDir(), producer)

	consumer, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}))
	require.NoError(t, err)
	var buf bytes.Buffer
	consumer.log = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	buildIn(t, ContextWithRemoteStats(t.Context()), t.TempDir(), consumer)

	assert.Equal(t, 1, strings.Count(buf.String(), `"msg":"cache.hit"`))
	assert.NotContains(t, buf.String(), `cache.remote.hit`)
	hit := logRecord(t, &buf, "cache.hit")
	assert.Equal(t, "fs", hit["remote"])
	assert.Positive(t, hit["bytes"])

	// A replay that fails is a failure, not a hit: the output path is a non-empty
	// directory, which replay cannot remove.
	root := t.TempDir()
	writeMain(t, root, "package main")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test", "pkg", "out.txt", "keep"), 0o755))
	other, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}))
	require.NoError(t, err)
	ctx := ContextWithRemoteStats(t.Context())
	step := makeStep(root)
	step.Outputs = []string{"test/pkg/out.txt"}
	_, _ = other.Run(ctx, step, func(context.Context) error { return nil })
	stats := remoteStatsFrom(ctx)
	assert.Zero(t, stats.hits.Load(), "a hit was counted before its replay succeeded")
	assert.Equal(t, int64(1), stats.fails.Load(), "the failed replay is the remote tier's failure")
}

// A local replay that fails (here a blob missing from L1) tries L2 once before rebuilding.
func TestTierLocalReplayFailureTriesL2(t *testing.T) {
	backend, _ := newCountingBackend(t)
	c := openTiered(t, backend, true)
	root := t.TempDir()
	r, _ := buildIn(t, ContextWithRemoteStats(t.Context()), root, c)
	m, err := c.readManifest("test/pkg", r.Hash)
	require.NoError(t, err)
	require.NoError(t, os.Remove(c.blobPath(m.Outputs[0].Blob)))

	ctx := ContextWithRemoteStats(t.Context())
	r, ran := buildIn(t, ctx, root, c)
	assert.True(t, r.Hit)
	assert.False(t, ran, "rebuilt instead of trying L2")
	assert.Equal(t, int64(1), remoteStatsFrom(ctx).hits.Load())
}

// A promotion counts against the local size cap like any other write.
func TestTierPromotionEvicts(t *testing.T) {
	backend, _ := newCountingBackend(t)
	pub, seed := genKeypair(t)
	producer, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}), WithSigningKey(seed))
	require.NoError(t, err)
	buildIn(t, ContextWithRemoteStats(t.Context()), t.TempDir(), producer)

	// A 2 MiB entry, written by an uncapped cache over the same directory.
	dir := filepath.Join(t.TempDir(), ".magus")
	uncapped, err := Open(t.Context(), dir)
	require.NoError(t, err)
	big := makeStep(t.TempDir())
	big.Target = "big"
	big.Outputs = []string{"test/pkg/big"}
	writeMain(t, big.WorkspaceRoot, "package big")
	touchOut(t, big.WorkspaceRoot)
	rb, err := uncapped.Run(t.Context(), big, func(context.Context) error {
		return os.WriteFile(filepath.Join(big.WorkspaceRoot, "test", "pkg", "big"), bytes.Repeat([]byte("x"), 2<<20), 0o644)
	})
	require.NoError(t, err)

	consumer, err := Open(t.Context(), dir,
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}), WithSizeMB(1))
	require.NoError(t, err)
	_, err = consumer.readManifest("test/pkg", rb.Hash)
	require.NoError(t, err)

	r, _ := buildIn(t, ContextWithRemoteStats(t.Context()), t.TempDir(), consumer)
	require.True(t, r.Hit)
	_, err = consumer.readManifest("test/pkg", rb.Hash)
	assert.Error(t, err, "the promotion did not evict down to the cap")
}

// A put the store answers with ErrRemoteExists is neither an upload nor a failure.
func TestTierAlreadyStoredIsNeitherUploadNorFailure(t *testing.T) {
	backend, _ := newCountingBackend(t)
	backend.putErr = ErrRemoteExists
	c := openTiered(t, backend, true)
	ctx := ContextWithRemoteStats(t.Context())
	buildIn(t, ctx, t.TempDir(), c)
	stats := remoteStatsFrom(ctx)
	assert.Equal(t, int64(1), backend.puts.Load())
	assert.Zero(t, stats.stores.Load())
	assert.Zero(t, stats.up.Load())
	assert.Zero(t, stats.fails.Load())
}

// Knowledge shards and other RemoteNamespace objects are signed on the way out,
// verified on the way in, and not written by a run that may not write the remote tier.
func TestRemoteNamespaceIsSignedVerifiedAndGated(t *testing.T) {
	backend, _ := newCountingBackend(t)
	pub, seed := genKeypair(t)
	writer, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}), WithSigningKey(seed))
	require.NoError(t, err)
	ns := writer.RemoteNamespace("@shards")
	require.NoError(t, ns.Put(t.Context(), "k1", strings.NewReader("shard bytes")))
	rc, err := ns.Get(t.Context(), "k1")
	require.NoError(t, err)
	got, _ := io.ReadAll(rc)
	assert.Equal(t, "shard bytes", string(got))

	_, err = ns.Get(t.Context(), "absent")
	assert.ErrorIs(t, err, ErrRemoteMiss)

	// Served under another key, the signature still verifies, and the binding refuses it.
	raw, err := backend.RemoteBackend.GetArtifact(t.Context(), "@shards", "k1")
	require.NoError(t, err)
	data, _ := io.ReadAll(raw)
	require.NoError(t, backend.RemoteBackend.PutArtifact(t.Context(), "@shards", "k2", bytes.NewReader(data)))
	_, err = ns.Get(t.Context(), "k2")
	assert.ErrorContains(t, err, "not bound to this key")

	// A different trust set refuses it outright.
	otherPub, _ := genKeypair(t)
	reader, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{otherPub}))
	require.NoError(t, err)
	_, err = reader.RemoteNamespace("@shards").Get(t.Context(), "k1")
	assert.Error(t, err)

	// No signing key, so the remote tier is read-only: the put is refused.
	err = reader.RemoteNamespace("@shards").Put(t.Context(), "k3", strings.NewReader("x"))
	assert.ErrorContains(t, err, "may not write the remote tier")
}

// A bundle publish is a remote-tier write, so a run that may not write it refuses.
func TestPublishOutputRefusedWithRemoteWriteOff(t *testing.T) {
	backend, _ := newCountingBackend(t)
	c := openTiered(t, backend, true, WithRemoteWrite(false))
	_, err := c.PublishOutput(t.Context(), "out0123")
	assert.ErrorContains(t, err, "remote writes are off")
	assert.Zero(t, backend.puts.Load())
}

// Every file an import commits is 0644 like the store's own writes, whether it arrived
// through a remote-tier promotion or a whole-cache Import: CreateTemp's 0600 must not
// leak into the store.
func TestImportedFilesAre0644(t *testing.T) {
	backend, _ := newCountingBackend(t)
	pub, seed := genKeypair(t)
	producer, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}), WithSigningKey(seed))
	require.NoError(t, err)
	buildIn(t, ContextWithRemoteStats(t.Context()), t.TempDir(), producer)

	promoted, err := Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		WithRemoteBackend(backend), WithTrustedKeys([][]byte{pub}))
	require.NoError(t, err)
	r, _ := buildIn(t, ContextWithRemoteStats(t.Context()), t.TempDir(), promoted)
	require.True(t, r.Hit)

	var archive bytes.Buffer
	require.NoError(t, producer.Export(t.Context(), &archive))
	imported := testCacheDir(t)
	require.NoError(t, imported.Import(t.Context(), &archive))

	for _, c := range []*Cache{promoted, imported} {
		for _, sub := range []string{"cas", "manifests", "logs"} {
			_ = filepath.WalkDir(filepath.Join(c.dir, sub), func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				info, err := d.Info()
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(), "%s", p)
				return nil
			})
		}
	}
}

// One validator for every tier: a local manifest that does not name its own key is
// refused exactly as an imported one is.
func TestLocalManifestWithoutIdentityIsRefused(t *testing.T) {
	c := testCacheDir(t)
	hash := strings.Repeat("a", 64)
	p := c.manifestPath("test/pkg", hash)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(`{"outputs":[]}`), 0o644))
	_, err := c.readManifest("test/pkg", hash)
	assert.ErrorContains(t, err, "but was read for")
}
