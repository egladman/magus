package cache_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/cache"
)

// discardLogger is a slog.Logger that drops all output, keeping benchmark
// results clean.
var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// openBenchCache opens a cache at dir and a discarding logger so benchmark
// output is not polluted with log lines.
func openBenchCache(b *testing.B, dir string, mutable bool) *cache.Cache {
	b.Helper()
	c, err := cache.Open(dir, cache.WithMutable(mutable), cache.WithLogger(discardLogger))
	if err != nil {
		b.Fatalf("cache.Open: %v", err)
	}
	return c
}

// writeBenchProject writes a minimal Go source file for project under root
// and returns the path of the declared output file the build fn must create.
func writeBenchProject(b *testing.B, root, project string) (outPath string) {
	b.Helper()
	src := filepath.Join(root, project, "main.go")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	return filepath.Join(project, "out.bin")
}

// benchSpec returns a Spec for a project in root with one declared output.
func benchSpec(root, project, outRel string) cache.Spec {
	return cache.Spec{
		ProjectPath:   project,
		Sources:       []string{project + "/*.go"},
		Outputs:       []string{outRel},
		WorkspaceRoot: root,
		Target:        "build",
	}
}

// buildFn returns a fn that creates the declared output file at
// <root>/<outRel>, simulating a build step with negligible CPU cost.
func buildFn(root, outRel string) func(context.Context) error {
	return func(_ context.Context) error {
		abs := filepath.Join(root, outRel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		return os.WriteFile(abs, []byte("binary"), 0o755)
	}
}

// BenchmarkCacheMiss measures the full miss path: spec hashing, fn execution,
// output snapshotting, and manifest writing. A fresh cache dir per iteration
// ensures every Run is a miss.
func BenchmarkCacheMiss(b *testing.B) {
	root := b.TempDir()
	outRel := writeBenchProject(b, root, "pkg")
	spec := benchSpec(root, "pkg", outRel)
	fn := buildFn(root, outRel)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cdir := filepath.Join(b.TempDir(), ".magus-cache")
		c := openBenchCache(b, cdir, true)
		if _, err := c.Run(ctx, spec, fn); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCacheHit measures the hot replay path: spec hashing, manifest
// lookup, and output extraction from the content-addressed store.
// The cache is pre-populated once before the loop so every b.N iteration
// is a guaranteed hit — the build fn is never called.
func BenchmarkCacheHit(b *testing.B) {
	root := b.TempDir()
	cdir := filepath.Join(b.TempDir(), ".magus")
	outRel := writeBenchProject(b, root, "pkg")
	spec := benchSpec(root, "pkg", outRel)
	fn := buildFn(root, outRel)
	ctx := context.Background()

	// Warm the cache.
	warm := openBenchCache(b, cdir, true)
	if _, err := warm.Run(ctx, spec, fn); err != nil {
		b.Fatalf("warm run: %v", err)
	}

	c := openBenchCache(b, cdir, true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r, err := c.Run(ctx, spec, fn)
		if err != nil {
			b.Fatal(err)
		}
		if !r.Hit {
			b.Fatal("expected cache hit; got miss")
		}
	}
}

// BenchmarkRunAll measures concurrent fan-out across 8 projects at varying
// concurrency levels. The cache is pre-populated so each iteration is pure
// replay + goroutine scheduling — no build work touches the CPU.
//
// Run with: go test -bench=BenchmarkRunAll -benchtime=5s ./magus/cache/
func BenchmarkRunAll(b *testing.B) {
	const n = 8
	for _, concurrency := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("concurrency=%d", concurrency), func(b *testing.B) {
			root := b.TempDir()
			cdir := filepath.Join(b.TempDir(), ".magus")

			specs := make([]cache.Spec, n)
			fns := make([]func(context.Context) error, n)
			for i := range specs {
				p := fmt.Sprintf("svc-%d", i+1)
				outRel := writeBenchProject(b, root, p)
				specs[i] = benchSpec(root, p, outRel)
				fns[i] = buildFn(root, outRel)
			}

			ctx := context.Background()

			// Pre-populate the cache so RunAll replays on every b.N iteration.
			warm := openBenchCache(b, cdir, true)
			for i, s := range specs {
				s := s
				fn := fns[i]
				if _, err := warm.Run(ctx, s, fn); err != nil {
					b.Fatalf("warm run %s: %v", s.ProjectPath, err)
				}
			}

			c := openBenchCache(b, cdir, true)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.RunAll(ctx, specs, func(_ context.Context, s cache.Spec) error {
					// fn not called on hit; this closure is only needed for the
					// RunAll signature. The warm cache ensures it is never invoked.
					return nil
				}, cache.WithConcurrency(concurrency)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
