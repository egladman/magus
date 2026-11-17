package types

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// identityOnlyRepo satisfies WorkspaceRepository via an embedded nil interface;
// the context round-trip tests only need identity, never a method call.
type identityOnlyRepo struct{ WorkspaceRepository }

// TestWorkspaceGraphObserver covers the mutable default observer: unset reads
// nil, SetGraphObserver installs one, and nil clears it.
func TestWorkspaceGraphObserver(t *testing.T) {
	ws := &Workspace{}
	assert.Nil(t, ws.GraphObserver(), "an unset observer reads nil")

	obs := NoopObserver{}
	ws.SetGraphObserver(obs)
	assert.Equal(t, obs, ws.GraphObserver())

	ws.SetGraphObserver(nil)
	assert.Nil(t, ws.GraphObserver(), "passing nil clears the observer")
}

// TestWorkspaceContextHelpers covers the three context carriers: the workspace
// repository, the active-dispatch set, and the request-scoped graph observer.
// Each reads nil from a bare context and its stored value from a seeded one.
func TestWorkspaceContextHelpers(t *testing.T) {
	assert.Nil(t, WorkspaceFromContext(context.Background()))
	assert.Nil(t, ActiveDispatchFromContext(context.Background()))
	assert.Nil(t, GraphObserverFromContext(context.Background()))

	repo := &identityOnlyRepo{}
	ctx := WithWorkspace(context.Background(), repo)
	assert.Same(t, repo, WorkspaceFromContext(ctx))

	dispatch := &ActiveDispatch{}
	dispatch.Mark("api")
	dispatch.Mark("web")
	ctx = WithActiveDispatch(context.Background(), dispatch)
	assert.Same(t, dispatch, ActiveDispatchFromContext(ctx))
	assert.True(t, ActiveDispatchFromContext(ctx).Has("api"))
	assert.False(t, ActiveDispatchFromContext(ctx).Has("absent"))

	obs := NoopObserver{}
	ctx = ContextWithGraphObserver(context.Background(), obs)
	assert.Equal(t, obs, GraphObserverFromContext(ctx))
}

func newWorkspace(paths ...string) *Workspace {
	projects := make(map[string]*Project, len(paths))
	for _, p := range paths {
		projects[p] = &Project{Path: p}
	}
	return &Workspace{Projects: projects}
}

func TestWorkspaceAllIsSorted(t *testing.T) {
	ws := newWorkspace("web/studio", "api", "cmd/tool")
	got := make([]string, 0, 3)
	for _, p := range ws.All() {
		got = append(got, p.Path)
	}
	assert.Equal(t, []string{"api", "cmd/tool", "web/studio"}, got, "All() must return a deterministic sort")
}

func TestWorkspaceGet(t *testing.T) {
	ws := newWorkspace("api")

	p := ws.Get("api")
	require.NotNil(t, p)
	assert.Equal(t, "api", p.Path)

	assert.Nil(t, ws.Get("missing"))

	// A nil workspace must not panic — Get guards the receiver.
	var nilWS *Workspace
	assert.Nil(t, nilWS.Get("api"))
}

func TestWorkspaceUnderPath(t *testing.T) {
	ws := newWorkspace("web", "web/studio", "web/admin", "webhook", "api")
	under := ws.UnderPath("web")
	got := make([]string, 0, len(under))
	for _, p := range under {
		got = append(got, p.Path)
	}
	slices.Sort(got)
	// Matching is path-segment aware: "web" and its descendants match because
	// "web/" is a prefix of "web/", "web/admin/", etc. "webhook" must NOT match
	// ("webhook/" does not have the prefix "web/").
	assert.Equal(t, []string{"web", "web/admin", "web/studio"}, got)
}

func TestWorkspaceNestedDirs(t *testing.T) {
	ws := &Workspace{Projects: map[string]*Project{
		".":         {Path: ".", Dir: "/w"},
		"leaf":      {Path: "leaf", Dir: "/w/leaf"},
		"leaf/deep": {Path: "leaf/deep", Dir: "/w/leaf/deep"},
		"sibling":   {Path: "sibling", Dir: "/w/sibling"},
	}}
	assert.Equal(t, []string{"leaf", "leaf/deep", "sibling"}, ws.NestedDirs("/w"))
	assert.Equal(t, []string{"deep"}, ws.NestedDirs("/w/leaf"))
	assert.Empty(t, ws.NestedDirs("/w/sibling"))
}

func TestGlobClaims(t *testing.T) {
	nested := []string{"leaf", "leaf/deep"}
	for _, tc := range []struct {
		glob, rel string
		want      bool
	}{
		{"**/gen/*.go", "gen/a.go", true},
		{"**/gen/*.go", "leaf/gen/a.go", false},
		{"leaf/**/gen/*.go", "leaf/gen/a.go", true},
		{"leaf/**/gen/*.go", "leaf/deep/gen/a.go", false},
		{"leaf/deep/gen/*.go", "leaf/deep/gen/a.go", true},
		{"leaf/out", "leaf/out/x.go", true},
		{"leaf/gen/a.go", "leaf/gen/a.go", true},
		{"lea*/gen/a.go", "leaf/gen/a.go", false},
	} {
		assert.Equal(t, tc.want, GlobClaims(tc.glob, tc.rel, nested), "%s claims %s", tc.glob, tc.rel)
	}
}

