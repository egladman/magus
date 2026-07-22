package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// resolveWS is a minimal types.WorkspaceRepository for resolveTargets tests. It
// models only what resolution touches: the set of projects (for fan-out) and which
// project contains a given directory (cwd-scope). Everything else panics so a test
// that leans on unmodeled behavior fails loud rather than passing vacuously.
type resolveWS struct {
	root     string
	projects []string          // project paths, e.g. ".", "api"
	contains map[string]string // absolute dir -> project path it is inside
}

var _ types.WorkspaceRepository = (*resolveWS)(nil)

func (w *resolveWS) Root() string { return w.root }

func (w *resolveWS) All() []*types.Project {
	out := make([]*types.Project, len(w.projects))
	for i, p := range w.projects {
		out[i] = &types.Project{Path: p}
	}
	return out
}

func (w *resolveWS) Where(dir string) (*types.Project, bool) {
	if p, ok := w.contains[dir]; ok {
		return &types.Project{Path: p}, true
	}
	return nil, false
}

// ExpandPath mirrors magus.ExpandPath for the empty/"/" fan-out and a concrete path.
func (w *resolveWS) ExpandPath(t types.Target) ([]types.Target, error) {
	if t.Path == "" || t.Path == "/" {
		out := make([]types.Target, len(w.projects))
		for i, p := range w.projects {
			out[i] = types.Target{Path: p, Name: t.Name}
		}
		return out, nil
	}
	for _, p := range w.projects {
		if p == t.Path {
			return []types.Target{{Path: p, Name: t.Name}}, nil
		}
	}
	return nil, types.ErrUnknownProject
}

// ExpandCwd must never be reached by resolveTargets anymore: resolution keys on the
// explicit cwd via Where, not on the daemon's os.Getwd. Panic to prove the fix holds.
func (w *resolveWS) ExpandCwd(types.Target) ([]types.Target, bool, error) {
	panic("resolveTargets must not call ExpandCwd (it reads os.Getwd); use the explicit cwd")
}

func (w *resolveWS) Get(string) *types.Project { panic("not used") }
func (w *resolveWS) Graph() (*types.Graph, error) {
	panic("not used")
}
func (w *resolveWS) VCSOptions() types.VCSOptions { panic("not used") }
func (w *resolveWS) ExpandAffected(context.Context, string, string) ([]types.Target, string, bool, error) {
	panic("not used")
}
func (w *resolveWS) Affected(context.Context, string) (*types.AffectedResult, error) {
	panic("not used")
}
func (w *resolveWS) AffectedFromPaths(context.Context, []string) (*types.AffectedResult, error) {
	panic("not used")
}
func (w *resolveWS) DescribeSpells() types.SpellsOutput         { panic("not used") }
func (w *resolveWS) DescribeCharms([]string) types.CharmsOutput { panic("not used") }
func (w *resolveWS) DescribeTargets() types.TargetsOutput       { panic("not used") }
func (w *resolveWS) DescribeGraph(context.Context) types.TargetGraphOutput { panic("not used") }
func (w *resolveWS) DescribeProjects() types.ProjectsOutput     { panic("not used") }
func (w *resolveWS) DescribeWorkspaces(types.WorkspaceConfig) types.WorkspacesOutput {
	panic("not used")
}
func (w *resolveWS) DescribeTarget(types.Target) (types.EvaluatedTargetsOutput, error) {
	panic("not used")
}
func (w *resolveWS) DescribeEvaluatedProjects() types.EvaluatedProjectsOutput { panic("not used") }
func (w *resolveWS) DescribeFiles([]string) types.FilesOutput                 { panic("not used") }

// TestResolveTargetsCwdScope proves resolveTargets keys the cwd-scope on the explicit
// cwd argument (the client's, for an adopted run) rather than the process's os.Getwd.
func TestResolveTargetsCwdScope(t *testing.T) {
	ws := &resolveWS{
		root:     "/ws/b",
		projects: []string{".", "api"},
		contains: map[string]string{"/ws/b/api": "api"},
	}

	t.Run("cwd inside a project scopes to it", func(t *testing.T) {
		targets, source, err := resolveTargets(ws, types.Target{Name: "test"}, nil, "/ws/b/api")
		require.NoError(t, err)
		assert.Equal(t, "cwd", source)
		assert.Equal(t, []types.Target{{Path: "api", Name: "test"}}, targets)
	})

	t.Run("cwd outside every project fans out to all", func(t *testing.T) {
		// A daemon's own cwd (unrelated to workspace B) must not scope B's run - it
		// falls through to the full fan-out, not a mis-scoped or empty result.
		targets, source, err := resolveTargets(ws, types.Target{Name: "test"}, nil, "/tmp/daemon-cwd")
		require.NoError(t, err)
		assert.Empty(t, source)
		assert.Len(t, targets, 2)
	})

	t.Run("empty cwd fans out to all without touching Where", func(t *testing.T) {
		targets, _, err := resolveTargets(ws, types.Target{Name: "test"}, nil, "")
		require.NoError(t, err)
		assert.Len(t, targets, 2)
	})
}

// TestResolveTargetsEmptyWorkspace proves a fan-out over a workspace with no projects
// yields zero targets (which runTarget then turns into a loud failure, not a vacuous pass).
func TestResolveTargetsEmptyWorkspace(t *testing.T) {
	ws := &resolveWS{root: "/ws/empty", projects: nil, contains: nil}
	targets, _, err := resolveTargets(ws, types.Target{Name: "test"}, nil, "/ws/empty")
	require.NoError(t, err)
	assert.Empty(t, targets, "an empty workspace resolves no targets; runTarget must fail loudly on this")
}

// TestClientCwd proves the client's cwd carried on ctx wins over the process os.Getwd,
// so an adopted run scopes and journals against where the client ran, not the daemon.
func TestClientCwd(t *testing.T) {
	t.Run("ctx cwd wins", func(t *testing.T) {
		ctx := proc.WithCwd(context.Background(), "/client/here")
		assert.Equal(t, "/client/here", clientCwd(ctx))
	})

	t.Run("no ctx cwd falls back to os.Getwd", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		got := clientCwd(context.Background())
		// t.TempDir may hand back a /var symlink that Getwd resolves; compare suffix.
		require.NotEmpty(t, got)
		assert.True(t, strings.HasSuffix(got, dir) || strings.HasSuffix(dir, got) || got == dir,
			"fallback cwd %q should relate to %q", got, dir)
	})
}
