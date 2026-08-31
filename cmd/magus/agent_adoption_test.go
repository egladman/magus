package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// classification pairs the two return values of classifyCommandLine so a wrong
// row reports once, with both halves visible.
type classification struct {
	Category string
	Symbol   string
}

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
		// The report's columns are frozen, so these classes are credited to no
		// category on purpose and land in "other".
		{"find . -name '*.go'", "other", ""},
		{"awk '{print $1}' foo.txt", "other", ""},
		{"bat internal/foo.go", "other", ""},
	} {
		t.Run(c.line, func(t *testing.T) {
			cat, sym := classifyCommandLine(c.line)
			require.Equal(t, classification{c.cat, c.sym}, classification{cat, sym})
		})
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
	// The WHOLE report, so a line mis-filed into a column nobody names - Other and
	// SearchOfProse were the unwatched ones - shows up as a diff rather than passing.
	// HandleFoo grepped twice ranks above parseQuery grepped once.
	require.Equal(t, adoptionReport{
		Total:          6, // the blank line is not counted
		GraphVerbs:     1,
		TextSearches:   3,
		SearchOfSource: 3,
		SearchOfProse:  0,
		FileReads:      1,
		MagusRuns:      1,
		Other:          0,
		TopSymbolGreps: []patternCount{
			{Pattern: "HandleFoo", Count: 2, Run: "magus refs HandleFoo"},
			{Pattern: "parseQuery", Count: 1, Run: "magus refs parseQuery"},
		},
	}, r)
}

// TestTopSymbolGrepRunsRouteByShape pins that the report's rendered command
// comes from the hint translator, not a hardcoded refs template: a diagnostic
// code routes to query, an identifier to refs. The slice is compared whole, so
// the count and topPatterns' alphabetical tie-break between two equal counts are
// pinned along with the routing.
func TestTopSymbolGrepRunsRouteByShape(t *testing.T) {
	r := analyzeAdoption([]string{
		"grep -rn MGS2011 docs/",
		"grep -rn HandleFoo .",
	})
	require.Equal(t, []patternCount{
		{Pattern: "HandleFoo", Count: 1, Run: "magus refs HandleFoo"},
		{Pattern: "MGS2011", Count: 1, Run: "magus query MGS2011"},
	}, r.TopSymbolGreps)
}

// TestAdoptionTable compares the whole rendering: the layout is fixed-width and
// deterministic, so a column that shifts or a section that stops being emitted is
// a diff rather than a substring that happens to survive.
func TestAdoptionTable(t *testing.T) {
	for _, tt := range []struct {
		name   string
		report adoptionReport
		want   string
	}{
		{
			name: "populated report",
			report: adoptionReport{
				Total: 6, GraphVerbs: 1, TextSearches: 3, SearchOfSource: 3, FileReads: 1, MagusRuns: 1,
				TopSymbolGreps: []patternCount{
					{Pattern: "HandleFoo", Count: 2, Run: "magus refs HandleFoo"},
					{Pattern: "parseQuery", Count: 1, Run: "magus refs parseQuery"},
				},
			},
			want: `agent adoption over 6 commands:

  graph verbs (query/refs/explain/path/graph)          1
  text searches (grep/rg/ag)                           3
  graph : search ratio                             1 : 3

  repo-wide search over source (refs/query)            3
  search over prose .md (docsection)                   0
  file reads via shell (cat/head/tail/sed)             1

top repo-wide patterns that look like symbols, with the graph query to try:
       2  HandleFoo  ->  magus refs HandleFoo
       1  parseQuery  ->  magus refs parseQuery
`,
		},
		{
			// A pattern the translator abstained on keeps its count and loses the
			// arrow, rather than pointing at nothing.
			name:   "pattern with no run",
			report: adoptionReport{Total: 1, TopSymbolGreps: []patternCount{{Pattern: "HandleFoo", Count: 2}}},
			want: `agent adoption over 1 commands:

  graph verbs (query/refs/explain/path/graph)          0
  text searches (grep/rg/ag)                           0
  graph : search ratio                               n/a

  repo-wide search over source (refs/query)            0
  search over prose .md (docsection)                   0
  file reads via shell (cat/head/tail/sed)             0

top repo-wide patterns that look like symbols, with the graph query to try:
       2  HandleFoo
`,
		},
		{
			// No symbol patterns means no second section at all, heading included.
			name:   "no symbol patterns",
			report: adoptionReport{Total: 2, MagusRuns: 2},
			want: `agent adoption over 2 commands:

  graph verbs (query/refs/explain/path/graph)          0
  text searches (grep/rg/ag)                           0
  graph : search ratio                               n/a

  repo-wide search over source (refs/query)            0
  search over prose .md (docsection)                   0
  file reads via shell (cat/head/tail/sed)             0
`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeAdoptionTable(&b, tt.report)
			require.Equal(t, tt.want, b.String())
		})
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