func TestEvaluationComputesOncePerKey(t *testing.T) {
	t.Parallel()
	ctx := WithEvalOnce(context.Background())
	m := EvalOnceFromContext(ctx)
	calls := 0
	compute := func() (any, error) { calls++; return calls, nil }

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			v, err := m.Do("graph", compute)
			assert.NoError(t, err) //nolint:testifylint // a wg.Go goroutine, where require's FailNow cannot stop the test
			assert.Equal(t, 1, v)
		})
	}
	wg.Wait()
	other, err := m.Do("other", compute)
	require.NoError(t, err)
	assert.Equal(t, 2, other, "a second key computes its own value")
}

func TestEvaluationKeepsNoFailure(t *testing.T) {
	t.Parallel()
	m := EvalOnceFromContext(WithEvalOnce(context.Background()))
	_, err := m.Do("graph", func() (any, error) { return nil, context.Canceled })
	require.ErrorIs(t, err, context.Canceled)
	v, err := m.Do("graph", func() (any, error) { return "built", nil })
	require.NoError(t, err)
	assert.Equal(t, "built", v, "a failure is recomputed rather than replayed")
}

func TestEvaluationScopes(t *testing.T) {
	t.Parallel()
	assert.Nil(t, EvalOnceFromContext(context.Background()))
	var nilHolder *EvalOnce
	calls := 0
	for range 2 {
		_, err := nilHolder.Do("k", func() (any, error) { calls++; return nil, errors.New("x") })
		require.Error(t, err)
	}
	assert.Equal(t, 2, calls, "a nil holder computes every time")

	outer := WithEvalOnce(context.Background())
	inner := WithEvalOnce(outer)
	assert.NotSame(t, EvalOnceFromContext(outer), EvalOnceFromContext(inner), "a nested evaluation starts empty")
}

func TestEndedEvaluationKeepsNothing(t *testing.T) {
	t.Parallel()
	m := EvalOnceFromContext(WithEvalOnce(context.Background()))
	calls := 0
	compute := func() (any, error) { calls++; return calls, nil }
	_, err := m.Do("graph", compute)
	require.NoError(t, err)

	m.End()
	m.mu.Lock()
	held := len(m.cells)
	m.mu.Unlock()
	assert.Zero(t, held, "End drops every computed value")
}

func TestEndedEvaluationComputesEveryTime(t *testing.T) {
	t.Parallel()
	m := EvalOnceFromContext(WithEvalOnce(context.Background()))
	calls := 0
	compute := func() (any, error) { calls++; return calls, nil }
	first, err := m.Do("graph", compute)
	require.NoError(t, err)
	assert.Equal(t, 1, first)

	m.End()
	for want := 2; want <= 3; want++ {
		v, err := m.Do("graph", compute)
		require.NoError(t, err)
		assert.Equal(t, want, v, "an ended evaluation recomputes, as a nil one does")
	}
}

// Not parallel: it swaps the default logger to read what is said.
func TestEndedEvaluationSaysSoOnce(t *testing.T) {
	var out bytes.Buffer
	prev, prevOut, prevFlags := slog.Default(), log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&out, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	m := EvalOnceFromContext(WithEvalOnce(context.Background()))
	m.End()
	for range 3 {
		_, err := m.Do("graph+symbols:", func() (any, error) { return nil, nil })
		require.NoError(t, err)
	}
	assert.Equal(t, 1, bytes.Count(out.Bytes(), []byte("already ended")), "said once: %s", out.String())
	assert.Contains(t, out.String(), "key=graph+symbols:", "it names the key")
}

func TestEndedEvaluationKeepsNothingFromAComputeInFlight(t *testing.T) {
	t.Parallel()
	m := EvalOnceFromContext(WithEvalOnce(context.Background()))
	started, release := make(chan struct{}), make(chan struct{})
	type result struct {
		v   any
		err error
	}
	done := make(chan result)
	go func() {
		v, err := m.Do("graph", func() (any, error) {
			close(started)
			<-release
			return "late", nil
		})
		done <- result{v, err}
	}()
	<-started
	m.End()
	close(release)
	got := <-done
	require.NoError(t, got.err)
	assert.Equal(t, "late", got.v, "a compute already running returns its value")

	again, err := m.Do("graph", func() (any, error) { return "again", nil })
	require.NoError(t, err)
	assert.Equal(t, "again", again, "the value it computed was not kept")
}

func TestEndedReportsWhetherTheEvaluationEnded(t *testing.T) {
	t.Parallel()
	var nilHolder *EvalOnce
	assert.False(t, nilHolder.Ended(), "a nil holder never ended")
	m := EvalOnceFromContext(WithEvalOnce(context.Background()))
	assert.False(t, m.Ended())
	m.End()
	assert.True(t, m.Ended())
}
