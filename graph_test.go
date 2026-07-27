package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeGraph(t *testing.T) {
	root := t.TempDir()
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
			Name:        filepath.Base(canonicalRoot),
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
