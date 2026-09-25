package merge3

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name               string
		base, ours, theirs string
		want               string
		kinds              []Kind
		unresolved         bool
	}{
		{name: "both added at one place keeps ours then theirs",
			base: "a\nz\n", ours: "a\np\nz\n", theirs: "a\nq\nz\n",
			want: "a\np\nq\nz\n", kinds: []Kind{BothAdded}},
		{name: "both added at the end of the file",
			base: "a\n", ours: "a\np\n", theirs: "a\nq\n",
			want: "a\np\nq\n", kinds: []Kind{BothAdded}},
		{name: "both created the file",
			base: "", ours: "p\n", theirs: "q\n",
			want: "p\nq\n", kinds: []Kind{BothAdded}},
		{name: "identical edits on both sides",
			base: "a\nb\nc\n", ours: "a\nB\nc\n", theirs: "a\nB\nc\n",
			want: "a\nB\nc\n", kinds: []Kind{Contained}},
		{name: "theirs added what ours added and more",
			base: "a\nz\n", ours: "a\np\nz\n", theirs: "a\np\nq\nz\n",
			want: "a\np\nq\nz\n", kinds: []Kind{Contained}},
		{name: "ours holds theirs's edit and more",
			base: "a\nx\nz\n", ours: "a\nx2\nextra\nz\n", theirs: "a\nx2\nz\n",
			want: "a\nx2\nextra\nz\n", kinds: []Kind{Contained}},
		{name: "one side each merges with no kind",
			base: "a\nm\nz\n", ours: "A\nm\nz\n", theirs: "a\nm\nZ\n",
			want: "A\nm\nZ\n"},
		{name: "separate regions each settle",
			base: "a\nm\nz\n", ours: "a\np\nm\nz\nr\n", theirs: "a\nq\nm\nz\ns\n",
			want: "a\np\nq\nm\nz\nr\ns\n", kinds: []Kind{BothAdded, BothAdded}},
		{name: "both edited one line differently",
			base: "a\nx\nz\n", ours: "a\nx1\nz\n", theirs: "a\nx2\nz\n", unresolved: true},
		{name: "one deleted what the other edited",
			base: "a\nx\nz\n", ours: "a\nz\n", theirs: "a\ny\nz\n", unresolved: true},
		{name: "a deletion is not contained in an edit carrying it",
			base: "a\nx\nz\n", ours: "a\np\nx\nz\n", theirs: "a\np\nz\n", unresolved: true},
		{name: "edits on adjacent lines",
			base: "a\nx\ny\nz\n", ours: "a\nX\ny\nz\n", theirs: "a\nx\nY\nz\n", unresolved: true},
		{name: "one unresolvable region leaves the file unresolved",
			base: "a\nm\nx\nz\n", ours: "a\np\nm\nx1\nz\n", theirs: "a\nq\nm\nx2\nz\n", unresolved: true},
		{name: "CRLF both added keeps each line's ending",
			base: "a\r\nz\r\n", ours: "a\r\np\r\nz\r\n", theirs: "a\r\nq\r\nz\r\n",
			want: "a\r\np\r\nq\r\nz\r\n", kinds: []Kind{BothAdded}},
		{name: "a line ending change is a change",
			base: "a\nx\nz\n", ours: "a\nx\r\nz\n", theirs: "a\nx2\nz\n", unresolved: true},
		{name: "no trailing newline survives an addition above it",
			base: "a\nz", ours: "p\na\nz", theirs: "q\na\nz",
			want: "p\nq\na\nz", kinds: []Kind{BothAdded}},
		{name: "both appending past a missing final newline edit its last line",
			base: "a\nb", ours: "a\nb\nc", theirs: "a\nb\nd", unresolved: true},
		{name: "binary content", base: "a\x00\n", ours: "a\x00\np\n", theirs: "a\x00\nq\n", unresolved: true},
		{name: "a side past the edit cap",
			base: "a\n", ours: "a\n" + strings.Repeat("p\n", maxEdits+1), theirs: "a\nq\n", unresolved: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Resolve([]byte(tc.base), []byte(tc.ours), []byte(tc.theirs))
			if tc.unresolved {
				assert.False(t, ok)
				assert.Equal(t, Resolution{}, got)
				return
			}
			assert.True(t, ok)
			assert.Equal(t, Resolution{Content: []byte(tc.want), Kinds: tc.kinds}, got)
		})
	}
}

func TestResolveIsSymmetricOutsideBothAdded(t *testing.T) {
	base, small, large := "a\nz\n", "a\np\nz\n", "a\np\nq\nz\n"
	one, ok1 := Resolve([]byte(base), []byte(small), []byte(large))
	two, ok2 := Resolve([]byte(base), []byte(large), []byte(small))
	assert.True(t, ok1)
	assert.True(t, ok2)
	assert.Equal(t, one, two)
}

func TestMatchesFollowAShortestEditScript(t *testing.T) {
	o := splitLines([]byte("a\nb\nc\nd\ne\n"))
	s := splitLines([]byte("x\nb\nc\ny\ne\nf\n"))
	m, ok := matches(o, s)
	assert.True(t, ok)
	assert.Equal(t, []int{-1, 1, 2, -1, 4}, m)
}

func TestLabel(t *testing.T) {
	assert.Equal(t, "one side each", Resolution{}.Label())
	assert.Equal(t, "kind 2", Resolution{Kinds: []Kind{BothAdded, BothAdded}}.Label())
	assert.Equal(t, "kinds 1, 2", Resolution{Kinds: []Kind{BothAdded, Contained}}.Label())
	assert.Equal(t, "both added", BothAdded.String())
	assert.Equal(t, "contained", Contained.String())
	assert.Equal(t, "Kind(7)", Kind(7).String())
}
