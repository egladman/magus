package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyCommandLine(t *testing.T) {
	for _, c := range []struct {
		line, cat, sym string
	}{
		{"magus query kind=spell", "graph", ""},
		{"magus refs HandleFoo", "graph", ""},
		{"./magus explain spell:go", "graph", ""},
		// A repo-wide grep whose pattern is a real (mixed-case) identifier is a refs candidate.
		{"grep -rn HandleFoo internal/", "search-source", "HandleFoo"},
		// A repo-wide grep for a lowercase word is still a source search, but NOT a symbol
		// candidate - refs would never resolve "error".
		{"grep -rn error internal/", "search-source", ""},
		// A search naming a .md is prose, routed to docsection.
		{"grep -rn setup docs/guide.md", "search-prose", ""},
		// A single-file grep is reading one file, not a repo-wide question.
		{"grep pattern onefile.go", "search-other", ""},
		{"cat internal/foo.go", "read", ""},
		{"sed -n '1,20p' foo.go", "read", ""},
		{"magus run test .", "magus", ""},
		{"ls -la", "other", ""},
	} {
		cat, sym := classifyCommandLine(c.line)
		assert.Equal(t, c.cat, cat, "category of %q", c.line)
		assert.Equal(t, c.sym, sym, "symbol of %q", c.line)
	}
}

func TestAnalyzeAdoptionRatioAndTotals(t *testing.T) {
	r := analyzeAdoption([]string{
		"grep -rn HandleFoo internal/",
		"grep -rn HandleFoo cmd/",
		"grep -rn parseQuery .",
		"cat foo.go",
		"magus query kind=spell",
		"", // blank lines are skipped
		"magus run test .",
	})
	assert.Equal(t, 6, r.Total, "blank line not counted")
	assert.Equal(t, 1, r.GraphVerbs)
	assert.Equal(t, 3, r.TextSearches)
	assert.Equal(t, 3, r.SearchOfSource)
	assert.Equal(t, 1, r.FileReads)
	assert.Equal(t, 1, r.MagusRuns)
	// HandleFoo grepped twice ranks above parseQuery grepped once.
	if assert.Len(t, r.TopSymbolGreps, 2) {
		assert.Equal(t, patternCount{Pattern: "HandleFoo", Count: 2}, r.TopSymbolGreps[0])
	}
}

func TestRatioString(t *testing.T) {
	assert.Equal(t, "1 : 20", ratioString(1057, 21360))
	assert.Equal(t, "3 : 1", ratioString(30, 10))
	assert.Equal(t, "0 : 5", ratioString(0, 5))
	assert.Equal(t, "n/a", ratioString(0, 0))
}

func TestLooksLikeSymbol(t *testing.T) {
	assert.True(t, looksLikeSymbol("HandleFoo"))
	assert.True(t, looksLikeSymbol("parseQuery"))
	assert.False(t, looksLikeSymbol("error"), "a lowercase common word is not a symbol candidate")
	assert.False(t, looksLikeSymbol("node_modules"))
	assert.False(t, looksLikeSymbol("go test"), "a phrase is not an identifier")
}

func TestHumanCount(t *testing.T) {
	assert.Equal(t, "0", humanCount(0))
	assert.Equal(t, "999", humanCount(999))
	assert.Equal(t, "1,057", humanCount(1057))
	assert.Equal(t, "70,264", humanCount(70264))
}
