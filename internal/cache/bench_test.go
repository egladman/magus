package cache

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// discardLogger is a slog.Logger that drops all output, keeping benchmark
// results clean.
var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// openBenchCache opens a cache at dir and a discarding logger so benchmark
// output is not polluted with log lines.
func openBenchCache(b *testing.B, dir string, mutable bool) *Cache {
	b.Helper()
	c, err := Open(dir, WithMutable(mutable), WithLogger(discardLogger))
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

// benchStep returns a Step for a project in root with one declared output.
func benchStep(root, project, outRel string) Step {
	return Step{
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

// BenchmarkCacheMiss measures the full miss path: step hashing, fn execution,
// output snapshotting, and manifest writing. A fresh cache dir per iteration
// ensures every Run is a miss.
func BenchmarkCacheMiss(b *testing.B) {
	root := b.TempDir()
	outRel := writeBenchProject(b, root, "pkg")
	step := benchStep(root, "pkg", outRel)
	fn := buildFn(root, outRel)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cdir := filepath.Join(b.TempDir(), ".magus-cache")
		c := openBenchCache(b, cdir, true)
		if _, err := c.Run(ctx, step, fn); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCacheHit measures the hot replay path: step hashing, manifest
// lookup, and output extraction from the content-addressed store.
// The cache is pre-populated once before the loop so every b.N iteration
// is a guaranteed hit — the build fn is never called.
func BenchmarkCacheHit(b *testing.B) {
	root := b.TempDir()
	cdir := filepath.Join(b.TempDir(), ".magus")
	outRel := writeBenchProject(b, root, "pkg")
	step := benchStep(root, "pkg", outRel)
	fn := buildFn(root, outRel)
	ctx := context.Background()

	// Warm the cache.
	warm := openBenchCache(b, cdir, true)
	if _, err := warm.Run(ctx, step, fn); err != nil {
		b.Fatalf("warm run: %v", err)
	}

	c := openBenchCache(b, cdir, true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r, err := c.Run(ctx, step, fn)
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

			steps := make([]Step, n)
			fns := make([]func(context.Context) error, n)
			for i := range steps {
				p := fmt.Sprintf("svc-%d", i+1)
				outRel := writeBenchProject(b, root, p)
				steps[i] = benchStep(root, p, outRel)
				fns[i] = buildFn(root, outRel)
			}

			ctx := context.Background()

			// Pre-populate the cache so RunAll replays on every b.N iteration.
			warm := openBenchCache(b, cdir, true)
			for i, s := range steps {
				s := s
				fn := fns[i]
				if _, err := warm.Run(ctx, s, fn); err != nil {
					b.Fatalf("warm run %s: %v", s.ProjectPath, err)
				}
			}

			c := openBenchCache(b, cdir, true)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.RunAll(ctx, steps, func(_ context.Context, s Step) error {
					// fn not called on hit; this closure is only needed for the
					// RunAll signature. The warm cache ensures it is never invoked.
					return nil
				}, WithConcurrency(concurrency)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// writeBenchProjectFiles writes nFiles Go source files for project under root and
// returns the declared output path. Many files per project spread hashes across
// many of the 256 mtime shards, so a multi-project cold build shares shards across
// steps — the case where per-step mtime flushing rewrites accumulating shards.
func writeBenchProjectFiles(b *testing.B, root, project string, nFiles int) (outRel string) {
	b.Helper()
	dir := filepath.Join(root, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < nFiles; i++ {
		src := filepath.Join(dir, fmt.Sprintf("f%03d.go", i))
		body := fmt.Sprintf("package p\n\nvar V%d = %d\n", i, i)
		if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return filepath.Join(project, "out.bin")
}

// BenchmarkRunAllColdMtime measures a cold (cache-empty) fan-out build over a
// multi-file, multi-project workspace — the path where the mtime store is
// re-populated and persisted. It is the benchmark behind moving the mtime flush
// from per-step (inside hashStep) to once-per-RunAll: with per-step flush each
// completing step rewrites every shard it shares with earlier steps, so the bytes
// written to the shard files grow super-linearly in the project count.
func BenchmarkRunAllColdMtime(b *testing.B) {
	const (
		nProjects = 24
		nFiles    = 64
	)
	root := b.TempDir()
	steps := make([]Step, nProjects)
	fns := make([]func(context.Context) error, nProjects)
	for i := range steps {
		p := fmt.Sprintf("svc-%02d", i)
		outRel := writeBenchProjectFiles(b, root, p, nFiles)
		steps[i] = benchStep(root, p, outRel)
		fns[i] = buildFn(root, outRel)
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Fresh cache dir per iteration: every Run is a miss, so all source files
		// are hashed and the mtime shards are dirtied and flushed.
		cdir := filepath.Join(b.TempDir(), ".magus")
		c := openBenchCache(b, cdir, true)
		if _, err := c.RunAll(ctx, steps, func(ctx context.Context, s Step) error {
			return fns[indexOfStep(steps, s.ProjectPath)](ctx)
		}, WithConcurrency(8)); err != nil {
			b.Fatal(err)
		}
	}
}

func indexOfStep(steps []Step, project string) int {
	for i := range steps {
		if steps[i].ProjectPath == project {
			return i
		}
	}
	return 0
}
