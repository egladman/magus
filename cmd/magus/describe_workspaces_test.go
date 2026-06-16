package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/config"
)

// TestDescribeWorkspacesOutput_MultiDeclared verifies that `describe workspaces`
// enumerates every declared daemon workspace, not just the active one.
func TestDescribeWorkspacesOutput_MultiDeclared(t *testing.T) {
	base := t.TempDir()
	mkWorkspace := func(name string) string {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// An empty magusfile.buzz marks the directory as a workspace root.
		if err := os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	wsA, wsB := mkWorkspace("a"), mkWorkspace("b")

	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	globalCfg.Daemon.Workspaces = []string{wsA, wsB}

	out, err := describeWorkspacesOutput(context.Background(), "")
	if err != nil {
		t.Fatalf("describeWorkspacesOutput: %v", err)
	}
	if out.Count != 2 || len(out.Workspaces) != 2 {
		t.Fatalf("Count = %d, len(Workspaces) = %d, want 2 each", out.Count, len(out.Workspaces))
	}
	if out.Workspaces[0].Root == out.Workspaces[1].Root {
		t.Errorf("expected two distinct workspace roots, got %q twice", out.Workspaces[0].Root)
	}
}
