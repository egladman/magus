package playground

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHighlight_lossless is the load-bearing property: the spans must reproduce
// the input byte-for-byte, or the overlay drifts out of alignment with the
// textarea.
func TestHighlight_lossless(t *testing.T) {
	srcs := []string{
		"",
		"return 1 + 2;",
		`import "magus"; // a comment` + "\nfun f(n: int) > int { return n; }\n",
		"final x = 3.14; // pi\nforeach (i in 0..5) { x = x; }",
		"/* block\n comment */ final s = \"he\\\"llo {name}\";",
	}
	for _, src := range srcs {
		var b strings.Builder
		for _, sp := range Highlight(src) {
			b.WriteString(sp.Text)
		}
		assert.Equal(t, src, b.String(), "not lossless")
	}
}

func TestHighlight_classes(t *testing.T) {
	spans := Highlight(`final n = 42; // note` + "\n" + `var s = "hi";`)
	got := map[string]string{} // class -> first text seen
	for _, sp := range spans {
		if _, ok := got[sp.Class]; !ok {
			got[sp.Class] = sp.Text
		}
	}
	assert.Equal(t, "final", got["kw"], "keyword")
	assert.Equal(t, "42", got["num"], "number")
	assert.True(t, strings.HasPrefix(got["com"], "// note"), "comment: got %q", got["com"])
	assert.Equal(t, `"hi"`, got["str"], "string")
}

func TestHighlight_rangeNotFloat(t *testing.T) {
	// 0..5 must scan as 0, .., 5 — not 0. and a stray .5.
	var nums []string
	for _, sp := range Highlight("0..5") {
		if sp.Class == "num" {
			nums = append(nums, sp.Text)
		}
	}
	assert.Equal(t, []string{"0", "5"}, nums, "range scan")
}
