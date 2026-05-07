package project_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"

	"github.com/egladman/magus/project"
)

// newAffectedWorkspace lays down a workspace with three projects in
// three languages, useful for exercising affected detection.
func newAffectedWorkspace(t *testing.T) *types.Workspace {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{
		"api/magusfile.tl",
		"web/studio/magusfile.tl",
		"extensions/drape/magusfile.tl",
	} {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := project.Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// TestAffectedFromPathsHappyPath verifies that AffectedFromPaths correctly
// maps file paths to seed projects and computes changed/seed sets.
func TestAffectedFromPathsHappyPath(t *testing.T) {
	ws := newAffectedWorkspace(t)

	r, err := project.AffectedFromPaths(context.Background(), ws, []string{
		"api/magusfile.tl",
		"web/studio/magusfile.tl",
	})
	if err != nil {
		t.Fatalf("AffectedFromPaths: %v", err)
	}
	wantSeed := []string{"api", "web/studio"}
	if !equalSlice(r.Seed, wantSeed) {
		t.Errorf("Seed = %v, want %v", r.Seed, wantSeed)
	}
	if r.FilesBySeed["api"] == nil || r.FilesBySeed["web/studio"] == nil {
		t.Errorf("FilesBySeed missing entries: %v", r.FilesBySeed)
	}
}

// TestAffectedDisabledReturnsErrFallback verifies that MAGUS_VCS_ENABLED=false
// causes Affected to return ErrAffectedFallback.
func TestAffectedDisabledReturnsErrFallback(t *testing.T) {
	ws := newAffectedWorkspace(t)
	t.Setenv("MAGUS_VCS_ENABLED", "false")

	_, err := project.Affected(context.Background(), ws, "")
	if !errors.Is(err, types.ErrAffectedFallback) {
		t.Fatalf("err = %v, want errors.Is(err, ErrAffectedFallback)", err)
	}
}

// TestAffectedFromPathsOutsideWorkspace verifies that absolute paths outside
// the workspace root are silently skipped.
func TestAffectedFromPathsOutsideWorkspace(t *testing.T) {
	ws := newAffectedWorkspace(t)

	r, err := project.AffectedFromPaths(context.Background(), ws, []string{
		"api/magusfile.tl",
		"/tmp/outside-workspace/file.go",
	})
	if err != nil {
		t.Fatalf("AffectedFromPaths: %v", err)
	}
	wantSeed := []string{"api"}
	if !equalSlice(r.Seed, wantSeed) {
		t.Errorf("Seed = %v, want %v", r.Seed, wantSeed)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// countObserver counts the graph events it receives.
type countObserver struct{ builds, queries, errs int }

func (c *countObserver) OnBuild(types.BuildStats) { c.builds++ }
func (c *countObserver) OnQuery(types.QueryEvent) { c.queries++ }
func (c *countObserver) OnError(error)            { c.errs++ }

// TestAffectedRequestScopedObserver verifies the C4 fix: a graph observer is
// scoped per request via context, not shared workspace state. Two independent
// calls each route to their own observer (exactly one build each), and a call
// with no observer touches neither — so concurrent daemon requests cannot
// clobber each other's observer.
func TestAffectedRequestScopedObserver(t *testing.T) {
	ws := newAffectedWorkspace(t)

	obsA := &countObserver{}
	obsB := &countObserver{}
	if _, err := project.AffectedFromPaths(types.ContextWithGraphObserver(context.Background(), obsA), ws, []string{"api/x.go"}); err != nil {
		t.Fatalf("call A: %v", err)
	}
	if _, err := project.AffectedFromPaths(types.ContextWithGraphObserver(context.Background(), obsB), ws, []string{"api/x.go"}); err != nil {
		t.Fatalf("call B: %v", err)
	}
	if obsA.builds != 1 {
		t.Errorf("observer A builds = %d, want 1", obsA.builds)
	}
	if obsB.builds != 1 {
		t.Errorf("observer B builds = %d, want 1", obsB.builds)
	}

	if _, err := project.AffectedFromPaths(context.Background(), ws, []string{"api/x.go"}); err != nil {
		t.Fatalf("call C: %v", err)
	}
	if obsA.builds != 1 || obsB.builds != 1 {
		t.Errorf("an observerless call leaked events: A=%d B=%d", obsA.builds, obsB.builds)
	}
}
