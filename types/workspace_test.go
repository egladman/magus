package types

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
