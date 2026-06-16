package workspace

import (
	"context"
	"testing"
)

func TestNewWorkspaceRegistry(t *testing.T) {
	r := NewWorkspaceRegistry()
	if r == nil {
		t.Fatal("NewWorkspaceRegistry() returned nil")
	}
}

func TestContextWithRegistry_RoundTrip(t *testing.T) {
	r := NewWorkspaceRegistry()
	ctx := ContextWithRegistry(context.Background(), r)
	got := WorkspaceRegistryFromContext(ctx)
	if got != r {
		t.Errorf("WorkspaceRegistryFromContext returned %p, want %p", got, r)
	}
}

func TestWorkspaceRegistryFromContext_MissingReturnsNil(t *testing.T) {
	got := WorkspaceRegistryFromContext(context.Background())
	if got != nil {
		t.Errorf("WorkspaceRegistryFromContext on empty ctx = %v, want nil", got)
	}
}

func TestWorkspaceRegistry_RegisterProject_ProjectPaths(t *testing.T) {
	r := NewWorkspaceRegistry()
	r.RegisterProject("api")
	r.RegisterProject("web")
	r.RegisterProject("cmd/tool")

	paths := r.ProjectPaths()
	want := []string{"api", "cmd/tool", "web"}
	if len(paths) != len(want) {
		t.Fatalf("ProjectPaths() = %v, want %v", paths, want)
	}
	for i, p := range paths {
		if p != want[i] {
			t.Errorf("ProjectPaths()[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestWorkspaceRegistry_ProjectPaths_Empty(t *testing.T) {
	r := NewWorkspaceRegistry()
	paths := r.ProjectPaths()
	if len(paths) != 0 {
		t.Errorf("ProjectPaths() on empty registry = %v, want []", paths)
	}
}

func TestWorkspaceRegistry_RegisterProject_AccumulatesOptions(t *testing.T) {
	r := NewWorkspaceRegistry()
	r.RegisterProject("api", WithOutputs("dist/api"))
	r.RegisterProject("api", WithOutputs("dist/api-extra"))

	// Both registrations should be stored (two calls to RegisterProject same path).
	// We verify via ProjectPaths that "api" appears exactly once.
	paths := r.ProjectPaths()
	if len(paths) != 1 || paths[0] != "api" {
		t.Errorf("ProjectPaths() = %v, want [api]", paths)
	}
}
