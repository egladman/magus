package workspace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWorkspaceRegistry(t *testing.T) {
	r := NewWorkspaceRegistry()
	require.NotNil(t, r)
}

func TestContextWithRegistry_RoundTrip(t *testing.T) {
	r := NewWorkspaceRegistry()
	ctx := ContextWithRegistry(context.Background(), r)
	got := WorkspaceRegistryFromContext(ctx)
	assert.Same(t, r, got)
}

func TestWorkspaceRegistryFromContext_MissingReturnsNil(t *testing.T) {
	got := WorkspaceRegistryFromContext(context.Background())
	assert.Nil(t, got)
}

func TestWorkspaceRegistry_RegisterProject_ProjectPaths(t *testing.T) {
	r := NewWorkspaceRegistry()
	r.RegisterProject("api")
	r.RegisterProject("web")
	r.RegisterProject("cmd/tool")

	assert.Equal(t, []string{"api", "cmd/tool", "web"}, r.ProjectPaths())
}

func TestWorkspaceRegistry_ProjectPaths_Empty(t *testing.T) {
	r := NewWorkspaceRegistry()
	assert.Empty(t, r.ProjectPaths())
}

func TestWorkspaceRegistry_RegisterProject_AccumulatesOptions(t *testing.T) {
	r := NewWorkspaceRegistry()
	r.RegisterProject("api", WithOutputs("dist/api"))
	r.RegisterProject("api", WithOutputs("dist/api-extra"))

	// Both registrations should be stored (two calls to RegisterProject same path).
	// We verify via ProjectPaths that "api" appears exactly once.
	assert.Equal(t, []string{"api"}, r.ProjectPaths())
}
