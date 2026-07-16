package knowledge

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRemote is an in-memory RemoteShards for tests: content-addressed by key.
type fakeRemote struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newFakeRemote() *fakeRemote { return &fakeRemote{blobs: map[string][]byte{}} }

func (f *fakeRemote) PutShard(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.blobs[key] = b
	f.mu.Unlock()
	return nil
}

func (f *fakeRemote) GetShard(_ context.Context, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	b, ok := f.blobs[key]
	f.mu.Unlock()
	if !ok {
		return nil, ErrShardMiss // honor the interface contract: a miss is ErrShardMiss, not a nil reader
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func evictAllShardFiles(t *testing.T, cacheDir string) {
	t.Helper()
	dir := filepath.Join(StoreDir(cacheDir), "shards")
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, ents)
	for _, e := range ents {
		require.NoError(t, os.Remove(filepath.Join(dir, e.Name())))
	}
}

func TestRemoteShardPushPullRoundTrip(t *testing.T) {
	in := sampleInputs() // deterministic shards only (no runtime)
	dir := t.TempDir()
	rem := newFakeRemote()
	ctx := context.Background()

	built, err := Build(ctx, dir, BuildOptions{Remote: rem}, in, nil)
	require.NoError(t, err)
	require.NotEmpty(t, rem.blobs, "deterministic shards pushed to remote")
	wantNodes := len(built.Nodes())

	// Simulate LRU eviction: every shard file gone, manifest intact.
	evictAllShardFiles(t, dir)

	// Load restores each shard from remote by fingerprint - full graph, no rebuild.
	g, err := NewStore(dir, false, 0, rem, nil).Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, wantNodes, len(g.Nodes()), "graph fully restored from remote")
}

func TestRuntimeShardNotPushed(t *testing.T) {
	in := sampleInputs()
	in.Runtime = []types.DiagnosticEvent{{Unit: "pkg/a:build", Code: types.ExecDenied}}
	dir := t.TempDir()
	rem := newFakeRemote()
	ctx := context.Background()

	_, err := Build(ctx, dir, BuildOptions{Remote: rem}, in, nil)
	require.NoError(t, err)

	// The runtime shard's blob must not be on the remote (local history, not shared).
	runtimeFile := filepath.Join(StoreDir(dir), "shards", shardSlug(RuntimeShardName)+".json")
	require.FileExists(t, runtimeFile)
	evictAllShardFiles(t, dir)

	// Load now fails to restore the @runtime shard (it was never pushed), proving
	// the exclusion; a deterministic-only graph would have loaded fine.
	_, err = NewStore(dir, false, 0, rem, nil).Load(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), RuntimeShardName)
}

func TestPruneToSizeEvictsOverCap(t *testing.T) {
	in := sampleInputs()
	dir := t.TempDir()
	ctx := context.Background()

	// Build once uncapped to learn the store's natural size.
	_, err := Build(ctx, dir, BuildOptions{}, in, nil)
	require.NoError(t, err)
	total := shardsDirSize(t, dir)
	require.Positive(t, total)

	// Rebuild with a cap at half; the prune must bring the dir under it.
	cap := total / 2
	_, err = Build(ctx, dir, BuildOptions{MaxBytes: cap, Refresh: true}, in, nil)
	require.NoError(t, err)
	assert.LessOrEqual(t, shardsDirSize(t, dir), cap, "shards dir pruned to the cap")
}

func shardsDirSize(t *testing.T, cacheDir string) int64 {
	t.Helper()
	dir := filepath.Join(StoreDir(cacheDir), "shards")
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	var total int64
	for _, e := range ents {
		info, err := e.Info()
		require.NoError(t, err)
		total += info.Size()
	}
	return total
}
