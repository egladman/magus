package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/proc"
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
func (w *resolveWS) ListCharms(context.Context) ([]types.Charm, error) {
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

// TestResolveTargetsCwdScope proves resolveTargets keys the cwd-scope on the explicit
// cwd argument (the client's, for an adopted run) rather than the process's os.Getwd.
func TestResolveTargetsCwdScope(t *testing.T) {
	ws := &resolveWS{
		root:     "/ws/b",
		projects: []string{".", "api"},
		contains: map[string]string{"/ws/b/api": "api"},
	}

	t.Run("cwd inside a project scopes to it", func(t *testing.T) {
		targets, source, err := resolveTargets(t.Context(), ws, types.Target{Name: "test"}, nil, "/ws/b/api")
		require.NoError(t, err)
		assert.Equal(t, "cwd", source)
		assert.Equal(t, []types.Target{{Path: "api", Name: "test"}}, targets)
	})

	t.Run("cwd outside every project fans out to all", func(t *testing.T) {
		// A daemon's own cwd (unrelated to workspace B) must not scope B's run - it
		// falls through to the full fan-out, not a mis-scoped or empty result.
		targets, source, err := resolveTargets(t.Context(), ws, types.Target{Name: "test"}, nil, "/tmp/daemon-cwd")
		require.NoError(t, err)
		assert.Empty(t, source)
		assert.Len(t, targets, 2)
	})

	t.Run("empty cwd fans out to all without touching Where", func(t *testing.T) {
		targets, _, err := resolveTargets(t.Context(), ws, types.Target{Name: "test"}, nil, "")
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
		targets, _, err := resolveTargets(t.Context(), ws, types.Target{Name: "test"}, []string{"."}, "/ws/other")
		require.NoError(t, err)
		assert.Equal(t, []types.Target{{Path: ".", Name: "test"}}, targets)
	})

	t.Run("dot-relative from outside means the workspace root project", func(t *testing.T) {
		targets, _, err := resolveTargets(t.Context(), ws, types.Target{Name: "test"}, []string{"./api"}, "/ws/other")
		require.NoError(t, err)
		assert.Equal(t, []types.Target{{Path: "api", Name: "test"}}, targets)
	})

	// Unchanged: an inside cwd still anchors dot-relative refs at that cwd.
	t.Run("dot from inside a project still means that project", func(t *testing.T) {
		targets, _, err := resolveTargets(t.Context(), ws, types.Target{Name: "test"}, []string{"."}, "/ws/b/api")
		require.NoError(t, err)
		assert.Equal(t, []types.Target{{Path: "api", Name: "test"}}, targets)
	})

	// An escape is still an escape: re-anchoring at the root does not widen what resolves.
	t.Run("escape from outside is still rejected", func(t *testing.T) {
		_, _, err := resolveTargets(t.Context(), ws, types.Target{Name: "test"}, []string{"../up"}, "/ws/other")
		require.ErrorContains(t, err, "escapes workspace root")
	})
}

// TestResolveTargetsEmptyWorkspace proves a fan-out over a workspace with no projects
// yields zero targets (which runTarget then turns into a loud failure, not a vacuous pass).
func TestResolveTargetsEmptyWorkspace(t *testing.T) {
	ws := &resolveWS{root: "/ws/empty", projects: nil, contains: nil}
	targets, _, err := resolveTargets(t.Context(), ws, types.Target{Name: "test"}, nil, "/ws/empty")
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
		got, err := applyTargetFilter(tgt(".", "gopherbuzz", "docs", "proto"), "go-vet", defines, label)
		require.NoError(t, err)
		assert.Equal(t, tgt(".", "gopherbuzz"), got, "undefined projects are dropped, not errored")
	})

	t.Run("single project undefined: error", func(t *testing.T) {
		_, err := applyTargetFilter(tgt("docs"), "go-vet", defines, label)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "docs", "names the single project")
	})

	t.Run("single project defined: runs", func(t *testing.T) {
		got, err := applyTargetFilter(tgt("gopherbuzz"), "go-vet", defines, label)
		require.NoError(t, err)
		assert.Equal(t, tgt("gopherbuzz"), got)
	})

	t.Run("none serve across many: unknown-target error", func(t *testing.T) {
		_, err := applyTargetFilter(tgt("docs", "proto"), "bogus", func(string, string) bool { return false }, label)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "any of the 2 selected projects")
	})

	t.Run("all defined: pass through", func(t *testing.T) {
		got, err := applyTargetFilter(tgt(".", "gopherbuzz"), "go-vet", defines, label)
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})
}

