package mcp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWorkspace is a minimal types.WorkspaceReader for the workspace-scoped
// tools: only Root and All are exercised by whereTool, the rest are stubs.
type fakeWorkspace struct {
	root     string
	projects []*types.Project
}

func (f *fakeWorkspace) Root() string                        { return f.root }
func (f *fakeWorkspace) All() []*types.Project               { return f.projects }
func (f *fakeWorkspace) Get(string) *types.Project           { return nil }
func (f *fakeWorkspace) Graph() (*types.Graph, error)        { return nil, nil }
func (f *fakeWorkspace) VCSOptions() types.VCSOptions        { return types.VCSOptions{} }
func (f *fakeWorkspace) Where(string) (*types.Project, bool) { return nil, false }

func TestWhereTool(t *testing.T) {
	ws := &fakeWorkspace{root: "/ws", projects: []*types.Project{{Path: "api"}, {Path: "web/studio"}}}
	tool := &whereTool{ws: ws}

	t.Run("unambiguous match resolves to abs dir", func(t *testing.T) {
		resp, err := tool.Invoke(context.Background(), types.InvokeRequest{Params: map[string]any{"filter": "api"}})
		require.NoError(t, err)
		got := resp.Data.(whereResult)
		assert.Equal(t, 1, got.Matched)
		assert.Equal(t, "api", got.Path)
		assert.Equal(t, filepath.Join("/ws", "api"), got.AbsDir)
	})

	t.Run("no filter with multiple projects is ambiguous", func(t *testing.T) {
		resp, err := tool.Invoke(context.Background(), types.InvokeRequest{})
		require.NoError(t, err)
		got := resp.Data.(whereResult)
		assert.Equal(t, 2, got.Matched)
		assert.Len(t, got.Candidates, 2)
		assert.NotEmpty(t, got.Error)
	})

	t.Run("no project matches the filter", func(t *testing.T) {
		_, err := tool.Invoke(context.Background(), types.InvokeRequest{Params: map[string]any{"filter": "zzz-nope"}})
		assert.Error(t, err)
	})

	t.Run("empty workspace errors", func(t *testing.T) {
		empty := &whereTool{ws: &fakeWorkspace{}}
		_, err := empty.Invoke(context.Background(), types.InvokeRequest{})
		assert.Error(t, err)
	})
}

func TestParamHelpers(t *testing.T) {
	params := map[string]any{"s": "value", "b": true, "wrongS": 42, "wrongB": "nope"}

	assert.Equal(t, "value", paramString(params, "s", "def"))
	assert.Equal(t, "def", paramString(params, "missing", "def"))
	assert.Equal(t, "def", paramString(params, "wrongS", "def"), "wrong type falls back to default")

	assert.True(t, paramBool(params, "b", false))
	assert.False(t, paramBool(params, "missing", false))
	assert.True(t, paramBool(params, "wrongB", true), "wrong type falls back to default")
}
