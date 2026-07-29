package magus

import (
	"os"
	"path/filepath"
	"testing"
)

// mkTree creates dirs and marker files under root. Each entry is a path relative to
// root; a trailing "/" makes it a directory, otherwise it is an empty marker file.
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

// TestFindRootWithLineage pins the one-workspace rule. A magusfile marks a PROJECT,
// not a workspace, so the walk must accrue enclosing projects and keep going rather
// than stopping at the first one it meets.
func TestFindRootWithLineage(t *testing.T) {
	t.Run("a nested project does not become its own workspace", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, "magus.yaml", "magusfile.buzz", "console/magusfile.buzz")

		got, lineage, err := FindRootWithLineage(filepath.Join(root, "console"))
		if err != nil {
			t.Fatalf("FindRootWithLineage: %v", err)
		}
		if got != root {
			t.Errorf("root = %q, want %q; halting at console's magusfile is the bug this guards", got, root)
		}
		if len(lineage) != 1 || lineage[0] != "console" {
			t.Errorf("lineage = %v, want [console]", lineage)
		}
	})

	t.Run("deep nesting records the whole chain innermost first", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, "magus.yaml", "docs/magusfile.buzz", "docs/guides/agents/magusfile.buzz")

		got, lineage, err := FindRootWithLineage(filepath.Join(root, "docs", "guides", "agents"))
		if err != nil {
			t.Fatalf("FindRootWithLineage: %v", err)
		}
		if got != root {
			t.Errorf("root = %q, want %q", got, root)
		}
		want := []string{"docs/guides/agents", "docs"}
		if len(lineage) != len(want) || lineage[0] != want[0] || lineage[1] != want[1] {
			t.Errorf("lineage = %v, want %v (innermost first)", lineage, want)
		}
	})

	t.Run("a root that is also a project still ends the walk", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, "go.mod", "magusfile.buzz")

		got, lineage, err := FindRootWithLineage(root)
		if err != nil {
			t.Fatalf("FindRootWithLineage: %v", err)
		}
		if got != root {
			t.Errorf("root = %q, want %q", got, root)
		}
		// A workspace is not nested inside itself, so it never appears in its own lineage.
		if len(lineage) != 0 {
			t.Errorf("lineage = %v, want empty for the root itself", lineage)
		}
	})

	t.Run("a standalone project with no workspace marker still resolves", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, "solo/magusfile.buzz", "solo/nested/magusfile.buzz")

		got, _, err := FindRootWithLineage(filepath.Join(root, "solo", "nested"))
		if err != nil {
			t.Fatalf("FindRootWithLineage: %v", err)
		}
		// The OUTERMOST project wins, so a nested one never silently becomes the
		// workspace while a larger enclosing unit exists.
		if want := filepath.Join(root, "solo"); got != want {
			t.Errorf("root = %q, want %q", got, want)
		}
	})

	t.Run("no markers at all is an error", func(t *testing.T) {
		// t.TempDir() sits under the OS temp root, which has no markers above it on a
		// normal machine; guard by pointing at a directory that definitely has none.
		dir := filepath.Join(t.TempDir(), "empty")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, _, err := FindRootWithLineage(dir); err == nil {
			t.Skip("temp dir has a marker above it on this machine; nothing to assert")
		}
	})

	t.Run("FindRoot agrees with the lineage form", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, "magus.yaml", "web/magusfile.buzz")

		a, err := FindRoot(filepath.Join(root, "web"))
		if err != nil {
			t.Fatalf("FindRoot: %v", err)
		}
		b, _, err := FindRootWithLineage(filepath.Join(root, "web"))
		if err != nil {
			t.Fatalf("FindRootWithLineage: %v", err)
		}
		if a != b {
			t.Errorf("FindRoot = %q but FindRootWithLineage = %q; they must not diverge", a, b)
		}
	})
}
