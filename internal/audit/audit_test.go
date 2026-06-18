package audit

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWS is a minimal types.WorkspaceRepository for audit tests. Only
// All() is exercised; the other methods panic so a test misuse fails
// loud instead of silently.
type fakeWS struct {
	projects []*types.Project
}

func (f *fakeWS) All() []*types.Project        { return f.projects }
func (f *fakeWS) Root() string                 { panic("not used") }
func (f *fakeWS) Get(string) *types.Project    { panic("not used") }
func (f *fakeWS) Graph() (*types.Graph, error) { panic("not used") }
func (f *fakeWS) VCSOptions() types.VCSOptions { panic("not used") }
func (f *fakeWS) Where(string) (*types.Project, bool) {
	panic("not used")
}

func (f *fakeWS) ExpandPath(types.Target) ([]types.Target, error) {
	panic("not used")
}

func (f *fakeWS) ExpandCwd(types.Target) ([]types.Target, bool, error) {
	panic("not used")
}

func (f *fakeWS) ExpandAffected(context.Context, string, string) ([]types.Target, string, bool, error) {
	panic("not used")
}

func (f *fakeWS) Affected(context.Context, string) (*types.AffectedResult, error) {
	panic("not used")
}

func (f *fakeWS) AffectedFromPaths(context.Context, []string) (*types.AffectedResult, error) {
	panic("not used")
}
func (f *fakeWS) DescribeSpells() types.SpellsOutput     { panic("not used") }
func (f *fakeWS) DescribeTargets() types.TargetsOutput   { panic("not used") }
func (f *fakeWS) DescribeGraph() types.TargetGraphOutput { panic("not used") }
func (f *fakeWS) DescribeProjects() types.ProjectsOutput { panic("not used") }
func (f *fakeWS) DescribeWorkspaces(types.WorkspaceConfig) types.WorkspacesOutput {
	panic("not used")
}

func (f *fakeWS) DescribeTarget(types.Target) (types.EvaluatedTargetsOutput, error) {
	panic("not used")
}

func (f *fakeWS) DescribeEvaluatedProjects() types.EvaluatedProjectsOutput {
	panic("not used")
}

// writeFile creates path with content and an explicit mtime so tests
// don't race with the filesystem's clock resolution.
func writeFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755), "mkdir %s", filepath.Dir(path))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644), "write %s", path)
	require.NoError(t, os.Chtimes(path, mtime, mtime), "chtimes %s", path)
}

func TestSnapshotAndDiff(t *testing.T) {
	dir := t.TempDir()
	t1 := time.Unix(1_700_000_000, 0)
	t2 := time.Unix(1_700_000_100, 0)

	writeFile(t, filepath.Join(dir, "keep.txt"), "a", t1)
	writeFile(t, filepath.Join(dir, "modify.txt"), "b", t1)
	writeFile(t, filepath.Join(dir, "remove.txt"), "c", t1)
	writeFile(t, filepath.Join(dir, "nested", "deep.txt"), "d", t1)
	// .git must be skipped wholesale.
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: x", t1)

	descs := []descendant{{path: "p", dir: dir}}
	pre, err := take(context.Background(), descs)
	require.NoError(t, err, "take")
	assert.NotContains(t, pre, filepath.Join(dir, ".git", "HEAD"), "snapshot included .git contents")
	require.Len(t, pre, 4, "expected 4 tracked files")

	// Mutate: one new, one modified, one removed.
	writeFile(t, filepath.Join(dir, "added.txt"), "e", t2)
	writeFile(t, filepath.Join(dir, "modify.txt"), "bb", t2)
	require.NoError(t, os.Remove(filepath.Join(dir, "remove.txt")), "remove")

	got := diff(context.Background(), pre, descs)
	want := map[string]changeKind{
		filepath.Join(dir, "added.txt"):  changeAdded,
		filepath.Join(dir, "modify.txt"): changeModified,
		filepath.Join(dir, "remove.txt"): changeRemoved,
	}
	// diff returns changes in arbitrary order; compare as a path→kind map.
	gotMap := make(map[string]changeKind, len(got))
	for _, c := range got {
		gotMap[c.path] = c.kind
	}
	assert.Equal(t, want, gotMap)
}

func TestBeginSkipsWhenReadOnly(t *testing.T) {
	a := Begin(context.Background(), &types.Project{Path: "p"}, false)
	assert.Nil(t, a, "Begin with write=false should return nil")
}