// TestCrossProjectOutputReplaysFromCache is the execution half of cross-project outputs:
// a target declaring ctx.writesFiles(<sibling>.file(...)) writes into ANOTHER project's tree,
// and magus snapshots that file like any other output, so a cache hit restores it after
// it is deleted. Getting this wrong is quiet: the second build reports success over a
// file that is no longer there.
//
// It lives here rather than beside the root package's cross-output tests because it needs
// a magusfile body to actually run, and the interpreter is a pack only cmd/magus wires in
// (packs_interp.go) - the root test binary registers shim spells that collide with it.
func TestCrossProjectOutputReplaysFromCache(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}
	write("magusfile.buzz", "")
	write("site/magusfile.buzz", "")
	// fs.writeFile, not a shelled-out redirect: what is under test is magus's handling of
	// a foreign-tree output, and a subprocess would drag /bin/sh in for nothing.
	//
	// The body also writes an UNDECLARED marker beside the output. That is what makes
	// replay observable: a cache miss re-runs the body and recreates both files, a cache
	// hit restores only the declared one. Without it the assertions below pass either way
	// and the test proves nothing. The marker sits in site/, not producer/, so creating it
	// does not perturb producer's own source hash and invalidate the entry under test.
	write("producer/magusfile.buzz", `import "magus";
import "fs";
import "project/../site" as site;

export fun build(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles(site.file("generated.txt"));
    fs\writeFile("../site/body-ran.marker", "1");
    fs\writeFile("../site/generated.txt", "hello");
}
`)

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	// m.Root() is symlink-resolved by project.Discover; build the path on it so it shares
	// that canonical base (t.TempDir resolves through /private on macOS).
	generated := filepath.Join(m.Root(), "site", "generated.txt")
	marker := filepath.Join(m.Root(), "site", "body-ran.marker")
	targets := []types.Target{{Path: "producer", Name: "build"}}

	require.NoError(t, m.Run(ctx, targets), "first run")
	content, err := os.ReadFile(generated)
	require.NoError(t, err, "producer must write into the sibling tree")
	assert.Equal(t, "hello", string(content))
	require.FileExists(t, marker, "first run executes the body")

	// Remove both. Only the declared output may come back.
	require.NoError(t, os.Remove(generated))
	require.NoError(t, os.Remove(marker))

	require.NoError(t, m.Run(ctx, targets), "replay run")
	content, err = os.ReadFile(generated)
	require.NoError(t, err, "a cache hit must replay the output into the OWNING project's tree")
	assert.Equal(t, "hello", string(content))
	assert.NoFileExists(t, marker,
		"the marker is undeclared, so its absence is what proves the run was a cache hit and not a re-execution")
}

// TestCrossProjectOutputMustBeProduced covers the mismatch nothing else can catch: the
// path a target DECLARES and the path it WRITES are two independent strings, spelled
// differently by convention (the declaration is relative to the owner, the write to the
// writer), so a typo between them is invisible at authoring time.
//
// The producer here also declares an output of its own, which is what defeats the
// ordinary all-or-nothing snapshot check: that check passes on own.txt alone, the
// manifest quietly omits the foreign file, and every later cache hit would replay a
// partial output set into a tree this target does not own.
func TestCrossProjectOutputMustBeProduced(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}
	write("magusfile.buzz", "")
	write("site/magusfile.buzz", "")
	write("producer/magusfile.buzz", `import "magus";
import "fs";
import "project/../site" as site;

export fun build(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles("own.txt", site.file("generated.txt"));
    fs\writeFile("own.txt", "mine");
    fs\writeFile("../site/generated.text", "typo");
}
`)

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	err = m.Run(ctx, []types.Target{{Path: "producer", Name: "build"}})
	require.Error(t, err, "a declared cross-project output that was never written must fail the run")
	assert.ErrorIs(t, err, types.CrossOutputNotProduced)
	assert.NoFileExists(t, filepath.Join(m.Root(), "site", "generated.txt"))
}

// TestWithoutDetachFlagStripsEverySpelling pins the loop guard on --detach. The argv is
// re-submitted to the daemon verbatim, so a --detach left in it would make the daemon
// detach again, handing the work to itself indefinitely.
func TestWithoutDetachFlagStripsEverySpelling(t *testing.T) {
	got := withoutDetachFlag([]string{"ci", "-detach", "docs", "--detach", "--detach=true", "--detach-me"})
	assert.Equal(t, []string{"ci", "docs", "--detach-me"}, got,
		"every spelling the flag package accepts is dropped; a different flag that merely starts the same is not")

	assert.Equal(t, []string{"ci", "."}, withoutDetachFlag([]string{"ci", "."}),
		"an argv without the flag is unchanged")

	assert.Equal(t, []string{"ci", "--", "--detach"}, withoutDetachFlag([]string{"ci", "--", "--detach"}),
		"a --detach past the -- separator belongs to the forwarded tool and must survive verbatim")
}

// TestLocalOnlyFlagsNeverReachTheDaemon guards the argv that is re-submitted.
//
// --detach left in would make the daemon detach again, handing the work to
// itself forever. --wait left in would be acted on by a run that is not
// detaching, where it is a usage error - so a valid local invocation would
// arrive at the daemon as an invalid one.
func TestLocalOnlyFlagsNeverReachTheDaemon(t *testing.T) {
	got := withoutDetachFlag([]string{
		"ci", "-detach", "docs", "--detach", "--detach=true", "--wait", "--wait=true", "--detach-me", "--waiting",
	})
	assert.Equal(t, []string{"ci", "docs", "--detach-me", "--waiting"}, got,
		"both local flags go, in every spelling; lookalikes stay")

	assert.Equal(t, []string{"ci", "--", "--wait"},
		withoutDetachFlag([]string{"ci", "--", "--wait"}),
		"past the separator the tokens belong to the forwarded tool")
}
