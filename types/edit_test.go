package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEditSiteValidate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		site EditSite
		err  string
	}{
		{"lines", EditSite{Path: "a.go", Anchor: EditAnchorLines, Lines: []int{2, 3}}, ""},
		{"insertion", EditSite{Path: "a.go", Anchor: EditAnchorLines, Lines: []int{4, 3}}, ""},
		{"text", EditSite{Path: "a.go", Anchor: EditAnchorText, Old: "x", All: true}, ""},
		{"no path", EditSite{Anchor: EditAnchorText, Old: "x"}, "no path"},
		{"no anchor", EditSite{Path: "a.go"}, "no anchor; one of lines, text"},
		{"unknown anchor", EditSite{Path: "a.go", Anchor: "symbol"}, `unknown anchor "symbol"; one of lines, text`},
		{"one number", EditSite{Path: "a.go", Anchor: EditAnchorLines, Lines: []int{2}}, "the lines anchor takes [start, end], got 1 numbers"},
		{"zero start", EditSite{Path: "a.go", Anchor: EditAnchorLines, Lines: []int{0, 1}}, "lines [0, 1] is not a range: start is 1-based and end is at least start-1"},
		{"backwards", EditSite{Path: "a.go", Anchor: EditAnchorLines, Lines: []int{5, 3}}, "lines [5, 3] is not a range: start is 1-based and end is at least start-1"},
		{"all on lines", EditSite{Path: "a.go", Anchor: EditAnchorLines, Lines: []int{1, 1}, All: true}, "all applies only to the text anchor"},
		{"text without old", EditSite{Path: "a.go", Anchor: EditAnchorText}, "the text anchor needs old, the bytes it replaces"},
		{"lines on text", EditSite{Path: "a.go", Anchor: EditAnchorText, Old: "x", Lines: []int{1, 1}}, "lines applies only to the lines anchor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.site.Validate()
			if tc.err == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.err)
		})
	}
}

func TestEditAnchorSet(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"lines", "text"}, EditAnchor("").Values())
	assert.True(t, EditAnchorText.Valid())
	assert.False(t, EditAnchor("symbol").Valid())
}