func TestBeginSkipsWithoutWorkspace(t *testing.T) {
	a := Begin(context.Background(), &types.Project{Path: "p"}, true)
	assert.Nil(t, a, "Begin without workspace ctx should return nil")
}

func TestBeginSkipsWithoutDescendants(t *testing.T) {
	ws := &fakeWS{projects: []*types.Project{{Path: "api", Dir: "/tmp/api"}}}
	ctx := types.WithWorkspace(context.Background(), ws)
	a := Begin(ctx, &types.Project{Path: "api", Dir: "/tmp/api"}, true)
	assert.Nil(t, a, "Begin without descendants should return nil")
}

func TestBeginSkipsActiveDispatchDescendant(t *testing.T) {
	tmp := t.TempDir()
	parent := &types.Project{Path: "api", Dir: filepath.Join(tmp, "api")}
	child := &types.Project{Path: "api/docs", Dir: filepath.Join(tmp, "api", "docs")}
	ws := &fakeWS{projects: []*types.Project{parent, child}}
	ctx := types.WithWorkspace(context.Background(), ws)
	ctx = types.WithActiveDispatch(ctx, map[string]struct{}{
		"api":      {},
		"api/docs": {}, // descendant is also being dispatched — skip it
	})
	a := Begin(ctx, parent, true)
	assert.Nil(t, a, "Begin should skip when the only descendant is in active dispatch")
}

func TestFinishWarnsOnDescendantWrite(t *testing.T) {
	tmp := t.TempDir()
	parentDir := filepath.Join(tmp, "api")
	childDir := filepath.Join(tmp, "api", "docs")
	require.NoError(t, os.MkdirAll(childDir, 0o755), "mkdir")
	t1 := time.Unix(1_700_000_000, 0)
	t2 := time.Unix(1_700_000_100, 0)
	writeFile(t, filepath.Join(childDir, "guide.md"), "# old", t1)

	parent := &types.Project{Path: "api", Dir: parentDir}
	child := &types.Project{Path: "api/docs", Dir: childDir}
	ws := &fakeWS{projects: []*types.Project{parent, child}}
	ctx := types.WithWorkspace(context.Background(), ws)
	// Active dispatch holds only the parent — child writes here are violations.
	ctx = types.WithActiveDispatch(ctx, map[string]struct{}{"api": {}})

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	a := Begin(ctx, parent, true)
	require.NotNil(t, a, "Begin returned nil; expected an audit")

	// Simulate a spell on parent reformatting a file inside the child.
	writeFile(t, filepath.Join(childDir, "guide.md"), "# new", t2)

	a.Finish(ctx, "format")

	out := buf.String()
	assert.Contains(t, out, "MGS3001", "expected warning with MGS3001")
	assert.Contains(t, out, "descendant project boundary crossed")
	assert.Contains(t, out, "api/docs")
	assert.Contains(t, out, "guide.md")
	assert.Contains(t, out, "target=format")
}

func TestFinishSilentOnNoChanges(t *testing.T) {
	tmp := t.TempDir()
	parentDir := filepath.Join(tmp, "api")
	childDir := filepath.Join(tmp, "api", "docs")
	require.NoError(t, os.MkdirAll(childDir, 0o755), "mkdir")
	t1 := time.Unix(1_700_000_000, 0)
	writeFile(t, filepath.Join(childDir, "guide.md"), "# untouched", t1)

	parent := &types.Project{Path: "api", Dir: parentDir}
	child := &types.Project{Path: "api/docs", Dir: childDir}
	ws := &fakeWS{projects: []*types.Project{parent, child}}
	ctx := types.WithWorkspace(context.Background(), ws)
	ctx = types.WithActiveDispatch(ctx, map[string]struct{}{"api": {}})

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	a := Begin(ctx, parent, true)
	a.Finish(ctx, "format")

	assert.Empty(t, buf.String(), "expected no warnings when descendant tree unchanged")
}

func TestFinishNilReceiverNoop(t *testing.T) {
	var a *Audit
	// Should not panic and should not emit anything.
	assert.NotPanics(t, func() { a.Finish(context.Background(), "format") })
}

func TestRootProjectTreatsAllOthersAsDescendants(t *testing.T) {
	tmp := t.TempDir()
	rootDir := tmp
	childDir := filepath.Join(tmp, "api")
	require.NoError(t, os.MkdirAll(childDir, 0o755), "mkdir")
	root := &types.Project{Path: ".", Dir: rootDir}
	child := &types.Project{Path: "api", Dir: childDir}
	ws := &fakeWS{projects: []*types.Project{root, child}}

	descs := descendantsOf(ws, root, nil)
	require.Len(t, descs, 1, "root project should see api as descendant; got %v", descs)
	assert.Equal(t, "api", descs[0].path, "root project should see api as descendant")

	// Sibling-of-root case: child has no descendants.
	descs = descendantsOf(ws, child, nil)
	assert.Empty(t, descs, "non-root leaf should have no descendants")
}

