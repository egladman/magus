package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
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

func (f *fakeWS) ExpandAffected(context.Context, string, string) ([]types.Target, string, error) {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
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
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if _, ok := pre[filepath.Join(dir, ".git", "HEAD")]; ok {
		t.Fatalf("snapshot included .git contents")
	}
	if len(pre) != 4 {
		t.Fatalf("expected 4 tracked files, got %d: %v", len(pre), fmt.Sprintf("%v", pre))
	}

	// Mutate: one new, one modified, one removed.
	writeFile(t, filepath.Join(dir, "added.txt"), "e", t2)
	writeFile(t, filepath.Join(dir, "modify.txt"), "bb", t2)
	if err := os.Remove(filepath.Join(dir, "remove.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	got := diff(context.Background(), pre, descs)
	want := map[string]changeKind{
		filepath.Join(dir, "added.txt"):  changeAdded,
		filepath.Join(dir, "modify.txt"): changeModified,
		filepath.Join(dir, "remove.txt"): changeRemoved,
	}
	if len(got) != len(want) {
		t.Fatalf("diff length: got %d (%v), want %d", len(got), got, len(want))
	}
	for _, c := range got {
		w, ok := want[c.path]
		if !ok {
			t.Errorf("unexpected change: %v", c)
			continue
		}
		if c.kind != w {
			t.Errorf("path %s: kind got %d want %d", c.path, c.kind, w)
		}
	}
}

func TestBeginSkipsWhenReadOnly(t *testing.T) {
	if a := Begin(context.Background(), &types.Project{Path: "p"}, false); a != nil {
		t.Fatalf("Begin with write=false should return nil, got %v", a)
	}
}

func TestBeginSkipsWithoutWorkspace(t *testing.T) {
	if a := Begin(context.Background(), &types.Project{Path: "p"}, true); a != nil {
		t.Fatalf("Begin without workspace ctx should return nil")
	}
}

func TestBeginSkipsWithoutDescendants(t *testing.T) {
	ws := &fakeWS{projects: []*types.Project{{Path: "api", Dir: "/tmp/api"}}}
	ctx := types.WithWorkspace(context.Background(), ws)
	if a := Begin(ctx, &types.Project{Path: "api", Dir: "/tmp/api"}, true); a != nil {
		t.Fatalf("Begin without descendants should return nil")
	}
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
	if a := Begin(ctx, parent, true); a != nil {
		t.Fatalf("Begin should skip when the only descendant is in active dispatch")
	}
}

func TestFinishWarnsOnDescendantWrite(t *testing.T) {
	tmp := t.TempDir()
	parentDir := filepath.Join(tmp, "api")
	childDir := filepath.Join(tmp, "api", "docs")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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
	if a == nil {
		t.Fatalf("Begin returned nil; expected an audit")
	}

	// Simulate a spell on parent reformatting a file inside the child.
	writeFile(t, filepath.Join(childDir, "guide.md"), "# new", t2)

	a.Finish(ctx, "format")

	out := buf.String()
	if !strings.Contains(out, "MGS3001") ||
		!strings.Contains(out, "descendant project boundary crossed") ||
		!strings.Contains(out, "api/docs") ||
		!strings.Contains(out, "guide.md") ||
		!strings.Contains(out, "target=format") {
		t.Fatalf("expected warning with MGS3001/project/target/descendant/file; got:\n%s", out)
	}
}

func TestFinishSilentOnNoChanges(t *testing.T) {
	tmp := t.TempDir()
	parentDir := filepath.Join(tmp, "api")
	childDir := filepath.Join(tmp, "api", "docs")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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

	if buf.Len() != 0 {
		t.Fatalf("expected no warnings when descendant tree unchanged; got:\n%s", buf.String())
	}
}

func TestFinishNilReceiverNoop(t *testing.T) {
	var a *Audit
	// Should not panic and should not emit anything.
	a.Finish(context.Background(), "format")
}

func TestRootProjectTreatsAllOthersAsDescendants(t *testing.T) {
	tmp := t.TempDir()
	rootDir := tmp
	childDir := filepath.Join(tmp, "api")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	root := &types.Project{Path: ".", Dir: rootDir}
	child := &types.Project{Path: "api", Dir: childDir}
	ws := &fakeWS{projects: []*types.Project{root, child}}

	descs := descendantsOf(ws, root, nil)
	if len(descs) != 1 || descs[0].path != "api" {
		t.Fatalf("root project should see api as descendant; got %v", descs)
	}

	// Sibling-of-root case: child has no descendants.
	descs = descendantsOf(ws, child, nil)
	if len(descs) != 0 {
		t.Fatalf("non-root leaf should have no descendants; got %v", descs)
	}
}

// TestNestedDescendantAttribution verifies the longest-prefix match in
// report() — a change inside api/docs/v2 should be blamed on
// api/docs/v2, not its parent api/docs.
func TestNestedDescendantAttribution(t *testing.T) {
	tmp := t.TempDir()
	parentDir := filepath.Join(tmp, "api")
	innerDir := filepath.Join(tmp, "api", "docs")
	leafDir := filepath.Join(tmp, "api", "docs", "v2")
	if err := os.MkdirAll(leafDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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
	if a == nil {
		t.Fatalf("Begin returned nil")
	}
	// topmostRoots must collapse api/docs/v2 into api/docs — only one
	// root to walk.
	if len(a.roots) != 1 || a.roots[0].path != "api/docs" {
		t.Fatalf("topmost roots should collapse to api/docs; got %v", a.roots)
	}

	writeFile(t, filepath.Join(leafDir, "page.md"), "# new", t2)
	a.Finish(ctx, "format")

	out := buf.String()
	if !strings.Contains(out, "descendant=api/docs/v2") {
		t.Fatalf("expected attribution to innermost descendant api/docs/v2; got:\n%s", out)
	}
	if strings.Contains(out, "descendant=api/docs ") || strings.Contains(out, "descendant=api/docs\n") {
		t.Fatalf("expected NO warning attributed to outer api/docs; got:\n%s", out)
	}
}

// TestReportCapsLargeFileLists verifies that a misbehaving spell that
// touches more than reportCap files produces a bounded slog payload and
// surfaces the total count.
func TestReportCapsLargeFileLists(t *testing.T) {
	tmp := t.TempDir()
	parentDir := filepath.Join(tmp, "api")
	childDir := filepath.Join(tmp, "api", "docs")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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
	if !strings.Contains(out, fmt.Sprintf("modified_total=%d", n)) {
		t.Fatalf("expected modified_total=%d when over the cap; got:\n%s", n, out)
	}
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
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
