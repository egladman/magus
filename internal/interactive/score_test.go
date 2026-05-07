package interactive_test

import (
	"testing"

	"github.com/egladman/magus/internal/interactive"
)

func TestLeafScore_NoMatch(t *testing.T) {
	if interactive.LeafScore("foo/bar.go", "xyz") != 0 {
		t.Error("LeafScore should return 0 when query not in path")
	}
}

func TestLeafScore_EmptyQuery(t *testing.T) {
	if interactive.LeafScore("foo/bar.go", "") != 0 {
		t.Error("LeafScore should return 0 for empty query")
	}
}

func TestLeafScore_LeafMatch(t *testing.T) {
	leafMatch := interactive.LeafScore("pkg/widget.go", "widget")
	if leafMatch <= 0 {
		t.Errorf("LeafScore: leaf match should be positive, got %d", leafMatch)
	}
	// Query found only in parent dir (not leaf) still scores non-zero.
	dirMatch := interactive.LeafScore("widget/main.go", "widget")
	if dirMatch == 0 {
		t.Error("LeafScore: path match (dir only) should be non-zero")
	}
}

func TestLeafScore_CaseInsensitive(t *testing.T) {
	a := interactive.LeafScore("Foo/Bar.go", "bar")
	b := interactive.LeafScore("Foo/Bar.go", "BAR")
	if a == 0 || b == 0 {
		t.Error("LeafScore should be case-insensitive")
	}
}

func TestLeafScore_ShallowerPathScoresHigher(t *testing.T) {
	shallow := interactive.LeafScore("foo.go", "foo")
	deep := interactive.LeafScore("a/b/c/d/foo.go", "foo")
	if shallow <= deep {
		t.Errorf("shallower path should score higher: %d <= %d", shallow, deep)
	}
}
