package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/depgraph"
	"github.com/egladman/magus/types"
)

// writeMarker creates a magusfile.tl marker under root/path and returns the dir.
func writeMarker(b *testing.B, root, relPath string) {
	b.Helper()
	dir := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "magusfile.tl"), []byte(""), 0o644); err != nil {
		b.Fatal(err)
	}
}

// buildSyntheticWorkspace creates a tree with n projects under root.
func buildSyntheticWorkspace(b *testing.B, root string, n int) {
	b.Helper()
	for i := range n {
		writeMarker(b, root, fmt.Sprintf("svc%02d", i))
	}
}

// BenchmarkInspect measures the cost of walking a workspace directory tree
// and discovering project markers. Scales with workspace size.
func BenchmarkInspect(b *testing.B) {
	for _, n := range []int{10, 50, 100} {
		n := n
		b.Run(fmt.Sprintf("projects=%d", n), func(b *testing.B) {
			root := b.TempDir()
			buildSyntheticWorkspace(b, root, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := Discover(context.Background(), root)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkGraph measures the cost of building the dependency DAG from
// a Workspace. Edges are added so that the last project depends on the first,
// forming a wide fan (no cycle).
func BenchmarkGraph(b *testing.B) {
	for _, n := range []int{10, 50, 100} {
		n := n
		b.Run(fmt.Sprintf("projects=%d", n), func(b *testing.B) {
			// Build workspace manually to avoid filesystem cost per iteration.
			ws := &types.Workspace{
				Root:     b.TempDir(),
				Projects: make(map[string]*types.Project, n),
			}
			paths := make([]string, n)
			for i := range n {
				p := fmt.Sprintf("svc%02d", i)
				paths[i] = p
				ws.Projects[p] = &types.Project{Path: p}
			}
			// Make a linear chain: svc01 → svc00, svc02 → svc01, …
			for i := 1; i < n; i++ {
				ws.Projects[paths[i]].DependsOn = []string{paths[i-1]}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := depgraph.Build(ws)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkAffectedFromPaths measures how quickly AffectedFromPaths resolves
// the closure for a set of changed files in a medium-sized workspace.
func BenchmarkAffectedFromPaths(b *testing.B) {
	const n = 50
	root := b.TempDir()
	buildSyntheticWorkspace(b, root, n)

	ws, err := Discover(context.Background(), root)
	if err != nil {
		b.Fatal(err)
	}
	// Point to a real file under svc00 as the changed path.
	changed := []string{filepath.Join(root, "svc00", "magusfile.tl")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := AffectedFromPaths(context.Background(), ws, changed)
		if err != nil {
			b.Fatal(err)
		}
	}
}
