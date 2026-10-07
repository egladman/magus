package scipbuzz

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInterpStartsFindExactSources places each interpolation where its source
// begins, past escapes, multibyte text and earlier interpolations on the same
// line: the cases where the lexer's own column is approximate.
func TestInterpStartsFindExactSources(t *testing.T) {
	cases := []struct {
		text  string
		exprs []string
	}{
		{`"{a} and {b}"`, []string{"a", "b"}},
		{`"ünï {name} ✓ {label}"`, []string{"name", "label"}},
		{`"\{x} \"{b}\" \t{a}"`, []string{"b", "a"}},
		{`"\065{a}"`, []string{"a"}},
		{`"{"{a}{b}"}{c}"`, []string{`"{a}{b}"`, "c"}},
		{"`{a}-{b}\n  {c}`", []string{"a", "b", "c"}},
		{"`\\{x} {y}`", []string{"y"}},
	}
	for _, c := range cases {
		text := "x = " + c.text + ";"
		starts := interpStarts(text, 4)
		require.Len(t, starts, len(c.exprs), c.text)
		for i, start := range starts {
			require.True(t, strings.HasPrefix(text[start:], c.exprs[i]+"}"), "%s: part %d starts at %q", c.text, i, text[start:])
		}
	}
}

func TestIdentRange(t *testing.T) {
	src := newSource("final @\"type\" = é + x;\n")
	s, err := fileStream(src)
	require.NoError(t, err)

	r, ok := s.identRange(1)
	require.True(t, ok)
	require.Equal(t, "type", src.text[8:12])
	require.Equal(t, int32(8), r.Start.Character, "a free identifier is ranged inside its quotes")
	require.Equal(t, int32(12), r.End.Character)

	_, ok = s.identRange(0)
	require.False(t, ok, "a keyword is not an identifier")
}

func TestSkipTypeRecordsTypeNames(t *testing.T) {
	src := newSource("x: mut [ns\\Rect]? = {str: Point}; y: fun (code: str, Shape) > Level !> Err = 1;")
	s, err := fileStream(src)
	require.NoError(t, err)

	names := func(start int) []string {
		var refs []typeRef
		_, ok := s.skipType(start, &refs)
		require.True(t, ok)
		var out []string
		for _, r := range refs {
			n := s.toks[r.name].Val
			if r.ns >= 0 {
				n = s.toks[r.ns].Val + `\` + n
			}
			out = append(out, n)
		}
		return out
	}
	require.Equal(t, []string{`ns\Rect`}, names(2))
	y := 0
	for i, tk := range s.toks {
		if tk.Val == "y" {
			y = i
		}
	}
	require.Equal(t, []string{"str", "Shape", "Level", "Err"}, names(y+2))
}
