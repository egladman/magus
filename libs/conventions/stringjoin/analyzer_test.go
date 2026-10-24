package stringjoin

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestAnalyzer(t *testing.T) {
	analyzer, err := New(Options{MinOperands: 3, Hint: "use fmt.Sprintf"})
	if err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), analyzer, "flagged", "clean")
}

func TestNewRequiresMinOperands(t *testing.T) {
	for _, n := range []int{0, 1} {
		if _, err := New(Options{MinOperands: n}); err == nil {
			t.Errorf("New(MinOperands: %d) succeeded, want an error", n)
		}
	}
}