// TestNestedDescendantAttribution verifies the longest-prefix match in
// report() — a change inside api/docs/v2 should be blamed on
// api/docs/v2, not its parent api/docs.
func TestNestedDescendantAttribution(t *testing.T) {
	tmp := t.TempDir()
	parentDir := filepath.Join(tmp, "api")
	innerDir := filepath.Join(tmp, "api", "docs")
	leafDir := filepath.Join(tmp, "api", "docs", "v2")
	require.NoError(t, os.MkdirAll(leafDir, 0o755), "mkdir")
	t1 := time.Unix(1_700_000_000, 0)
	t2 := time.Unix(1_700_000_100, 0)
	writeFile(t, filepath.Join(leafDir, "page.md"), "# old", t1)

	parent := &types.Project{Path: "api", Dir: parentDir}
	inner := &types.Project{Path: "api/docs", Dir: innerDir}
	leaf := &types.Project{Path: "api/docs/v2", Dir: leafDir}
	ws := &fakeWS{projects: []*types.Project{parent, inner, leaf}}
	ctx := types.WithWorkspace(context.Background(), ws)
	ctx = types.WithActiveDispatch(ctx, map[string]struct{}{"api": {}})

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	a := Begin(ctx, parent, true)
	require.NotNil(t, a, "Begin returned nil")
	// topmostRoots must collapse api/docs/v2 into api/docs — only one
	// root to walk.
	require.Len(t, a.roots, 1, "topmost roots should collapse to api/docs; got %v", a.roots)
	assert.Equal(t, "api/docs", a.roots[0].path, "topmost roots should collapse to api/docs")

	writeFile(t, filepath.Join(leafDir, "page.md"), "# new", t2)
	a.Finish(ctx, "format")

	out := buf.String()
	assert.Contains(t, out, "descendant=api/docs/v2", "expected attribution to innermost descendant api/docs/v2")
	assert.NotContains(t, out, "descendant=api/docs ", "expected NO warning attributed to outer api/docs")
	assert.NotContains(t, out, "descendant=api/docs\n", "expected NO warning attributed to outer api/docs")
}

// TestReportCapsLargeFileLists verifies that a misbehaving spell that
// touches more than reportCap files produces a bounded slog payload and
// surfaces the total count.
func TestReportCapsLargeFileLists(t *testing.T) {
	tmp := t.TempDir()
	parentDir := filepath.Join(tmp, "api")
	childDir := filepath.Join(tmp, "api", "docs")
	require.NoError(t, os.MkdirAll(childDir, 0o755), "mkdir")
	t1 := time.Unix(1_700_000_000, 0)
	const n = reportCap + 10
	for i := 0; i < n; i++ {
		writeFile(t, filepath.Join(childDir, fmt.Sprintf("file%03d.md", i)), "x", t1)
	}

	parent := &types.Project{Path: "api", Dir: parentDir}
	child := &types.Project{Path: "api/docs", Dir: childDir}
	ws := &fakeWS{projects: []*types.Project{parent, child}}
	ctx := types.WithWorkspace(context.Background(), ws)
	ctx = types.WithActiveDispatch(ctx, map[string]struct{}{"api": {}})

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	a := Begin(ctx, parent, true)
	t2 := time.Unix(1_700_000_100, 0)
	for i := 0; i < n; i++ {
		writeFile(t, filepath.Join(childDir, fmt.Sprintf("file%03d.md", i)), "y", t2)
	}
	a.Finish(ctx, "format")

	out := buf.String()
	assert.Contains(t, out, fmt.Sprintf("modified_total=%d", n), "expected modified_total when over the cap")
}

// TestWalkHonoursContextCancellation verifies that a cancelled ctx
// short-circuits the walk between directory reads.
func TestWalkHonoursContextCancellation(t *testing.T) {
	tmp := t.TempDir()
	// Build a few files so the walk has something to do.
	for i := 0; i < 5; i++ {
		writeFile(t, filepath.Join(tmp, fmt.Sprintf("f%d.txt", i)), "x", time.Unix(1, 0))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := take(ctx, []descendant{{path: "p", dir: tmp}})
	assert.ErrorIs(t, err, context.Canceled)
}
