package main

// In-process benchmarks for magus.Open itself, separate from the cmd-level
// startup() pipeline benchmarks in startup_bench_test.go.
//
// Hypothesis B (cross-run magusfile parse-table cache) needs to score the
// residual cost of Open after the Teal→Lua bytecode cache in
// internal/runtime/compile.go is fully warm. BenchmarkStartupLs already
// measures whole-startup cost; the benchmarks here isolate the Open path
// so hypothesis-B work can be evaluated against a cleaner baseline.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus"
)

// openBenchJSWorkspace creates nProjects JS project directories (package.json
// marker only, no magusfile.tl) so project.Discover auto-detects them as JS
// projects. Used to measure Open on a pure JS workspace where preloadMagusfiles
// is a no-op and all cost is borne by Inspect + cache replay.
func openBenchJSWorkspace(tb testing.TB, nProjects int) string {
	tb.Helper()
	root := tb.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module openbench\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < nProjects; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg-%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"pkg"}`), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}

func openBenchWorkspace(tb testing.TB, numTargets int) string {
	tb.Helper()
	root := tb.TempDir()
	proj := filepath.Join(root, "svc")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		tb.Fatal(err)
	}
	var sb strings.Builder
	for i := 0; i < numTargets; i++ {
		fmt.Fprintf(&sb, "global function t_%d(args: {string}) end\n", i)
	}
	if err := os.WriteFile(filepath.Join(proj, "magusfile.tl"), []byte(sb.String()), 0o644); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module openbench\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	return root
}

// BenchmarkMagusOpenCold measures magus.Open on a fresh workspace each
// iteration. Every iteration pays full Teal compile + binding registration.
func BenchmarkMagusOpenCold(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		root := openBenchWorkspace(b, 10)
		b.StartTimer()

		m, err := magus.Open(ctx, root)
		if err != nil {
			b.Fatal(err)
		}
		_ = m.Close()
	}
}

// BenchmarkMagusOpenWarmGraphCache measures magus.Open on a 50-project pure JS
// workspace where the workspace.cache.json is already warm (disk cache valid).
// On a warm cache, project.Discover replays the graph from disk (no FS walk),
// preloadMagusfiles is a no-op (no .tl files), and registry.Apply resolves
// spell names. This is the cold-process / warm-disk hot path for JS monorepos.
//
// optimization: workspace.cache.json fast path (project.Discover restoreFromCache)
//
//	skips the full directory walk + autoDetect goroutine pool on warm disk.
//	For a 50-project JS workspace, the cache hit collapses Inspect from
//	O(D) WalkDir syscalls to a single ReadFile + N Lstat calls (one per dir
//	in DirMtimes). Combined with preloadMagusfiles being a no-op for
//	pure JS workspaces, total Open cost drops by ~80% vs cold.
//	measured: BenchmarkMagusOpenWarmGraphCache vs BenchmarkMagusOpenCold.
//	trade-off: cache is invalidated on any directory mtime change; over-
//	  invalidates on dir-only touches but never serves stale projects.
//	assumes: spell registry is process-deterministic — restoreFromCache
//	  binds spells by name from DefaultSpellRegistry().
func BenchmarkMagusOpenWarmGraphCache(b *testing.B) {
	ctx := context.Background()
	root := openBenchJSWorkspace(b, 50)

	// Warm the on-disk workspace cache with one full Open.
	warm, err := magus.Open(ctx, root)
	if err != nil {
		b.Fatal(err)
	}
	_ = warm.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, err := magus.Open(ctx, root)
		if err != nil {
			b.Fatal(err)
		}
		_ = m.Close()
	}
	b.Logf("target: <5 ms/op (informational; vs ~50 ms cold 50-project walk)")
}

// BenchmarkMagusOpenWarmAOT measures magus.Open after one previous Open
// against the same workspace — the in-process compile cache is fully warm
// so this isolates the per-Open residual that hypothesis B targets.
func BenchmarkMagusOpenWarmAOT(b *testing.B) {
	ctx := context.Background()
	root := openBenchWorkspace(b, 10)

	warm, err := magus.Open(ctx, root)
	if err != nil {
		b.Fatal(err)
	}
	_ = warm.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, err := magus.Open(ctx, root)
		if err != nil {
			b.Fatal(err)
		}
		_ = m.Close()
	}
}
