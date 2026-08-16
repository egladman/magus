package diff

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// Intra-line emphasis is computed independently in TypeScript, and the console renders from
// that copy. If the two ever disagree, the same changed line is highlighted differently in the
// browser than in the terminal - which reads as a bug in whichever surface the reader trusts
// less, with nothing in either output saying which one is wrong.
//
// PROVENANCE: every expectation below was produced by RUNNING the console's words.ts under
// node v24 (which strips the TypeScript natively, so the shipped source ran unmodified) over
// exactly these inputs, then converting each UTF-16 offset to a UTF-8 byte offset with
// Buffer.byteLength(s.slice(0, i)). They are pinned as literals so a change to either
// implementation has to confront the other. The trailing comment on each row is the text the
// span selects, so the numbers stay auditable by eye.
func TestEmphasizeMatchesTheConsoleImplementation(t *testing.T) {
	cases := []struct {
		name          string
		before, after string
		wantBefore    Span
		wantAfter     Span
	}{
		{"identical lines", "same", "same", Span{}, Span{}},                                                     // nil -> nil
		{"empty before", "", "added", Span{}, Span{}},                                                           // nil -> nil
		{"empty after", "removed", "", Span{}, Span{}},                                                          // nil -> nil
		{"wholly different", "alpha", "9182", Span{}, Span{}},                                                   // nil -> nil
		{"renamed identifier", "const userName = read()", "const userLabel = read()", Span{6, 14}, Span{6, 15}}, // "userName" -> "userLabel"
		{"one operator", "total += x", "total -= x", Span{6, 7}, Span{6, 7}},                                    // "+" -> "-"
		{"insertion", "call(a, b)", "call(a, extra, b)", Span{}, Span{8, 15}},                                   // nil -> "extra, "
		{"two edit regions", "f(a, b, c)", "f(x, b, z)", Span{2, 9}, Span{2, 9}},                                // "a, b, c" -> "x, b, z"
		{"indent widened", "  x = 1", "    x = 1", Span{}, Span{2, 4}},                                          // nil -> "  "
		{"space collapsed", "a  b", "a b", Span{2, 3}, Span{}},                                                  // " " -> nil
		{"punctuation boundary", "f(a, b);", "f(a; b);", Span{3, 4}, Span{3, 4}},                                // "," -> ";"
		{"token boundary disagrees", "ab cd", "ab_cd", Span{0, 3}, Span{0, 5}},                                  // "ab " -> "ab_cd"
		// The multi-byte inputs are escaped rather than written literally so the byte offsets
		// above stay countable by eye, and so no editor can renormalise the vectors.
		{"accent swapped", "caf\u00e9", "caf\u00e8", Span{3, 5}, Span{3, 5}},                                    // "é" -> "è"
		{"multibyte prefix", "\u65e5\u672c\u8a9e alpha", "\u65e5\u672c\u8a9e beta", Span{10, 15}, Span{10, 14}}, // "alpha" -> "beta"
		{"multibyte both ends", "\u00e9 alpha \u00e9", "\u00e9 beta \u00e9", Span{3, 8}, Span{3, 7}},            // "alpha" -> "beta"
		{"accent splits a token", "let s = \"na\u00efve\"", "let s = \"naive\"", Span{0, 13}, Span{0, 14}},      // "let s = \"naï" -> "let s = \"naive"
		{"trailing digit", "value = 1", "value = 2", Span{8, 9}, Span{8, 9}},                                    // "1" -> "2"
		{"prepended token", "bc", "abc", Span{}, Span{0, 3}},                                                    // nil -> "abc"
		{"appended token", "abc", "abcd", Span{}, Span{}},                                                       // nil -> nil
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotBefore, gotAfter := Emphasize(tc.before, tc.after)
			assert.Equal(t, tc.wantBefore, gotBefore, "before span over %q", tc.before)
			assert.Equal(t, tc.wantAfter, gotAfter, "after span over %q", tc.after)

			// An offset that is not on a rune boundary slices the line into invalid UTF-8, which
			// is the failure a byte-wise port of the UTF-16 scan produces and nothing else would
			// catch: the numbers would still look plausible.
			if !gotBefore.Empty() {
				assert.True(t, utf8.ValidString(tc.before[gotBefore.Start:gotBefore.End]),
					"before span must cut whole runes")
			}
			if !gotAfter.Empty() {
				assert.True(t, utf8.ValidString(tc.after[gotAfter.Start:gotAfter.End]),
					"after span must cut whole runes")
			}
		})
	}
}

func TestPairForEmphasisIsPositionalAndOnlyWithinAnEqualRun(t *testing.T) {
	// The inputs are line positions in a hunk body: two removed lines followed by the two that
	// replaced them, which is the shape the run scanner hands over.
	assert.Equal(t, []EmphasisPair{{Del: 0, Add: 2}, {Del: 1, Add: 3}},
		PairForEmphasis([]int{0, 1}, []int{2, 3}))
	// An unequal run means lines were added or removed rather than rewritten. Pairing across
	// that would invent a correspondence the patch does not contain.
	assert.Nil(t, PairForEmphasis([]int{0}, []int{1, 2}))
	assert.Nil(t, PairForEmphasis([]int{}, []int{}))
}
