package types

import (
	"context"
	"slices"
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

	dispatch := map[string]struct{}{"api": {}, "web": {}}
	ctx = WithActiveDispatch(context.Background(), dispatch)
	assert.Equal(t, dispatch, ActiveDispatchFromContext(ctx))

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
