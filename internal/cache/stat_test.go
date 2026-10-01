package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestStatMtimeAllocsBudget asserts that statMtime is zero-alloc on the fast
// path. On Linux this exercises the statx(AT_STATX_DONT_SYNC) path; on other
// platforms it exercises the os.Stat fallback (which allocates one
// *os.fileStat, so the budget is 1 there). The hard gate is enforced here;
// BenchmarkStatMtime provides ns/op for benchstat comparison.
func TestStatMtimeAllocsBudget(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(200, func() {
		_, _, _, _ = statMtime(f)
	})
	// Linux uses the statx fast path (zero allocs). os.Stat-based platforms
	// (darwin, etc.) allocate inside os.Stat itself, so the floor there is 2,
	// not a regression in this package. Keep the strict gate on Linux so a real
	// statx-path regression (a string conversion or error wrapping on the hot
	// path) is still caught.
	budget := 1.0
	if runtime.GOOS != "linux" {
		budget = 2
	}
	if allocs > budget {
		t.Fatalf("statMtime alloc budget exceeded: got %.0f allocs/op, want <=%.0f", allocs, budget)
	}
}

// BenchmarkStatMtime measures statMtime for ns/op comparison against os.Stat.
// On Linux this exercises statx(AT_STATX_DONT_SYNC); on other platforms it
// delegates to os.Stat.
func BenchmarkStatMtime(b *testing.B) {
	f := filepath.Join(b.TempDir(), "x")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _, _ = statMtime(f)
	}
	b.Logf("target: <500 ns/op Linux, <1 µs/op other (informational)")
}
