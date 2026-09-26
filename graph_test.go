package magus

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

func TestComposeGraph(t *testing.T) {
	// A workspace named "magus" so the root display reads as "magus" rather
	// than as the temp dir's auto-generated basename.
	root := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.MkdirAll(root, 0o755))
	canonicalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	for _, path := range []string{".", "api", "docs"} {
		dir := filepath.Join(root, path)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
	}
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".")
	reg.RegisterProject("api", WithDependsOn(".."))
	reg.RegisterProject("docs", WithDependsOn("api"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	graph, err := ws.Graph()
	require.NoError(t, err)

	out := ComposeGraph(ws, WithGraphInput(graph), WithComposeRoots("."))
	assert.Equal(t, types.GraphOutput{
		Direction: "downstream",
		Roots:     []string{"."},
		Nodes: []types.Node{{
			Path:        ".",
			Name:        "magus",
			Children:    []string{},
			Dir:         canonicalRoot,
			BlastRadius: 3,
		}},
	}, out)

	upstream := ComposeGraph(ws, WithUpstream(), WithComposeRoots("api"))
	assert.Equal(t, types.GraphOutput{
		Direction: "upstream",
		Roots:     []string{"api"},
		Nodes: []types.Node{{
			Path:     "api",
			Name:     "api",
			Children: []string{"docs"},
			Dir:      filepath.Join(canonicalRoot, "api"),
		}},
	}, upstream)
}

// countingRebuild returns a rebuild func that tallies calls and yields a fresh
// (distinct) graph each time, plus a pointer to the call counter.
func countingRebuild() (func(context.Context, bool) (*knowledge.Graph, error), *atomic.Int64) {
	var n atomic.Int64
	return func(context.Context, bool) (*knowledge.Graph, error) {
		n.Add(1)
		return knowledge.NewGraph(), nil
	}, &n
}

// TestWarmGraphPassesRefreshToTheBuilder pins the thing that was silently false: Get's
// refresh must reach the BUILDER, not merely skip this process's memory. Bound to false,
// a caller asking to refresh got a rebuild from the same cached shards, which is the one
// answer that looks like it worked.
func TestWarmGraphPassesRefreshToTheBuilder(t *testing.T) {
	var got []bool
	w := newWarmGraph(func(_ context.Context, refresh bool) (*knowledge.Graph, error) {
		got = append(got, refresh)
		return knowledge.NewGraph(), nil
	}, nil)

	_, err := w.Get(t.Context(), false)
	require.NoError(t, err)
	_, err = w.Get(t.Context(), true)
	require.NoError(t, err)

	assert.Equal(t, []bool{false, true}, got, "the builder must see the caller's refresh verbatim")
}

func TestWarmGraphNoWatcherAlwaysRebuilds(t *testing.T) {
	rebuild, n := countingRebuild()
	w := newWarmGraph(rebuild, nil)
	ctx := context.Background()
	// With no watcher, nothing can invalidate a cache, so every Get rebuilds.
	for i := 0; i < 3; i++ {
		_, err := w.Get(ctx, false)
		require.NoError(t, err)
	}
	assert.Equal(t, int64(3), n.Load())
}

func TestWarmGraphWatchingCachesUntilInvalidated(t *testing.T) {
	rebuild, n := countingRebuild()
	w := newWarmGraph(rebuild, nil)
	w.mu.Lock()
	w.watching = true // simulate an active watcher without spinning up fsnotify
	w.mu.Unlock()
	ctx := context.Background()

	_, err := w.Get(ctx, false)
	require.NoError(t, err)
	_, err = w.Get(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n.Load(), "second Get should hit the warm cache")

	w.invalidate()
	_, err = w.Get(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n.Load(), "Get after invalidate should rebuild")
}

func TestWarmGraphRefreshForcesRebuild(t *testing.T) {
	rebuild, n := countingRebuild()
	w := newWarmGraph(rebuild, nil)
	w.mu.Lock()
	w.watching = true
	w.mu.Unlock()
	ctx := context.Background()

	_, err := w.Get(ctx, false)
	require.NoError(t, err)
	_, err = w.Get(ctx, true) // refresh bypasses the cache
	require.NoError(t, err)
	assert.Equal(t, int64(2), n.Load())
}

func TestWarmGraphSingleFlight(t *testing.T) {
	var n atomic.Int64
	release := make(chan struct{})
	rebuild := func(context.Context, bool) (*knowledge.Graph, error) {
		n.Add(1)
		<-release // hold every build open until the test releases it
		return knowledge.NewGraph(), nil
	}
	w := newWarmGraph(rebuild, nil)
	w.mu.Lock()
	w.watching = true
	w.mu.Unlock()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = w.Get(ctx, false) }()
	}
	// Let the first builder enter rebuild, then release; the rest coalesce.
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	assert.Equal(t, int64(1), n.Load(), "concurrent misses must coalesce into one build")
}

// TestWarmGraphInvalidationDuringBuild covers the mid-build race: a change that
// lands while a rebuild is in flight must leave the cache untrusted, so the graph
// that missed the change is never served on a later query.
func TestWarmGraphInvalidationDuringBuild(t *testing.T) {
	var n atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	rebuild := func(context.Context, bool) (*knowledge.Graph, error) {
		if n.Add(1) == 1 {
			close(started)
			<-release // hold the first build open
		}
		return knowledge.NewGraph(), nil
	}
	w := newWarmGraph(rebuild, nil)
	w.mu.Lock()
	w.watching = true
	w.mu.Unlock()
	ctx := context.Background()

	done := make(chan struct{})
	go func() { defer close(done); _, _ = w.Get(ctx, false) }()

	<-started
	w.invalidate() // a source change lands mid-build
	close(release)
	<-done

	assert.Nil(t, w.cached(), "cache must be untrusted after a mid-build invalidation")
	_, err := w.Get(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n.Load(), "the next Get must rebuild, not serve the stale graph")
}

// TestWarmGraphWatchInvalidatesOnChange exercises the real fsnotify watcher: a
// change to a graph-relevant file under the watched root drops the warm cache.
func TestWarmGraphWatchInvalidatesOnChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fsnotify integration test under -short")
	}
	root := t.TempDir()
	rebuild, n := countingRebuild()
	w := newWarmGraph(rebuild, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := w.watch(ctx, root)
	require.NoError(t, err)
	defer stop()

	// Warm the cache; a second Get should be a cache hit.
	_, err = w.Get(ctx, false)
	require.NoError(t, err)
	require.NotNil(t, w.cached())
	_, err = w.Get(ctx, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), n.Load())

	// Touch a graph-relevant file; the watcher (200ms debounce) should invalidate.
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// x\n"), 0o644))

	require.Eventually(t, func() bool { return w.cached() == nil }, 5*time.Second, 25*time.Millisecond,
		"a change to a .buzz file should invalidate the warm cache")

	_, err = w.Get(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n.Load(), "Get after invalidation rebuilds")
}

func TestGraphRelevant(t *testing.T) {
	assert.True(t, graphRelevant([]string{"src/main.go", "pkg/x/magusfile.buzz"}))
	assert.True(t, graphRelevant([]string{"docs/knowledge.md"}))
	assert.True(t, graphRelevant([]string{"/abs/path/magus.yaml"}))
	assert.True(t, graphRelevant([]string{"a/magusfiles"}))
	assert.False(t, graphRelevant([]string{"main.go", "assets/logo.png", "go.mod"}))
	assert.False(t, graphRelevant(nil))
}
