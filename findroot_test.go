package magus

import (
	"os"
	"path/filepath"
	"testing"
)

// mkTree creates dirs and marker files under root. A trailing "/" makes a directory,
// otherwise an empty marker file.
func mkTree(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if p[len(p)-1] == '/' {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", p, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", p, err)
		}
		if err := os.WriteFile(full, nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

func mustFindRoot(t *testing.T, dir string) string {
	t.Helper()
	got, err := FindRoot(dir)
	if err != nil {
		t.Fatalf("FindRoot(%s): %v", dir, err)
	}
	return got
}

// TestFindRoot pins the one-workspace rule: a magusfile marks a PROJECT, and only
// magus.yaml declares where a workspace begins.
func TestFindRoot(t *testing.T) {
	t.Run("a nested project does not become its own workspace", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, "magus.yaml", "magusfile.buzz", "console/magusfile.buzz")
		if got := mustFindRoot(t, filepath.Join(root, "console")); got != root {
			t.Errorf("root = %q, want %q; halting at console's magusfile is the original bug", got, root)
		}
	})

	// The regression the first attempt shipped: go.mod was treated as a workspace
	// marker on the theory it only sits at a repo's top, which is false in any
	// multi-module repo. A submodule then locked its own cache dir and stopped
	// excluding the root run despite touching the same files.
	t.Run("a nested go.mod does not become its own workspace", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, "magus.yaml", "go.mod", "libs/diagnostics/go.mod", "libs/diagnostics/magusfile.buzz")
		if got := mustFindRoot(t, filepath.Join(root, "libs", "diag")); got != root {
			t.Errorf("root = %q, want %q; a submodule must not split the workspace", got, root)
		}
	})

	// Worktrees nest INSIDE their parent repo, so an outermost-wins rule swallows
	// them. The nearest declaration governs, the same way .git behaves.
	t.Run("nearest magus.yaml wins so a nested worktree keeps its own", func(t *testing.T) {
		outer := t.TempDir()
		mkTree(t, outer, "magus.yaml", "magusfile.buzz",
			".claude/worktrees/feature/magus.yaml",
			".claude/worktrees/feature/magusfile.buzz",
			".claude/worktrees/feature/console/magusfile.buzz")

		wt := filepath.Join(outer, ".claude", "worktrees", "feature")
		if got := mustFindRoot(t, wt); got != wt {
			t.Errorf("root = %q, want the worktree %q, not its parent repo", got, wt)
		}
		if got := mustFindRoot(t, filepath.Join(wt, "console")); got != wt {
			t.Errorf("root from inside the worktree = %q, want %q", got, wt)
		}
		if got := mustFindRoot(t, outer); got != outer {
			t.Errorf("root from the parent = %q, want %q", got, outer)
		}
	})

	t.Run("a standalone project with no magus.yaml resolves to its outermost marker", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, "solo/magusfile.buzz", "solo/nested/magusfile.buzz")
		if got, want := mustFindRoot(t, filepath.Join(root, "solo", "nested")), filepath.Join(root, "solo"); got != want {
			t.Errorf("root = %q, want %q", got, want)
		}
	})

	// Contiguity bounds the fallback: without it, one stray magusfile in a home
	// directory would adopt every project beneath it.
	t.Run("a gap stops the fallback from reaching a stray ancestor", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, "magusfile.buzz", "gap/", "gap/proj/magusfile.buzz")
		if got, want := mustFindRoot(t, filepath.Join(root, "gap", "proj")), filepath.Join(root, "gap", "proj"); got != want {
			t.Errorf("root = %q, want %q; the marker-less gap must stop the walk", got, want)
		}
	})
}

// TestFindRootStopsAtRepositoryBoundary pins the boundary on the magus.yaml search.
//
// The contiguity rule already refused to let a stray magusfile in an unrelated
// ancestor adopt everything beneath it, but workspaceMarker was checked first and
// unconditionally, so it skipped that defence entirely. A planted /tmp/magus.yaml or
// $HOME/magus.yaml captured every repo below it and outranked that repo's own go.mod
// and magusfile.buzz - handing its sandbox allowlist, default charms and daemon
// address to builds inside someone else's project.
func TestFindRootStopsAtRepositoryBoundary(t *testing.T) {
	t.Run("planted ancestor magus.yaml does not capture a repo", func(t *testing.T) {
		tmp := t.TempDir()
		mkTree(t, tmp,
			"magus.yaml",  // the attacker's plant, above the repo
			"repo/.git/",  // the victim's repository boundary
			"repo/go.mod", // and its own project marker
			"repo/magusfile.buzz",
			"repo/sub/",
		)
		got, err := FindRoot(filepath.Join(tmp, "repo", "sub"))
		if err != nil {
			t.Fatalf("FindRoot: %v", err)
		}
		if want := filepath.Join(tmp, "repo"); got != want {
			t.Fatalf("root escaped the repository boundary:\n got  %s\n want %s", got, want)
		}
	})

	t.Run("magus.yaml inside the repo still wins", func(t *testing.T) {
		tmp := t.TempDir()
		mkTree(t, tmp,
			"repo/.git/",
			"repo/magus.yaml",
			"repo/libs/thing/go.mod",
		)
		got, err := FindRoot(filepath.Join(tmp, "repo", "libs", "thing"))
		if err != nil {
			t.Fatalf("FindRoot: %v", err)
		}
		if want := filepath.Join(tmp, "repo"); got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	})

	t.Run("a workspace that is not a repository is unaffected", func(t *testing.T) {
		tmp := t.TempDir()
		mkTree(t, tmp, "ws/magus.yaml", "ws/sub/")
		got, err := FindRoot(filepath.Join(tmp, "ws", "sub"))
		if err != nil {
			t.Fatalf("FindRoot: %v", err)
		}
		if want := filepath.Join(tmp, "ws"); got != want {
			t.Fatalf("got %s, want %s", got, want)
		}
	})
}
