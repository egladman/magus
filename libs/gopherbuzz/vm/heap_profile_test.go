//go:build !buzz_safe && !buzz_unsafe

package vm

import (
	"sort"
	"testing"
)

// Prints what the heap is actually made of after this package's own tests have
// run. Not an assertion: the point is to answer "what accumulates" before anyone
// designs a collector around a guess. Run with -v.
func TestHeapProfileReportsWhatAccumulates(t *testing.T) {
	p := HeapProfile()
	if len(p) == 0 {
		t.Skip("nothing on the heap yet")
	}
	type row struct {
		kind string
		n    int
	}
	rows := make([]row, 0, len(p))
	total := 0
	for k, n := range p {
		rows = append(rows, row{k, n})
		total += n
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].n > rows[j].n })
	t.Logf("heap slots by kind (%d total):", total)
	for _, r := range rows {
		t.Logf("  %-12s %8d  %5.1f%%", r.kind, r.n, 100*float64(r.n)/float64(total))
	}
}
