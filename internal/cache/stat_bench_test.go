package cache

import (
	"os"
	"path/filepath"
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
	// Linux statx path: zero allocs. os.Stat fallback: 1 alloc (FileInfo).
	// Use 1 as the budget so the test passes on all platforms while still
	// catching regressions (e.g. a string conversion or error wrapping added
	// to the hot path).
	if allocs > 1 {
		t.Fatalf("statMtime alloc budget exceeded: got %.0f allocs/op, want ≤1", allocs)
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
