package magus

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lazyShards is a RemoteShards that records what reached it, so a test can tell which
// backing a lazyRemoteShards resolved.
type lazyShards struct {
	mu   sync.Mutex
	gets []string
	puts []string
}

func (s *lazyShards) GetShard(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets = append(s.gets, key)
	return io.NopCloser(strings.NewReader("shard:" + key)), nil
}

func (s *lazyShards) PutShard(_ context.Context, key string, body io.Reader) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts = append(s.puts, key+"="+string(b))
	return nil
}

// countingOpen is an open func over a real, empty workspace that counts its calls. An
// Inspect-constructed *Magus is cheap (no cache, no evaluation until asked), so the test
// exercises the real handle rather than a stand-in.
func countingOpen(t *testing.T) (string, func(context.Context) (*Magus, error), *int) {
	t.Helper()
	root := writeWorkspace(t, map[string]string{"magusfile.buzz": ""})
	opens := new(int)
	return root, func(ctx context.Context) (*Magus, error) {
		*opens++
		ws, err := Inspect(ctx, root)
		if err != nil {
			return nil, err
		}
		return ws.(*Magus), nil
	}, opens
}

func failingOpen(err error) (func(context.Context) (*Magus, error), *int) {
	opens := new(int)
	return func(context.Context) (*Magus, error) {
		*opens++
		return nil, err
	}, opens
}

func TestLazyWorkspaceReportsRootWithoutOpening(t *testing.T) {
	root, open, opens := countingOpen(t)
	lw := NewLazyWorkspace(root, open)

	assert.Equal(t, root, lw.Root())
	assert.False(t, lw.Opened())
	assert.Zero(t, *opens, "the stamps and the store need only the root, which an unopened read still has")
}

func TestLazyWorkspaceOpensExactlyOnceOnTheFirstInspectorCall(t *testing.T) {
	root, open, opens := countingOpen(t)
	lw := NewLazyWorkspace(root, open)
	ctx := context.Background()

	_, err := lw.ListProjects(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, *opens)
	assert.True(t, lw.Opened())

	_, err = lw.TargetGraph(ctx)
	require.NoError(t, err)
	_, err = lw.ListTargets(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, *opens, "every later call answers from the handle the first one opened")

	first, err := lw.Magus(ctx)
	require.NoError(t, err)
	second, err := lw.Magus(ctx)
	require.NoError(t, err)
	assert.Same(t, first, second)
	assert.Equal(t, 1, *opens)
}

func TestLazyWorkspaceOpensOnceUnderConcurrentFirstCalls(t *testing.T) {
	root, open, opens := countingOpen(t)
	lw := NewLazyWorkspace(root, open)

	var wg sync.WaitGroup
	handles := make([]*Magus, 8)
	for i := range handles {
		wg.Go(func() {
			m, err := lw.Magus(context.Background())
			assert.NoError(t, err)
			handles[i] = m
		})
	}
	wg.Wait()

	assert.Equal(t, 1, *opens)
	for _, m := range handles {
		assert.Same(t, handles[0], m)
	}
}

func TestLazyWorkspaceEveryInspectorMethodReturnsTheOpenError(t *testing.T) {
	boom := errors.New("workspace failed to load")
	open, opens := failingOpen(boom)
	lw := NewLazyWorkspace(t.TempDir(), open)
	ctx := context.Background()

	var errs []error
	collect := func(err error) { errs = append(errs, err) }
	_, err := lw.ListCharms(ctx)
	collect(err)
	_, err = lw.ListTargets(ctx)
	collect(err)
	_, err = lw.ListProjects(ctx)
	collect(err)
	_, err = lw.EvaluateProjects(ctx)
	collect(err)
	_, err = lw.EvaluateTarget(ctx, types.Target{})
	collect(err)
	_, err = lw.ClassifyFiles(ctx, []string{"a.go"})
	collect(err)
	_, err = lw.TargetGraph(ctx)
	collect(err)
	_, err = lw.Workspace(ctx, types.WorkspaceConfig{})
	collect(err)

	require.Len(t, errs, 8)
	for i, err := range errs {
		assert.ErrorIsf(t, err, boom, "Inspector call %d", i)
	}
	assert.Equal(t, 1, *opens, "a failed open is not retried: the caller reports it, as an eager open would have")
	assert.True(t, lw.Opened())
}

