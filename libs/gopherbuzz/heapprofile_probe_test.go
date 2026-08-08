//go:build !buzz_safe && !buzz_unsafe

package buzz_test

import (
	"context"
	"fmt"
	"sort"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// Answers "what accumulates" for the shape that actually hurts: many independent
// programs in one process, which is what this package's own suite does and why it
// peaks at 13.5GB. Diagnostic, not an assertion. Run with -v.
func TestHeapProfileAcrossManyPrograms(t *testing.T) {
	before := vm.ReadHeapStats().Objects
	for i := range 200 {
		s := buzz.NewSession(context.Background())
		src := fmt.Sprintf(`
fun work() > str {
    var parts = mut [<str>];
    foreach (j in 0..50) { parts.append("row %d " + "{j}"); }
    final m = {"a": parts.len(), "b": %d};
    return parts.join(",");
}
work();
`, i, i)
		if _, err := s.Eval(context.Background(), src); err != nil {
			t.Fatalf("program %d: %v", i, err)
		}
	}
	after := vm.ReadHeapStats().Objects

	p := vm.HeapProfile()
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
	t.Logf("200 programs added %d heap slots (%d -> %d)", after-before, before, after)
	t.Logf("heap slots by kind (%d total):", total)
	for _, r := range rows {
		t.Logf("  %-12s %8d  %5.1f%%", r.kind, r.n, 100*float64(r.n)/float64(total))
	}
}
