package diagram

import (
	"context"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagramCutWalksBothDirections(t *testing.T) {
	g := projectGraph(chainWorkspace().targets)

	cut, err := g.cut(Lens{Focus: "libs/lib", Depth: 1})
	require.NoError(t, err)
	assert.Equal(t, []string{"app", "libs/lib", "libs/core"}, anchors(cut.nodes))
	assert.Equal(t, []edge{{src: "app", dst: "libs-lib"}, {src: "libs-lib", dst: "libs-core"}}, cut.edges)

	cut, err = g.cut(Lens{Focus: "libs/lib"})
	require.NoError(t, err)
	assert.Equal(t, []string{"libs/lib"}, anchors(cut.nodes))
	assert.Empty(t, cut.edges)

	cut, err = g.cut(Lens{Scope: []string{"libs/"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"libs/lib", "libs/core", "libs/base"}, anchors(cut.nodes))
	assert.Len(t, cut.edges, 2, "the app -> lib edge leaves with app")
}

func TestDiagramIDsStayUnique(t *testing.T) {
	taken := ids{}
	assert.Equal(t, "libs-lib", taken.of("libs/lib"))
	assert.Equal(t, "libs-lib-2", taken.of("libs-lib"))
	assert.Equal(t, "root", taken.of("."))
}

func TestDiagramActorNamesStayApart(t *testing.T) {
	names := actorNames([]Node{{ID: "a", Anchor: "x", Label: "core"}, {ID: "b", Anchor: "y", Label: "core"}, {ID: "c", Anchor: "z", Label: "app"}})
	assert.Equal(t, map[string]string{"a": "core (x)", "b": "core (y)", "c": "app"}, names)
	assert.Equal(t, "https://h/blob/r/libs/lib", linkTo("https://h/blob/r/{path}", "libs/lib"))
	assert.Empty(t, linkTo("", "libs/lib"))
}

func TestDiagramFingerprintMovesWithTheGraph(t *testing.T) {
	g := projectGraph(chainWorkspace().targets)
	assert.Equal(t, g.fingerprint(), projectGraph(chainWorkspace().targets).fingerprint())

	relabeled := g
	relabeled.nodes = slices.Clone(g.nodes)
	relabeled.nodes[0].Label = "renamed"
	assert.NotEqual(t, g.fingerprint(), relabeled.fingerprint())

	rewired := g
	rewired.edges = g.edges[1:]
	assert.NotEqual(t, g.fingerprint(), rewired.fingerprint())

	// Length prefixes keep "ab"+"c" apart from "a"+"bc".
	a := graph{id: "ab", title: "c"}
	b := graph{id: "a", title: "bc"}
	assert.NotEqual(t, a.fingerprint(), b.fingerprint())
}

func TestRenderCacheComputesOncePerKey(t *testing.T) {
	c := newRenderCache()
	var calls int
	compute := func(context.Context) (Figure, error) {
		calls++
		return Figure{Title: "t", SVG: "<svg/>", Nodes: []Node{{ID: "a"}}}, nil
	}
	first, err := c.get(t.Context(), "k", compute)
	require.NoError(t, err)
	first.Nodes[0].ID = "mutated"

	second, err := c.get(t.Context(), "k", compute)
	require.NoError(t, err)
	assert.Equal(t, Figure{Title: "t", SVG: "<svg/>", Nodes: []Node{{ID: "a"}}}, second, "a caller's edit does not reach the cache")
	assert.Equal(t, 1, calls)
}

func TestRenderCacheSharesOneRenderAcrossConcurrentCallers(t *testing.T) {
	c := newRenderCache()
	var calls atomic.Int32
	release := make(chan struct{})
	compute := func(context.Context) (Figure, error) {
		calls.Add(1)
		<-release
		return Figure{SVG: "<svg/>"}, nil
	}
	var wg sync.WaitGroup
	got := make([]Figure, 6)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = c.get(t.Context(), "k", compute)
		}()
	}
	for calls.Load() == 0 {
		runtime.Gosched()
	}
	close(release)
	wg.Wait()

	assert.Equal(t, int32(1), calls.Load())
	for _, f := range got {
		assert.Equal(t, Figure{SVG: "<svg/>"}, f)
	}
}

func TestRenderCacheNeverStoresAnError(t *testing.T) {
	c := newRenderCache()
	refusal := &FindingsError{Findings: "too big"}
	var calls int
	_, err := c.get(t.Context(), "k", func(context.Context) (Figure, error) { calls++; return Figure{}, refusal })
	assert.Same(t, refusal, err)
	_, err = c.get(t.Context(), "k", func(context.Context) (Figure, error) { calls++; return Figure{SVG: "ok"}, nil })
	require.NoError(t, err)
	assert.Equal(t, 2, calls)
	assert.Empty(t, c.inflight)
}

func TestRenderCacheNeverStoresACancelledRender(t *testing.T) {
	c := newRenderCache()
	ctx, cancel := context.WithCancel(t.Context())
	_, err := c.get(ctx, "k", func(ctx context.Context) (Figure, error) {
		cancel()
		return Figure{SVG: "torn"}, ctx.Err()
	})
	require.ErrorIs(t, err, context.Canceled)

	fig, err := c.get(t.Context(), "k", func(context.Context) (Figure, error) { return Figure{SVG: "whole"}, nil })
	require.NoError(t, err)
	assert.Equal(t, Figure{SVG: "whole"}, fig)
}

func TestRenderCacheWaiterSurvivesTheLeadersCancellation(t *testing.T) {
	c := newRenderCache()
	leaderCtx, cancel := context.WithCancel(t.Context())
	entered, proceed := make(chan struct{}), make(chan struct{})
	leaderDone := make(chan error, 1)
	go func() {
		_, err := c.get(leaderCtx, "k", func(ctx context.Context) (Figure, error) {
			close(entered)
			<-proceed
			return Figure{}, ctx.Err()
		})
		leaderDone <- err
	}()
	<-entered

	waiter := make(chan Figure, 1)
	go func() {
		fig, _ := c.get(t.Context(), "k", func(context.Context) (Figure, error) { return Figure{SVG: "mine"}, nil })
		waiter <- fig
	}()
	cancel()
	close(proceed)
	require.ErrorIs(t, <-leaderDone, context.Canceled)
	assert.Equal(t, Figure{SVG: "mine"}, <-waiter)
}

func TestRenderCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := newRenderCache()
	compute := func(context.Context) (Figure, error) { return Figure{SVG: "s"}, nil }
	for i := range renderCacheEntries {
		_, err := c.get(t.Context(), strconv.Itoa(i), compute)
		require.NoError(t, err)
	}
	_, err := c.get(t.Context(), "0", compute)
	require.NoError(t, err)
	_, err = c.get(t.Context(), "new", compute)
	require.NoError(t, err)

	assert.Len(t, c.entries, renderCacheEntries)
	assert.Contains(t, c.entries, "0", "a recent hit outlives older entries")
	assert.NotContains(t, c.entries, "1", "the oldest entry made room")
	assert.Equal(t, renderCacheEntries, c.bytes)
}

func TestRenderCacheSkipsAFigureOverTheByteBound(t *testing.T) {
	c := newRenderCache()
	var calls int
	huge := func(context.Context) (Figure, error) {
		calls++
		return Figure{SVG: strings.Repeat("x", renderCacheBytes+1)}, nil
	}
	for range 2 {
		_, err := c.get(t.Context(), "k", huge)
		require.NoError(t, err)
	}
	assert.Equal(t, 2, calls)
	assert.Empty(t, c.entries)
	assert.Zero(t, c.bytes)
}
