package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interactive"
)

func TestWhereUniqueMatch(t *testing.T) {
	all := []*types.Project{
		{Path: "api/gateway", Dir: "/tmp/api/gateway"},
		{Path: "api/auth", Dir: "/tmp/api/auth"},
		{Path: "web/dashboard", Dir: "/tmp/web/dashboard"},
	}
	scored := interactive.ScoreProjects(all, []string{"dash"})
	require.Len(t, scored, 1, "expected unique match web/dashboard")
	assert.Equal(t, "web/dashboard", scored[0].P.Path)
}

func TestWhereAmbiguous(t *testing.T) {
	all := []*types.Project{
		{Path: "api/gateway", Dir: "/tmp/api/gateway"},
		{Path: "api/auth", Dir: "/tmp/api/auth"},
	}
	scored := interactive.ScoreProjects(all, []string{"api"})
	assert.GreaterOrEqual(t, len(scored), 2, "expected ambiguous results")
}

func TestWhereNoMatch(t *testing.T) {
	all := []*types.Project{
		{Path: "api/gateway", Dir: "/tmp/api/gateway"},
	}
	scored := interactive.ScoreProjects(all, []string{"zzznope"})
	assert.Empty(t, scored)
}

func TestResolveProjectArg(t *testing.T) {
	t.Run("all projects empty sentinel", func(t *testing.T) {
		got, err := file.ResolveProject(t.Context(), "", "web/studio")
		require.NoError(t, err)
		assert.Equal(t, "", got)
	})
	t.Run("all projects slash sentinel", func(t *testing.T) {
		got, err := file.ResolveProject(t.Context(), "/", "web/studio")
		require.NoError(t, err)
		assert.Equal(t, "/", got)
	})
	t.Run("bare stays workspace-relative", func(t *testing.T) {
		got, err := file.ResolveProject(t.Context(), "api", "web/studio")
		require.NoError(t, err)
		assert.Equal(t, "api", got)
	})
	t.Run("dot up resolves against cwd", func(t *testing.T) {
		got, err := file.ResolveProject(t.Context(), "../api", "web/studio")
		require.NoError(t, err)
		assert.Equal(t, "web/api", got)
	})
	t.Run("dot sibling resolves against cwd", func(t *testing.T) {
		got, err := file.ResolveProject(t.Context(), "./peer", "extensions/drape")
		require.NoError(t, err)
		assert.Equal(t, "extensions/drape/peer", got)
	})
	t.Run("escape rejected", func(t *testing.T) {
		_, err := file.ResolveProject(t.Context(), "../../../foo", "a/b")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "escapes workspace root")
	})
	t.Run("absolute rejected", func(t *testing.T) {
		_, err := file.ResolveProject(t.Context(), "/etc", "web/studio")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be repo-relative")
	})
}

func TestCwdAnchor(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err, "eval-symlinks temp dir")
	sub := filepath.Join(root, "web", "studio")
	require.NoError(t, os.MkdirAll(sub, 0o755), "mkdir")

	t.Run("subdir resolves to slash-relative anchor", func(t *testing.T) {
		assert.Equal(t, "web/studio", cwdAnchor(root, sub))
	})

	t.Run("root resolves to dot", func(t *testing.T) {
		assert.Equal(t, ".", cwdAnchor(root, root))
	})

	t.Run("empty cwd falls back to dot", func(t *testing.T) {
		assert.Equal(t, ".", cwdAnchor(root, ""))
	})

	// The --root regression. filepath.Rel answers a cwd outside the workspace with a
	// "../"-prefixed path, which is not an anchor - every relative project ref then
	// inherits the escape and `magus --root <ws> run build .` failed with
	// `project path "." escapes workspace root from "../<dir>"`.
	t.Run("cwd outside the workspace anchors at the root", func(t *testing.T) {
		outside := filepath.Join(filepath.Dir(root), "elsewhere")
		require.NoError(t, os.MkdirAll(outside, 0o755), "mkdir")
		assert.Equal(t, ".", cwdAnchor(root, outside))
	})

	t.Run("cwd in the workspace parent anchors at the root", func(t *testing.T) {
		assert.Equal(t, ".", cwdAnchor(root, filepath.Dir(root)))
	})
}
