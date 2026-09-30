package types

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWorkspaceRepo satisfies WorkspaceRepository via an embedded nil interface;
// the context round-trip tests only need identity, never a method call.
type fakeWorkspaceRepo struct{ WorkspaceRepository }

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

	repo := &fakeWorkspaceRepo{}
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

func TestEvalMemoComputesOncePerKey(t *testing.T) {
	t.Parallel()
	ctx := WithEvalMemo(context.Background())
	m := EvalMemoFromContext(ctx)
	calls := 0
	compute := func() (any, error) { calls++; return calls, nil }

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			v, err := m.Do("graph", compute)
			assert.NoError(t, err)
			assert.Equal(t, 1, v)
		})
	}
	wg.Wait()
	other, err := m.Do("other", compute)
	require.NoError(t, err)
	assert.Equal(t, 2, other, "a second key computes its own value")
}

func TestEvalMemoKeepsNoFailure(t *testing.T) {
	t.Parallel()
	m := EvalMemoFromContext(WithEvalMemo(context.Background()))
	_, err := m.Do("graph", func() (any, error) { return nil, context.Canceled })
	require.ErrorIs(t, err, context.Canceled)
	v, err := m.Do("graph", func() (any, error) { return "built", nil })
	require.NoError(t, err)
	assert.Equal(t, "built", v, "a failure is recomputed rather than replayed")
}

func TestEvalMemoScopes(t *testing.T) {
	t.Parallel()
	assert.Nil(t, EvalMemoFromContext(context.Background()))
	var nilMemo *EvalMemo
	calls := 0
	for range 2 {
		_, err := nilMemo.Do("k", func() (any, error) { calls++; return nil, errors.New("x") })
		require.Error(t, err)
	}
	assert.Equal(t, 2, calls, "a nil memo computes every time")

	outer := WithEvalMemo(context.Background())
	inner := WithEvalMemo(outer)
	assert.NotSame(t, EvalMemoFromContext(outer), EvalMemoFromContext(inner), "a nested evaluation starts empty")
}
