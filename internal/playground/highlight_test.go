package playground

import (
	"strings"
	"testing"
)

// TestHighlight_lossless is the load-bearing property: the spans must reproduce
// the input byte-for-byte, or the overlay drifts out of alignment with the
// textarea.
func TestHighlight_lossless(t *testing.T) {
	srcs := []string{
		"",
		"return 1 + 2;",
		`import "magus"; // a comment` + "\nfun f(n: int) > int { return n; }\n",
		"const x = 3.14; // pi\nforeach (i in 0..5) { x = x; }",
		"/* block\n comment */ const s = \"he\\\"llo {name}\";",
	}
	for _, src := range srcs {
		var b strings.Builder
		for _, sp := range Highlight(src) {
			b.WriteString(sp.Text)
		}
		if b.String() != src {
			t.Errorf("not lossless:\n in:  %q\n out: %q", src, b.String())
		}
	}
}

func TestHighlight_classes(t *testing.T) {
	spans := Highlight(`const n = 42; // note` + "\n" + `var s = "hi";`)
	got := map[string]string{} // class -> first text seen
	for _, sp := range spans {
		if _, ok := got[sp.Class]; !ok {
			got[sp.Class] = sp.Text
		}
	}
	if got["kw"] != "const" {
		t.Errorf("keyword: got %q", got["kw"])
	}
	if got["num"] != "42" {
		t.Errorf("number: got %q", got["num"])
	}
	if !strings.HasPrefix(got["com"], "// note") {
		t.Errorf("comment: got %q", got["com"])
	}
	if got["str"] != `"hi"` {
		t.Errorf("string: got %q", got["str"])
	}
}

func TestHighlight_rangeNotFloat(t *testing.T) {
	// 0..5 must scan as 0, .., 5 — not 0. and a stray .5.
	var nums []string
	for _, sp := range Highlight("0..5") {
		if sp.Class == "num" {
			nums = append(nums, sp.Text)
		}
	}
	if len(nums) != 2 || nums[0] != "0" || nums[1] != "5" {
		t.Errorf("range scan: got nums %v, want [0 5]", nums)
	}
}
