package audit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
)

// BenchmarkBeginNoDescendants is the hot path: project with zero
// registered descendants. Must allocate as little as possible and never
// touch the filesystem.
func BenchmarkBeginNoDescendants(b *testing.B) {
	ws := &fakeWS{projects: []*types.Project{{Path: "api", Dir: "/tmp/api"}}}
	ctx := types.WithWorkspace(context.Background(), ws)
	p := &types.Project{Path: "api", Dir: "/tmp/api"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if a := Begin(ctx, p, true); a != nil {
			b.Fatalf("expected nil")
		}
	}
}

// BenchmarkBeginReadOnly is the other hot path: write=false bails before
// touching ctx at all.
func BenchmarkBeginReadOnly(b *testing.B) {
	ctx := context.Background()
	p := &types.Project{Path: "api", Dir: "/tmp/api"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if a := Begin(ctx, p, false); a != nil {
			b.Fatalf("expected nil")
		}
	}
}

// BenchmarkSnapshotAndDiff measures the audit-active path for a realistic
// descendant tree: 200 files across nested directories. Mutates one file
// per iteration so the diff produces a result.
func BenchmarkSnapshotAndDiff(b *testing.B) {
	dir := b.TempDir()
	for i := 0; i < 200; i++ {
		sub := filepath.Join(dir, fmt.Sprintf("d%02d", i/20))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			b.Fatal(err)
		}
		path := filepath.Join(sub, fmt.Sprintf("file%03d.txt", i))
		if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	descs := []descendant{{path: "d", dir: dir}}
	mutateTarget := filepath.Join(dir, "d00", "file000.txt")
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		snap, err := take(ctx, descs)
		if err != nil {
			b.Fatal(err)
		}
		// Mutate one file so diff has work to do.
		if err := os.WriteFile(mutateTarget, []byte(fmt.Sprintf("v%d", i)), 0o644); err != nil {
			b.Fatal(err)
		}
		if d := diff(ctx, snap, descs); len(d) == 0 {
			b.Fatal("expected at least one change")
		}
	}
}
