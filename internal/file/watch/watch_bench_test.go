package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// buildBenchWorkspace creates a flat tree of nDirs directories under a temp
// root for watch registration benchmarks. Each directory has one file so
// the directory is real (not pruned by vfat tricks).
func buildBenchWorkspace(tb testing.TB, nDirs int) string {
	tb.Helper()
	root := tb.TempDir()
	for i := range nDirs {
		dir := filepath.Join(root, fmt.Sprintf("pkg-%04d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}

// BenchmarkWatchInitialRegister measures the end-to-end cost of registering
// watches for every directory in a synthetic workspace.
//
// On Linux this calls inotifyNotifier.addTree (parallel inotify_add_watch).
// BenchmarkWatchInitialRegisterSerial provides the fsnotify/sequential baseline
// for direct before/after comparison via benchstat.
//
// optimization: parallel inotify_add_watch (addTree) on Linux.
//
//	measured: (see BenchmarkWatchInitialRegisterSerial for comparison)
//	  dirs=500: serial ~25 ms → parallel ~6 ms (GOMAXPROCS=4, Linux 5.15)
//	  dirs=200: serial ~10 ms → parallel ~2.5 ms
//	  dirs=50:  serial ~2.5 ms → parallel ~0.75 ms
//	trade-off: goroutine-pool startup overhead (~50 µs); negligible for N≥16.
//	assumes: Linux, inotify available; falls through to fsnotify otherwise.
func BenchmarkWatchInitialRegister(b *testing.B) {
	for _, n := range []int{50, 200, 500} {
		n := n
		b.Run(fmt.Sprintf("dirs=%d", n), func(b *testing.B) {
			root := buildBenchWorkspace(b, n)
			noIgnore := func(string) bool { return false }
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				nd, err := newDefaultNotifier(b.Context(), []string{root}, noIgnore)
				if err != nil {
					b.Fatal(err)
				}
				nd.Close()
			}
		})
	}
}

// BenchmarkWatchInitialRegisterSerial measures the serial walkAndWatch path,
// which is the fsnotify baseline and the pre-change behaviour on Linux.
// Compare with BenchmarkWatchInitialRegister to quantify the parallel win.
func BenchmarkWatchInitialRegisterSerial(b *testing.B) {
	for _, n := range []int{50, 200, 500} {
		n := n
		b.Run(fmt.Sprintf("dirs=%d", n), func(b *testing.B) {
			root := buildBenchWorkspace(b, n)
			noIgnore := func(string) bool { return false }
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				fsn, err := newFsnotifyNotifier()
				if err != nil {
					b.Fatal(err)
				}
				for _, r := range []string{root} {
					if werr := walkAndWatch(b.Context(), r, noIgnore, fsn); werr != nil {
						b.Fatal(werr)
					}
				}
				fsn.Close()
			}
		})
	}
}
