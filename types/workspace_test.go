package types

import (
	"slices"
	"testing"
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
	want := []string{"api", "cmd/tool", "web/studio"}
	if !slices.Equal(got, want) {
		t.Errorf("All() paths = %v, want %v (deterministic sort)", got, want)
	}
}

func TestWorkspaceGet(t *testing.T) {
	ws := newWorkspace("api")
	if p := ws.Get("api"); p == nil || p.Path != "api" {
		t.Errorf("Get(api) = %v, want project api", p)
	}
	if p := ws.Get("missing"); p != nil {
		t.Errorf("Get(missing) = %v, want nil", p)
	}
	// A nil workspace must not panic — Get guards the receiver.
	var nilWS *Workspace
	if p := nilWS.Get("api"); p != nil {
		t.Errorf("(*Workspace)(nil).Get(api) = %v, want nil", p)
	}
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
	want := []string{"web", "web/admin", "web/studio"}
	if !slices.Equal(got, want) {
		t.Errorf("UnderPath(web) = %v, want %v", got, want)
	}
}
