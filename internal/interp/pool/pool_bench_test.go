package pool_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/pool"
)

// nTargetMagusfile returns a magusfile body with n numbered targets
// (target-1..target-N) plus the fixed "noop" target.
func nTargetMagusfile(n int) string {
	src := "global function noop(args: {string}) end\n"
	for i := 1; i <= n; i++ {
		src += fmt.Sprintf("global function target_%d(args: {string}) end\n", i)
	}
	return src
}

func writeMagusfileB(b testing.TB, dir, body string) {
	b.Helper()
	if err := os.WriteFile(filepath.Join(dir, "magusfile.tl"), []byte(body), 0o644); err != nil {
		b.Fatal(err)
	}
}

// writeMagusfilesB creates dir/magusfiles/ and splits n numbered targets across
// multiple .tl files (100 targets per file) to avoid the Teal registry overflow
// that occurs when a single file declares more than ~500 targets.
// A "noop" target is included in the first file to match the single-file form.
func writeMagusfilesB(b testing.TB, dir string, n int) {
	b.Helper()
	mfDir := filepath.Join(dir, "magusfiles")
	if err := os.MkdirAll(mfDir, 0o755); err != nil {
		b.Fatal(err)
	}
	const perFile = 100
	for fileIdx, start := 0, 0; start < n; fileIdx, start = fileIdx+1, start+perFile {
		end := start + perFile
		if end > n {
			end = n
		}
		var sb strings.Builder
		if fileIdx == 0 {
			sb.WriteString("global function noop(args: {string}) end\n")
		}
		for i := start; i < end; i++ {
			fmt.Fprintf(&sb, "global function target_%d(args: {string}) end\n", i+1)
		}
		name := filepath.Join(mfDir, fmt.Sprintf("%04d.tl", fileIdx))
		if err := os.WriteFile(name, []byte(sb.String()), 0o644); err != nil {
			b.Fatal(err)
		}
	}
}

func findSourceB(b *testing.B, dir string) *interp.Source {
	b.Helper()
	src, err := interp.Find(dir)
	if err != nil {
		b.Fatalf("Find: %v", err)
	}
	if src == nil {
		b.Fatal("Find: no source")
	}
	return src
}

// BenchmarkPoolColdStart measures the time from pool.New to the first Submit
// result completing (worker init cost: bindings + tl.lua + magusfile parse).
func BenchmarkPoolColdStart(b *testing.B) {
	dir := b.TempDir()
	writeMagusfileB(b, dir, nTargetMagusfile(1))
	src := findSourceB(b, dir)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := pool.New(src, 1)
		res := <-p.Submit(context.Background(), "noop", nil)
		if res.Err != nil {
			b.Fatal(res.Err)
		}
		_ = p.Close()
	}
}

// BenchmarkDispatchOverhead measures a single Submit on a warm pool.
// This is the hot-path cost: should land sub-100µs.
func BenchmarkDispatchOverhead(b *testing.B) {
	dir := b.TempDir()
	writeMagusfileB(b, dir, nTargetMagusfile(1))
	src := findSourceB(b, dir)

	p := pool.New(src, 1)
	defer p.Close()
	if res := <-p.Submit(context.Background(), "noop", nil); res.Err != nil {
		b.Fatal(res.Err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if res := <-p.Submit(context.Background(), "noop", nil); res.Err != nil {
			b.Fatal(res.Err)
		}
	}
}

// BenchmarkDispatchFanOut measures N sibling targets dispatched concurrently
// with NumCPU workers. Ratio to sequential baseline should be ~NumCPU.
// N=1000 uses the magusfiles/ split-file form to avoid the Teal registry
// overflow that occurs when a single file declares more than ~500 targets.
func BenchmarkDispatchFanOut(b *testing.B) {
	for _, tc := range []struct {
		n         int
		multiFile bool
	}{
		{10, false},
		{100, false},
		{1000, true},
	} {
		tc := tc
		n := tc.n
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			if tc.multiFile {
				writeMagusfilesB(b, dir, n)
			} else {
				writeMagusfileB(b, dir, nTargetMagusfile(n))
			}
			src := findSourceB(b, dir)

			p := pool.New(src, runtime.NumCPU())
			defer p.Close()
			if res := <-p.Submit(context.Background(), "noop", nil); res.Err != nil {
				b.Fatal(res.Err)
			}

			names := make([]string, n)
			for i := range names {
				names[i] = fmt.Sprintf("target-%d", i+1)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				chs := make([]<-chan pool.Result, n)
				for j, name := range names {
					chs[j] = p.Submit(context.Background(), name, nil)
				}
				for _, ch := range chs {
					if res := <-ch; res.Err != nil {
						b.Fatal(res.Err)
					}
				}
			}
		})
	}
}

