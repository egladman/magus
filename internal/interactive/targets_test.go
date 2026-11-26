package interactive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
func (w *resolveWS) ListCharms(context.Context) ([]types.CharmEntry, error) {
	panic("not used")
}
func (w *resolveWS) ListTargets(context.Context) ([]types.TargetEntry, error) {
	panic("not used")
}
func (w *resolveWS) TargetGraph(context.Context) (types.TargetGraphOutput, error) {
	panic("not used")
}
func (w *resolveWS) ListProjects(context.Context) (types.ProjectsOutput, error) {
	panic("not used")
}
func (w *resolveWS) Workspace(context.Context, types.WorkspaceConfig) (types.WorkspaceEntry, error) {
	panic("not used")
}
func (w *resolveWS) EvaluateTarget(context.Context, types.Target) ([]types.EvaluatedTarget, error) {
	panic("not used")
}
func (w *resolveWS) EvaluateProjects(context.Context) (types.EvaluatedProjectsOutput, error) {
	panic("not used")
}
func (w *resolveWS) ClassifyFiles(context.Context, []string) ([]types.FileEntry, error) {
	panic("not used")
}

// TestResolveTargetsCwdScope proves ResolveTargets keys the cwd-scope on the explicit
// cwd argument (the client's, for an adopted run) rather than the process's os.Getwd.
func TestResolveTargetsCwdScope(t *testing.T) {
	ws := &resolveWS{
		root:     "/ws/b",
		projects: []string{".", "api"},
		contains: map[string]string{"/ws/b/api": "api"},
	}

	t.Run("cwd inside a project scopes to it", func(t *testing.T) {
		targets, source, err := ResolveTargets(t.Context(), ws, types.Target{Name: "test"}, RunSelection{Cwd: "/ws/b/api"})
		require.NoError(t, err)
		assert.Equal(t, "cwd", source)
		assert.Equal(t, []types.Target{{Path: "api", Name: "test"}}, targets)
	})

	t.Run("cwd outside every project fans out to all", func(t *testing.T) {
		// A server's own cwd (unrelated to workspace B) must not scope B's run: it
		// falls through to the full fan-out, not a mis-scoped or empty result.
		targets, source, err := ResolveTargets(t.Context(), ws, types.Target{Name: "test"}, RunSelection{Cwd: "/tmp/server-cwd"})
		require.NoError(t, err)
		assert.Empty(t, source)
		assert.Len(t, targets, 2)
	})

	t.Run("empty cwd fans out to all without touching Where", func(t *testing.T) {
		targets, _, err := ResolveTargets(t.Context(), ws, types.Target{Name: "test"}, RunSelection{Cwd: ""})
		require.NoError(t, err)
		assert.Len(t, targets, 2)
	})
}

// TestResolveTargetsRootOverride proves a relative project arg is measured from the
// workspace `--root` named, not from the cwd the caller happens to be standing in.
// The trap: filepath.Rel answers an outside cwd with a "../"-prefixed anchor, which
// reads as an escape unless re-anchored at the root.
func TestResolveTargetsRootOverride(t *testing.T) {
	ws := &resolveWS{
		root:     "/ws/b",
		projects: []string{".", "api"},
		contains: map[string]string{"/ws/b/api": "api"},
	}

	t.Run("dot from outside means the workspace root project", func(t *testing.T) {
		targets, _, err := ResolveTargets(t.Context(), ws, types.Target{Name: "test"}, RunSelection{Projects: []string{"."}, Skips: nil, Cwd: "/ws/other"})
		require.NoError(t, err)
		assert.Equal(t, []types.Target{{Path: ".", Name: "test"}}, targets)
	})

	t.Run("dot-relative from outside means the workspace root project", func(t *testing.T) {
		targets, _, err := ResolveTargets(t.Context(), ws, types.Target{Name: "test"}, RunSelection{Projects: []string{"./api"}, Skips: nil, Cwd: "/ws/other"})
		require.NoError(t, err)
		assert.Equal(t, []types.Target{{Path: "api", Name: "test"}}, targets)
	})

	// Unchanged: an inside cwd still anchors dot-relative refs at that cwd.
	t.Run("dot from inside a project still means that project", func(t *testing.T) {
		targets, _, err := ResolveTargets(t.Context(), ws, types.Target{Name: "test"}, RunSelection{Projects: []string{"."}, Skips: nil, Cwd: "/ws/b/api"})
		require.NoError(t, err)
		assert.Equal(t, []types.Target{{Path: "api", Name: "test"}}, targets)
	})

	// An escape is still an escape: re-anchoring at the root does not widen what resolves.
	t.Run("escape from outside is still rejected", func(t *testing.T) {
		_, _, err := ResolveTargets(t.Context(), ws, types.Target{Name: "test"}, RunSelection{Projects: []string{"../up"}, Skips: nil, Cwd: "/ws/other"})
		require.ErrorContains(t, err, "escapes workspace root")
	})
}

