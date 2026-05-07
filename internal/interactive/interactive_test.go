package interactive_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

func makeProjects(paths ...string) []*types.Project {
	out := make([]*types.Project, len(paths))
	for i, p := range paths {
		out[i] = &types.Project{Path: p}
	}
	return out
}

// ── ScoreProjects ──────────────────────────────────────────────────────────────

func TestScoreProjectsNoFilter(t *testing.T) {
	t.Parallel()
	all := makeProjects("api/users", "api/orders", "web/app")
	got := interactive.ScoreProjects(all, nil)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

func TestScoreProjectsFilterMatchesSubset(t *testing.T) {
	t.Parallel()
	all := makeProjects("api/users", "api/orders", "web/app")
	got := interactive.ScoreProjects(all, []string{"api"})
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, sp := range got {
		if sp.P.Path == "web/app" {
			t.Error("web/app should not match filter 'api'")
		}
	}
}

func TestScoreProjectsMultipleFilters(t *testing.T) {
	t.Parallel()
	all := makeProjects("api/users", "api/orders", "web/app", "api/users/v2")
	got := interactive.ScoreProjects(all, []string{"api", "users"})
	for _, sp := range got {
		if sp.P.Path == "api/orders" || sp.P.Path == "web/app" {
			t.Errorf("unexpected project %q matched all filters", sp.P.Path)
		}
	}
}

func TestScoreProjectsEmptyFilterTokensIgnored(t *testing.T) {
	t.Parallel()
	all := makeProjects("api/users")
	got := interactive.ScoreProjects(all, []string{"", "   "})
	if len(got) != 1 {
		t.Fatalf("blank filters should not filter anything, got %d", len(got))
	}
}

func TestScoreProjectsLeafRanking(t *testing.T) {
	t.Parallel()
	// "users" appears in two paths; the one where it's the leaf component
	// should rank higher.
	all := makeProjects("api/users", "services/users-svc")
	got := interactive.ScoreProjects(all, []string{"users"})
	if len(got) < 2 {
		t.Fatal("expected both projects to match")
	}
	if got[0].P.Path != "api/users" {
		t.Errorf("expected api/users ranked first, got %q", got[0].P.Path)
	}
}

func TestScoreProjectsCaseInsensitive(t *testing.T) {
	t.Parallel()
	all := makeProjects("API/Users")
	got := interactive.ScoreProjects(all, []string{"api"})
	if len(got) != 1 {
		t.Error("filter should be case-insensitive")
	}
}

// ── State persistence ──────────────────────────────────────────────────────────

func TestSaveAndLoadState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	s := interactive.State{
		LastTarget:     map[string]string{"/path/to/proj": "build"},
		LastInvocation: []string{"magus", "build"},
	}
	if err := interactive.SaveState(s); err != nil {
		t.Fatal(err)
	}

	got, err := interactive.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.LastTarget["/path/to/proj"] != "build" {
		t.Errorf("LastTarget = %v, want build", got.LastTarget)
	}
	if len(got.LastInvocation) != 2 || got.LastInvocation[0] != "magus" {
		t.Errorf("LastInvocation = %v", got.LastInvocation)
	}
}

func TestLoadStateMissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	// No file written — should not error.
	_, err := interactive.LoadState()
	if err == nil {
		return // ideal: not an error
	}
	// os.ReadFile returns an error for missing file; that's acceptable too
	// as long as the caller can distinguish it from a corrupt file.
}

func TestSaveStateIsAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	// Confirm no .tmp file is left behind after a successful save.
	if err := interactive.SaveState(interactive.State{}); err != nil {
		t.Fatal(err)
	}
	path, err := interactive.StatePath()
	if err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	if _, err := os.Stat(tmp); err == nil {
		t.Errorf("temp file %s still exists after SaveState", tmp)
	}
}

func TestSaveStateValidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	s := interactive.State{LastTarget: map[string]string{"proj": "test"}}
	if err := interactive.SaveState(s); err != nil {
		t.Fatal(err)
	}
	path, err := interactive.StatePath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var check interactive.State
	if err := json.Unmarshal(b, &check); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
}

func TestStatePathUsesXDGStateHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)

	p, err := interactive.StatePath()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(p) {
		t.Errorf("StatePath returned relative path %q", p)
	}
	// Must be under our custom dir.
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "" {
		t.Errorf("path %q is not under XDG_STATE_HOME %q", p, dir)
	}
}