// BenchmarkPoolSaturation holds N=100 targets fixed while varying slot count.
// Wall-time should approach (N × per_target_work) / Slots.
func BenchmarkPoolSaturation(b *testing.B) {
	const n = 100
	dir := b.TempDir()
	writeMagusfileB(b, dir, nTargetMagusfile(n))
	src := findSourceB(b, dir)

	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("target-%d", i+1)
	}

	for _, slots := range []int{2, 4, 8, 16} {
		slots := slots
		b.Run(fmt.Sprintf("Slots=%d", slots), func(b *testing.B) {
			p := pool.New(src, slots)
			defer p.Close()
			if res := <-p.Submit(context.Background(), "noop", nil); res.Err != nil {
				b.Fatal(res.Err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				chs := make([]<-chan pool.Result, n)
				for j, name := range names {
					chs[j] = p.Submit(context.Background(), name, nil)
				}
				for _, ch := range chs {
					if res := <-ch; res.Err != nil {
						b.Fatal(res.Err)
					}
				}
			}
		})
	}
}

// BenchmarkPoolColdStartFanOut measures cold-start cost when N targets are
// dispatched into a pool of capacity NumCPU. Useful for tracking whether
// eager vs lazy worker spawn affects small-fanout latency (a workload that
// only needs N workers should not pay NumCPU worker init).
func BenchmarkPoolColdStartFanOut(b *testing.B) {
	for _, n := range []int{1, 2, 4} {
		n := n
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			writeMagusfileB(b, dir, nTargetMagusfile(n))
			src := findSourceB(b, dir)

			names := make([]string, n)
			for i := range names {
				names[i] = fmt.Sprintf("target-%d", i+1)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := pool.New(src, runtime.NumCPU())
				chs := make([]<-chan pool.Result, n)
				for j, name := range names {
					chs[j] = p.Submit(context.Background(), name, nil)
				}
				for _, ch := range chs {
					if res := <-ch; res.Err != nil {
						b.Fatal(res.Err)
					}
				}
				_ = p.Close()
			}
		})
	}
}

// BenchmarkDispatchVsSubprocess compares in-process dispatch against subprocess
// invocation. In-process should be 100-1000× faster for trivial targets.
func BenchmarkDispatchVsSubprocess(b *testing.B) {
	for _, n := range []int{10, 100} {
		n := n

		b.Run(fmt.Sprintf("N=%d/inprocess", n), func(b *testing.B) {
			dir := b.TempDir()
			writeMagusfileB(b, dir, nTargetMagusfile(n))
			src := findSourceB(b, dir)

			p := pool.New(src, runtime.NumCPU())
			defer p.Close()
			if res := <-p.Submit(context.Background(), "noop", nil); res.Err != nil {
				b.Fatal(res.Err)
			}

			names := make([]string, n)
			for i := range names {
				names[i] = fmt.Sprintf("target-%d", i+1)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				chs := make([]<-chan pool.Result, n)
				for j, name := range names {
					chs[j] = p.Submit(context.Background(), name, nil)
				}
				for _, ch := range chs {
					if res := <-ch; res.Err != nil {
						b.Fatal(res.Err)
					}
				}
			}
		})

		b.Run(fmt.Sprintf("N=%d/subprocess", n), func(b *testing.B) {
			if _, err := exec.LookPath("magus"); err != nil {
				b.Skip("magus binary not in PATH; skipping subprocess baseline")
			}
			b.Skip("subprocess baseline requires a real workspace; skipping in unit context")
		})
	}
}
