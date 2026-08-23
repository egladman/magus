package magus

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectDisplayName resolves the label the insight lenses attach to a scanned
// path. A path the workspace does not know still gets a label rather than an empty
// cell: a commit can touch a directory no project claims.
func TestProjectDisplayName(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
	})
	ws, err := Inspect(context.Background(), root)
	require.NoError(t, err)
	m := ws.(*Magus)

	assert.Equal(t, "api", m.projectDisplayName("api"))
	assert.Equal(t, "deleted/project", m.projectDisplayName("deleted/project"),
		"a path no project claims still labels as itself")
	assert.NotEmpty(t, m.projectDisplayName("."), "the root project always has a label")
}

// TestDeclaredDeps is the lookup the affinity lens uses to decide whether a
// co-change pair is HIDDEN. It is indexed by project path, and both directions are
// consulted, so a project with no dependencies must still appear with an empty set
// rather than be absent.
func TestDeclaredDeps(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":     "",
		"lib/magusfile.buzz": "",
		"web/magusfile.buzz": "",
	})

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("web", WithDependsOn("lib"))

	ws, err := Inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err)
	m := ws.(*Magus)

	deps := m.declaredDeps()
	require.Contains(t, deps, "web")
	require.Contains(t, deps, "lib")
	assert.True(t, deps["web"]["lib"], "a declared dependency is indexed in the direction it points")
	assert.Empty(t, deps["lib"], "a project with no dependencies is present with an empty set, not absent")
	assert.False(t, deps["lib"]["web"], "the edge is one-way")
}
