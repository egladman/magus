package cache

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// buildSyntheticJSWorkspace creates nProjects JS project directories under a
// temp root, each with nFilesEach source files (.ts, .js, package.json,
// pnpm-lock.yaml). Used to measure glob expansion under realistic JS monorepo
// conditions.
func buildSyntheticJSWorkspace(b *testing.B, nProjects, nFilesEach int) string {
	b.Helper()
	root := b.TempDir()
	for i := 0; i < nProjects; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg-%03d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"pkg"}`), 0o644); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte("lockfileVersion: '6.0'\n"), 0o644); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < nFilesEach; j++ {
			ext := "ts"
			if j%5 == 0 {
				ext = "js"
			}
			name := filepath.Join(dir, fmt.Sprintf("src/mod-%04d.%s", j, ext))
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(name, []byte("export const x = 1\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	return root
}

// BenchmarkHashFilesBatch measures hashFiles for varying batch sizes around
// the io_uring threshold (currently 32 in hash_iouring_linux.go). On Linux
// with io_uring available, sub-benchmarks ≥32 take the io_uring fast path;
// sub-benchmarks <32 fall through to the goroutine-pool tier. Files are
// freshly touched each iteration so the mtime fast-path never hits.
func BenchmarkHashFilesBatch(b *testing.B) {
	for _, n := range []int{8, 16, 32, 64, 128} {
		n := n
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			files := make([]relAbs, n)
			for i := range files {
				rel := fmt.Sprintf("f-%04d.txt", i)
				abs := filepath.Join(dir, rel)
				buf := make([]byte, 1024)
				for j := range buf {
					buf[j] = byte((i*7 + j) & 0xff)
				}
				if err := os.WriteFile(abs, buf, 0o644); err != nil {
					b.Fatal(err)
				}
				files[i] = relAbs{rel: rel, abs: abs}
			}

			c, err := Open(
				b.TempDir(),
				WithMutable(true),
				WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
			)
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				stamp := time.Now().Add(time.Duration(i+1) * time.Second)
				for _, f := range files {
					_ = os.Chtimes(f.abs, stamp, stamp)
				}
				b.StartTimer()
				if _, _, err := c.hashFiles(ctx, files); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkHashStep isolates cache-key serialization: no sources (so neither the
// glob walk nor file hashing run), but many charms/env/deps/tools so the per-field
// serialization dominates. This is the gate for the fmt.Fprintf → direct-write
// optimization on the key path.
func BenchmarkHashStep(b *testing.B) {
	c, err := Open(
		b.TempDir(),
		WithMutable(true),
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	if err != nil {
		b.Fatal(err)
	}
	deps := make([]string, 200)
	for i := range deps {
		deps[i] = fmt.Sprintf("project/pkg-%03d:deadbeefcafef00d%03d", i, i)
	}
	env := make([]string, 40)
	for i := range env {
		env[i] = fmt.Sprintf("MAGUS_VAR_%02d", i)
	}
	tools := make([]string, 20)
	for i := range tools {
		tools[i] = fmt.Sprintf("tool-%02d:1.%d.0", i, i)
	}
	step := &Step{
		ProjectPath:     "pkg/example/service",
		Target:          "build",
		Charms:          []string{"race", "verbose", "coverage", "write"},
		EnvAllow:        env,
		Deps:            deps,
		ToolVersions:    tools,
		SpellDefVersion: "abc123def456",
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := c.hashStep(ctx, step); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExpandSourcesJSWorkspace measures expandSources on a synthetic
// 50-project JS workspace (each project has ~100 source files). This
// baseline captures the per-glob-walk cost before the single-walk
// optimisation (Opt 2/3) is applied.
func BenchmarkExpandSourcesJSWorkspace(b *testing.B) {
	root := buildSyntheticJSWorkspace(b, 50, 100)
	// Scoped globs matching the javascript spell's declared Sources
	// after joinGlob scoping (workspaceRoot-relative).
	globs := make([]string, 0, 50*8)
	for i := 0; i < 50; i++ {
		proj := fmt.Sprintf("pkg-%03d", i)
		globs = append(
			globs,
			proj+"/**/*.js",
			proj+"/**/*.mjs",
			proj+"/**/*.cjs",
			proj+"/**/*.jsx",
			proj+"/package.json",
			proj+"/.npmrc",
			proj+"/pnpm-lock.yaml",
			proj+"/package-lock.json",
		)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = expandSources(globs, root, nil)
	}
	b.Logf("target: <15 ms/op (measured: ~15 ms after single-walk+compiled-glob opt)")
}

// BenchmarkCompiledGlobMatchHot measures the hot-path cost of matching a
// fixed set of paths against pre-compiled glob patterns. On the fast paths
// (extension globs → HasSuffix, exact paths → string compare) the match is
// zero-alloc. The hard alloc gate is enforced by TestCompiledGlobAllocsBudget.
func BenchmarkCompiledGlobMatchHot(b *testing.B) {
	pats := compileGlobs([]string{
		"web/studio/**/*.ts",
		"web/studio/**/*.tsx",
		"web/studio/**/*.js",
		"web/studio/package.json",
		"web/studio/pnpm-lock.yaml",
	})
	paths := []string{
		"web/studio/src/components/Button.tsx",
		"web/studio/src/lib/utils.ts",
		"web/studio/package.json",
		"web/studio/pnpm-lock.yaml",
		"web/api/server.go", // no match expected
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, path := range paths {
			for _, p := range pats {
				_ = p.Match(path)
			}
		}
	}
	b.Logf("target: <200 ns/op (informational)")
}
