package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuzzFileMapSpans pins each declaration's span to its own doc comment and last line:
// an unnamed statement between two declarations still ends the first, and trailing blank
// lines belong to nobody.
func TestBuzzFileMapSpans(t *testing.T) {
	src := "import \"std\";\n" + // 1
		"\n" + // 2
		"// Point is a pair.\n" + // 3
		"object Point {\n" + // 4
		"    x: int,\n" + // 5
		"}\n" + // 6
		"\n" + // 7
		"enum Kind { A, B }\n" + // 8
		"\n" + // 9
		"// area multiplies.\n" + // 10
		"// Twice.\n" + // 11
		"export fun area(p: Point) > int {\n" + // 12
		"    return p.x * p.x;\n" + // 13
		"}\n" + // 14
		"\n" + // 15
		"final origin = Point{ x = 0 };\n" + // 16
		"\n" + // 17
		"test \"area squares\" {\n" + // 18
		"    std\\assert(area(origin) == 0, message: \"zero\");\n" + // 19
		"}\n" + // 20
		"\n" // 21
	root := writeTree(t, map[string]string{"x.buzz": src})

	m, ok := buzzFileMap(root+"/x.buzz", "x.buzz")

	require.True(t, ok)
	assert.Equal(t, fileMap{rel: "x.buzz", lines: 21, noun: "declaration", entries: []mapEntry{
		{name: "object Point", first: 3, last: 6},
		{name: "enum Kind", first: 8, last: 8},
		{name: "fun area", first: 10, last: 14},
		{name: `test "area squares"`, first: 18, last: 20},
	}}, m)
}

func TestBuzzFileMapNeedsAParse(t *testing.T) {
	root := writeTree(t, map[string]string{"bad.buzz": "fun (\n", "plain.buzz": "import \"std\";\nfinal x = 1;\n"})

	_, ok := buzzFileMap(root+"/bad.buzz", "bad.buzz")
	assert.False(t, ok, "a file that does not parse has no map")
	_, ok = buzzFileMap(root+"/plain.buzz", "plain.buzz")
	assert.False(t, ok, "a file with no declaration has no map")
}
