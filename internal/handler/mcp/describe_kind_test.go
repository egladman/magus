package mcp

import (
	"context"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDescribeSpellByName(t *testing.T) {
	out := []types.SpellEntry{{Name: "go"}, {Name: "typescript"}}

	t.Run("hit narrows to one entry", func(t *testing.T) {
		resp, err := describeSpellByName(out, "go")
		require.NoError(t, err)
		got := resp.Data.(types.SpellReport)
		assert.Equal(t, 1, got.Count)
		require.Len(t, got.Spells, 1)
		assert.Equal(t, "go", got.Spells[0].Name)
	})

	t.Run("miss names the valid values", func(t *testing.T) {
		_, err := describeSpellByName(out, "rust")
		require.Error(t, err)
		assert.ErrorContains(t, err, `no spell named "rust"`)
		assert.ErrorContains(t, err, "go, typescript")
	})
}

func TestDescribeProjectByPath(t *testing.T) {
	out := types.ProjectsOutput{
		Count:    2,
		Projects: []types.ProjectEntry{{Path: "api"}, {Path: "web/studio"}},
	}

	t.Run("hit narrows to one entry", func(t *testing.T) {
		resp, err := describeProjectByPath(out, "web/studio")
		require.NoError(t, err)
		got := resp.Data.(types.ProjectsOutput)
		assert.Equal(t, 1, got.Count)
		require.Len(t, got.Projects, 1)
		assert.Equal(t, "web/studio", got.Projects[0].Path)
	})

	t.Run("miss names the valid values", func(t *testing.T) {
		_, err := describeProjectByPath(out, "web")
		require.Error(t, err)
		assert.ErrorContains(t, err, `no project at path "web"`)
		assert.ErrorContains(t, err, "api, web/studio")
	})
}

// fakeDescriber records the Target passed to EvaluateTarget so a test can assert
// how a name param was parsed; every other Inspector method is an unused stub.
type fakeDescriber struct{ gotTarget types.Target }

func (f *fakeDescriber) ListCharms(context.Context) ([]types.Charm, error) {
	return nil, nil
}
func (f *fakeDescriber) ListTargets(context.Context) ([]types.TargetEntry, error) { return nil, nil }
func (f *fakeDescriber) TargetGraph(context.Context) (types.TargetGraphOutput, error) {
	return types.TargetGraphOutput{}, nil
}
func (f *fakeDescriber) ListProjects(context.Context) (types.ProjectsOutput, error) {
	return types.ProjectsOutput{}, nil
}
func (f *fakeDescriber) Workspace(context.Context, types.WorkspaceConfig) (types.WorkspaceEntry, error) {
	return types.WorkspaceEntry{}, nil
}
func (f *fakeDescriber) EvaluateProjects(context.Context) (types.EvaluatedProjectsOutput, error) {
	return types.EvaluatedProjectsOutput{}, nil
}
func (f *fakeDescriber) ClassifyFiles(context.Context, []string) ([]types.FileEntry, error) {
	return nil, nil
}
func (f *fakeDescriber) EvaluateTarget(ctx context.Context, target types.Target) ([]types.EvaluatedTarget, error) {
	f.gotTarget = target
	return []types.EvaluatedTarget{{}}, nil
}

func TestDescribeTargetByName(t *testing.T) {
	t.Run("name only leaves the project unscoped", func(t *testing.T) {
		ws := &fakeDescriber{}
		resp, err := describeTargetByName(context.Background(), ws, "build")
		require.NoError(t, err)
		assert.Equal(t, "build", ws.gotTarget.Name)
		assert.Empty(t, ws.gotTarget.Path)
		assert.Equal(t, 1, resp.Data.(types.EvaluatedTargetReport).Count)
	})

	t.Run("name with charms parses the charm suffix", func(t *testing.T) {
		ws := &fakeDescriber{}
		_, err := describeTargetByName(context.Background(), ws, "lint:rw")
		require.NoError(t, err)
		assert.Equal(t, "lint", ws.gotTarget.Name)
		assert.Equal(t, []string{"rw"}, ws.gotTarget.Charms)
	})

	t.Run("trailing project token scopes the plan", func(t *testing.T) {
		ws := &fakeDescriber{}
		_, err := describeTargetByName(context.Background(), ws, "build api")
		require.NoError(t, err)
		assert.Equal(t, "build", ws.gotTarget.Name)
		assert.Equal(t, "api", ws.gotTarget.Path)
	})
}

// TestDescribeKindCoversEveryCLINoun guards the parity gap this closed: charms,
// graph and modules were reachable on the CLI but not over MCP, so an agent with
// only MCP could not discover what charms exist, could not see the target DAG, and
// could not introspect the Buzz stdlib at all. Two of the three were already on the
// Inspector interface the tool holds - the capability was in hand and unswitched.
func TestDescribeKindCoversEveryCLINoun(t *testing.T) {
	tool := &describeKindTool{ws: &fakeDescriber{}}
	for _, kind := range []string{"spells", "charms", "targets", "graph", "projects", "workspaces", "modules", "mcp_tools"} {
		resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"kind": kind}})
		require.NoErrorf(t, err, "kind %q must be served", kind)
		assert.NotNilf(t, resp.Data, "kind %q returned no data", kind)
	}

	_, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"kind": "nonsense"}})
	require.Error(t, err, "an unknown kind must still be an error")
	// The error lists the valid set; every served kind must appear in it, or an agent
	// is told a noun does not exist when it does.
	for _, kind := range []string{"spells", "charms", "targets", "graph", "projects", "workspaces", "modules", "mcp_tools"} {
		assert.Containsf(t, err.Error(), kind, "the unknown-kind error omits %q", kind)
	}
}
