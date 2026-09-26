// No longer exempt, so it narrows widget.go like any other test file would.
package strict // want `widget_bench_test.go narrows widget.go; these tests belong in widget_test.go`

import "testing"

func BenchmarkWidget(b *testing.B) {
	for b.Loop() {
		_ = Widget{}
	}
}