// TestResolveTargetsEmptyWorkspace proves a fan-out over a workspace with no projects
// yields zero targets (which runTarget then turns into a loud failure, not a vacuous pass).
func TestResolveTargetsEmptyWorkspace(t *testing.T) {
	ws := &resolveWS{root: "/ws/empty", projects: nil, contains: nil}
	targets, _, err := ResolveTargets(t.Context(), ws, types.Target{Name: "test"}, RunSelection{Cwd: "/ws/empty"})
	require.NoError(t, err)
	assert.Empty(t, targets, "an empty workspace resolves no targets; runTarget must fail loudly on this")
}

// skipTargets resolves the test target with skips from a cwd outside the workspace, so
// "." names the root project rather than the cwd.
func skipTargets(t *testing.T, named []string, skips ...string) ([]types.Target, error) {
	t.Helper()
	ws := &resolveWS{
		root:     "/ws/b",
		projects: []string{".", "api", "web"},
		contains: map[string]string{"/ws/b/api": "api"},
	}
	targets, _, err := ResolveTargets(t.Context(), ws, types.Target{Name: "test"}, RunSelection{Projects: named, Skips: skips, Cwd: "/ws/other"})
	return targets, err
}

// testIn is the test target in each of paths.
func testIn(paths ...string) []types.Target {
	out := make([]types.Target, len(paths))
	for i, p := range paths {
		out[i] = types.Target{Path: p, Name: "test"}
	}
	return out
}

// TestResolveTargetsSkip covers --skip: it subtracts from whatever the positionals
// resolved to, and refuses anything that would leave the caller believing a project was
// gated when it was not.
func TestResolveTargetsSkip(t *testing.T) {
	t.Run("bare invocation skips from the full fan-out", func(t *testing.T) {
		targets, err := skipTargets(t, nil, "api")
		require.NoError(t, err)
		assert.Equal(t, testIn(".", "web"), targets)
	})

	t.Run("repeatable", func(t *testing.T) {
		targets, err := skipTargets(t, nil, "api", "web")
		require.NoError(t, err)
		assert.Equal(t, testIn("."), targets)
	})

	// The same grammar as a positional, resolver included: "." from outside the
	// workspace is the root project, not the cwd.
	t.Run("dot reference resolves like a positional", func(t *testing.T) {
		targets, err := skipTargets(t, nil, ".")
		require.NoError(t, err)
		assert.Equal(t, testIn("api", "web"), targets)
	})

	// A real project outside an explicit selection is not a contradiction (it was
	// never going to run), so it subtracts nothing and stays silent.
	t.Run("skipping an unselected project is a no-op", func(t *testing.T) {
		targets, err := skipTargets(t, []string{"api"}, "web")
		require.NoError(t, err)
		assert.Equal(t, testIn("api"), targets)
	})

	t.Run("a reference no project matches is an error", func(t *testing.T) {
		_, err := skipTargets(t, nil, "apo")
		require.ErrorContains(t, err, "--skip apo")
		require.ErrorIs(t, err, types.ErrUnknownProject)
	})

	t.Run("skipping a project the command line named is contradictory", func(t *testing.T) {
		_, err := skipTargets(t, []string{"api"}, "api")
		require.ErrorContains(t, err, "--skip api contradicts naming it on the command line")
	})

	t.Run("skipping every project is an error, not an empty pass", func(t *testing.T) {
		_, err := skipTargets(t, nil, ".", "api", "web")
		require.ErrorContains(t, err, "removed every selected project")
	})

	// "/" is the all-projects sentinel ResolveProject passes through untouched; matched
	// against project paths it would skip nothing at all.
	t.Run("the all-projects sentinel is refused", func(t *testing.T) {
		_, err := skipTargets(t, nil, "/")
		require.ErrorContains(t, err, "there is no all-projects skip")
	})

	t.Run("a glob subtracts every project it matches", func(t *testing.T) {
		targets, err := skipTargets(t, nil, "a*")
		require.NoError(t, err)
		assert.Equal(t, testIn(".", "web"), targets)
	})

	t.Run("a glob matching no project is an error", func(t *testing.T) {
		_, err := skipTargets(t, nil, "z*")
		require.ErrorContains(t, err, "no project matches the glob")
	})

	t.Run("a glob covering an explicitly named project is contradictory", func(t *testing.T) {
		_, err := skipTargets(t, []string{"api"}, "a*")
		require.ErrorContains(t, err, "contradicts naming it on the command line")
	})

	// "*" legitimately matches every single-segment path including ".", so it falls to
	// the emptied-selection refusal rather than a special case.
	t.Run("a glob skipping everything is the emptied-selection error", func(t *testing.T) {
		_, err := skipTargets(t, nil, "*")
		require.ErrorContains(t, err, "removed every selected project")
	})
}

