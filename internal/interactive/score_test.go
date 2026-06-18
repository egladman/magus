package interactive

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLeafScore_NoMatch(t *testing.T) {
	assert.Zero(t, LeafScore("foo/bar.go", "xyz"), "LeafScore should return 0 when query not in path")
}

func TestLeafScore_EmptyQuery(t *testing.T) {
	assert.Zero(t, LeafScore("foo/bar.go", ""), "LeafScore should return 0 for empty query")
}

func TestLeafScore_LeafMatch(t *testing.T) {
	leafMatch := LeafScore("pkg/widget.go", "widget")
	assert.Positive(t, leafMatch, "LeafScore: leaf match should be positive")
	// Query found only in parent dir (not leaf) still scores non-zero.
	dirMatch := LeafScore("widget/main.go", "widget")
	assert.NotZero(t, dirMatch, "LeafScore: path match (dir only) should be non-zero")
}

func TestLeafScore_CaseInsensitive(t *testing.T) {
	a := LeafScore("Foo/Bar.go", "bar")
	b := LeafScore("Foo/Bar.go", "BAR")
	assert.NotZero(t, a, "LeafScore should be case-insensitive")
	assert.NotZero(t, b, "LeafScore should be case-insensitive")
}

func TestLeafScore_ShallowerPathScoresHigher(t *testing.T) {
	shallow := LeafScore("foo.go", "foo")
	deep := LeafScore("a/b/c/d/foo.go", "foo")
	assert.Greater(t, shallow, deep, "shallower path should score higher")
}
