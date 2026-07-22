package mcp

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDescribeSpellByName(t *testing.T) {
	out := types.SpellsOutput{
		Count:  2,
		Spells: []types.SpellEntry{{Name: "go"}, {Name: "typescript"}},
	}

	t.Run("hit narrows to one entry", func(t *testing.T) {
		resp, err := describeSpellByName(out, "go")
		require.NoError(t, err)
		got := resp.Data.(types.SpellsOutput)
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

// fakeDescriber records the Target passed to DescribeTarget so a test can assert
// how a name param was parsed; every other Describer method is an unused stub.
type fakeDescriber struct{ gotTarget types.Target }

func (f *fakeDescriber) DescribeSpells() types.SpellsOutput         { return types.SpellsOutput{} }
func (f *fakeDescriber) DescribeCharms([]string) types.CharmsOutput { return types.CharmsOutput{} }
func (f *fakeDescriber) DescribeTargets() types.TargetsOutput       { return types.TargetsOutput{} }
func (f *fakeDescriber) DescribeGraph(context.Context) types.TargetGraphOutput { return types.TargetGraphOutput{} }
func (f *fakeDescriber) DescribeProjects() types.ProjectsOutput     { return types.ProjectsOutput{} }
func (f *fakeDescriber) DescribeWorkspaces(types.WorkspaceConfig) types.WorkspacesOutput {
	return types.WorkspacesOutput{}
}
func (f *fakeDescriber) DescribeEvaluatedProjects() types.EvaluatedProjectsOutput {
	return types.EvaluatedProjectsOutput{}
}
func (f *fakeDescriber) DescribeFiles([]string) types.FilesOutput { return types.FilesOutput{} }
func (f *fakeDescriber) DescribeTarget(target types.Target) (types.EvaluatedTargetsOutput, error) {
	f.gotTarget = target
	return types.EvaluatedTargetsOutput{Count: 1}, nil
}

func TestDescribeTargetByName(t *testing.T) {
	t.Run("name only leaves the project unscoped", func(t *testing.T) {
		ws := &fakeDescriber{}
		resp, err := describeTargetByName(ws, "build")
		require.NoError(t, err)
		assert.Equal(t, "build", ws.gotTarget.Name)
		assert.Empty(t, ws.gotTarget.Path)
		assert.Equal(t, 1, resp.Data.(types.EvaluatedTargetsOutput).Count)
	})

	t.Run("name with charms parses the charm suffix", func(t *testing.T) {
		ws := &fakeDescriber{}
		_, err := describeTargetByName(ws, "lint:rw")
		require.NoError(t, err)
		assert.Equal(t, "lint", ws.gotTarget.Name)
		assert.Equal(t, []string{"rw"}, ws.gotTarget.Charms)
	})

	t.Run("trailing project token scopes the plan", func(t *testing.T) {
		ws := &fakeDescriber{}
		_, err := describeTargetByName(ws, "build api")
		require.NoError(t, err)
		assert.Equal(t, "build", ws.gotTarget.Name)
		assert.Equal(t, "api", ws.gotTarget.Path)
	})
}