// TestApplyTargetFilter locks the run fault-tolerance matrix: tolerant across the
// workspace / multiple projects, strict for a single named project and for a name no
// project serves.
func TestApplyTargetFilter(t *testing.T) {
	// "go-vet" is defined only in the go projects.
	defines := func(path, target string) bool {
		return target == "go-vet" && (path == "." || path == "gopherbuzz")
	}
	label := func(path string) string { return path }
	tgt := func(paths ...string) []types.Target {
		out := make([]types.Target, len(paths))
		for i, p := range paths {
			out[i] = types.Target{Path: p, Name: "go-vet"}
		}
		return out
	}

	t.Run("multi partial: skip the undefined, keep the defined", func(t *testing.T) {
		got, err := applyTargetFilter(context.Background(), tgt(".", "gopherbuzz", "docs", "proto"), "go-vet", defines, label)
		require.NoError(t, err)
		assert.Equal(t, tgt(".", "gopherbuzz"), got, "undefined projects are dropped, not errored")
	})

	t.Run("single project undefined: error", func(t *testing.T) {
		_, err := applyTargetFilter(context.Background(), tgt("docs"), "go-vet", defines, label)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "docs", "names the single project")
	})

	t.Run("single project defined: runs", func(t *testing.T) {
		got, err := applyTargetFilter(context.Background(), tgt("gopherbuzz"), "go-vet", defines, label)
		require.NoError(t, err)
		assert.Equal(t, tgt("gopherbuzz"), got)
	})

	t.Run("none serve across many: unknown-target error", func(t *testing.T) {
		_, err := applyTargetFilter(context.Background(), tgt("docs", "proto"), "bogus", func(string, string) bool { return false }, label)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "any of the 2 selected projects")
	})

	t.Run("all defined: pass through", func(t *testing.T) {
		got, err := applyTargetFilter(context.Background(), tgt(".", "gopherbuzz"), "go-vet", defines, label)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})
}

func TestCwdAnchor(t *testing.T) {
	root := testkit.NewWorkspace(t).Root()
	sub := filepath.Join(root, "web", "studio")
	require.NoError(t, os.MkdirAll(sub, 0o755), "mkdir")

	t.Run("subdir resolves to slash-relative anchor", func(t *testing.T) {
		assert.Equal(t, "web/studio", currentWorkingDirectoryAnchor(root, sub))
	})

	t.Run("root resolves to dot", func(t *testing.T) {
		assert.Equal(t, ".", currentWorkingDirectoryAnchor(root, root))
	})

	t.Run("empty cwd falls back to dot", func(t *testing.T) {
		assert.Equal(t, ".", currentWorkingDirectoryAnchor(root, ""))
	})

	// The --root regression. filepath.Rel answers a cwd outside the workspace with a
	// "../"-prefixed path, which is not an anchor: every relative project ref then
	// inherits the escape and `magus --root <ws> run build .` failed with
	// `project path "." escapes workspace root from "../<dir>"`.
	t.Run("cwd outside the workspace anchors at the root", func(t *testing.T) {
		outside := filepath.Join(filepath.Dir(root), "elsewhere")
		require.NoError(t, os.MkdirAll(outside, 0o755), "mkdir")
		assert.Equal(t, ".", currentWorkingDirectoryAnchor(root, outside))
	})

	t.Run("cwd in the workspace parent anchors at the root", func(t *testing.T) {
		assert.Equal(t, ".", currentWorkingDirectoryAnchor(root, filepath.Dir(root)))
	})

	// `magus --root <main> run go-build .` from a worktree nested under the main checkout
	// resolved `.` to the worktree's path, which is no project of the main checkout.
	t.Run("cwd in a checkout nested under the workspace anchors at the root", func(t *testing.T) {
		tree := filepath.Join(root, ".claude", "worktrees", "w")
		require.NoError(t, os.MkdirAll(filepath.Join(tree, "internal"), 0o755), "mkdir")
		require.NoError(t, os.WriteFile(filepath.Join(tree, ".git"), []byte("gitdir: elsewhere\n"), 0o644), "write .git")
		assert.Equal(t, ".", currentWorkingDirectoryAnchor(root, tree))
		assert.Equal(t, ".", currentWorkingDirectoryAnchor(root, filepath.Join(tree, "internal")))
	})
}