func TestLazyWorkspaceUsePublishedShardsBeforeOpenBacksTheOpenedMagus(t *testing.T) {
	root, open, opens := countingOpen(t)
	lw := NewLazyWorkspace(root, open)
	published := &lazyShards{}

	UsePublishedShards(lw, published)
	assert.Zero(t, *opens, "installing a backing must not open the workspace")

	m, err := lw.Magus(context.Background())
	require.NoError(t, err)
	assert.Same(t, published, remoteShards(m), "a read that opens late still restores from the published graph")
}

func TestLazyWorkspaceUsePublishedShardsAfterOpenBacksTheOpenedMagus(t *testing.T) {
	root, open, _ := countingOpen(t)
	lw := NewLazyWorkspace(root, open)
	m, err := lw.Magus(context.Background())
	require.NoError(t, err)
	require.Nil(t, remoteShards(m), "an Inspect-constructed handle has no backing until one is installed")
	published := &lazyShards{}

	UsePublishedShards(lw, published)

	assert.Same(t, published, remoteShards(m))
}

func TestLazyWorkspaceRemoteShardsIsLazyAndOpensNothing(t *testing.T) {
	root, open, opens := countingOpen(t)
	lw := NewLazyWorkspace(root, open)

	r := remoteShards(lw)

	assert.IsType(t, lazyRemoteShards{}, r)
	assert.Zero(t, *opens, "a read the stored shards answer never opens the workspace for its remote")
	assert.False(t, lw.Opened())
}

func TestLazyWorkspaceRemoteShardsOpenOnGetAndPutOnly(t *testing.T) {
	root, open, opens := countingOpen(t)
	lw := NewLazyWorkspace(root, open)
	published := &lazyShards{}
	UsePublishedShards(lw, published)
	r := lazyRemoteShards{lw}
	ctx := context.Background()

	rc, err := r.GetShard(ctx, "k1")
	require.NoError(t, err)
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "shard:k1", string(b), "the read reached the backing the opened Magus carries")
	assert.Equal(t, 1, *opens)

	// The opened Magus has no cache, so the only link is the published one, which the
	// chain exposes as itself; its write is whatever that backing does with it.
	assert.NoError(t, r.PutShard(ctx, "k2", strings.NewReader("body")))
	assert.Equal(t, 1, *opens, "the second use reuses the open")
	assert.Equal(t, []string{"k1"}, published.gets)
	assert.Equal(t, []string{"k2=body"}, published.puts)
}

func TestLazyWorkspaceRemoteShardsWithNoBackingBehaveAsLocalOnly(t *testing.T) {
	root, open, opens := countingOpen(t)
	r := lazyRemoteShards{NewLazyWorkspace(root, open)}
	ctx := context.Background()

	_, err := r.GetShard(ctx, "k")
	assert.ErrorIs(t, err, knowledge.ErrShardMiss, "no remote is a miss, which the store answers by building locally")
	assert.NoError(t, r.PutShard(ctx, "k", strings.NewReader("body")), "nothing to push to is not a failure")
	assert.Equal(t, 1, *opens)
}

func TestLazyWorkspaceRemoteShardsReturnTheOpenError(t *testing.T) {
	boom := errors.New("workspace failed to load")
	open, opens := failingOpen(boom)
	r := lazyRemoteShards{NewLazyWorkspace(t.TempDir(), open)}
	ctx := context.Background()

	_, err := r.GetShard(ctx, "k")
	assert.ErrorIs(t, err, boom)
	assert.ErrorIs(t, r.PutShard(ctx, "k", strings.NewReader("body")), boom)
	assert.Equal(t, 1, *opens)
}
