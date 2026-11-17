package impact

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Only the markers a change ADDS are left behind by it: a marker already in the file is
// context, a removed one is the opposite, and one in generated output was written by its
// generator. A bare mention of the marker outside a comment is not one.
func TestAddedMarkers(t *testing.T) {
	patch := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,4 +1,6 @@\n package a\n" +
		" // compat(until: already here): context.\n" +
		"-// compat(until: gone): removed.\n" +
		"+// compat(until: no store holds v1 descriptors): keep the branch.\n" +
		"+const marker = \"compat(until: x)\"\n" +
		"+// Deprecated: use G.\n" +
		" func F() {}\n" +
		"diff --git a/gen/c.go b/gen/c.go\n--- a/gen/c.go\n+++ b/gen/c.go\n@@ -1 +1,2 @@\n package gen\n" +
		"+// compat(until: whatever): generated.\n"

	got := addedMarkers(patch, map[string]bool{"gen/c.go": true})
	assert.Equal(t, []addedMarker{
		{Path: "a.go", Line: 3, Marker: "compat(until:", Text: "no store holds v1 descriptors"},
		{Path: "a.go", Line: 5, Marker: "Deprecated:", Text: "use G."},
	}, got)
}

func TestImpactLeftBehindLines(t *testing.T) {
	t.Run("each empty form says what it means", func(t *testing.T) {
		assert.Equal(t, []string{
			"LEFT BEHIND: no compat(until:) or Deprecated: marker added",
			"      no symbol it stopped using is still defined",
		}, leftBehindLines(&leftBehind{UnreferencedKnown: true}))
		assert.Equal(t, []string{
			"LEFT BEHIND: no compat(until:) or Deprecated: marker added",
			"      symbols left unused: not measured, read only for the working tree",
		}, leftBehindLines(&leftBehind{UnreferencedReason: "read only for the working tree"}))
	})

	t.Run("the lists are capped", func(t *testing.T) {
		l := &leftBehind{UnreferencedKnown: true}
		for i := range listCap + 3 {
			l.Markers = append(l.Markers, addedMarker{Path: "a.go", Line: i, Marker: "Deprecated:", Text: "x"})
			l.Unreferenced = append(l.Unreferenced, fmt.Sprintf("S%d (a.go:%d)", i, i))
		}
		lines := leftBehindLines(l)
		assert.Equal(t, "      and 3 more", lines[listCap+1])
		assert.Equal(t, fmt.Sprintf("      %d symbols lost the last use here and are still defined", listCap+3), lines[listCap+2])
		assert.Equal(t, "      and 3 more", lines[len(lines)-1])
	})
}
