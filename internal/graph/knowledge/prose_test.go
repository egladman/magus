package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func TestProse(t *testing.T) {
	documented := func(s types.KnowledgeSymbol, doc string) types.KnowledgeSymbol {
		s.Doc = doc
		return s
	}
	syms := []types.KnowledgeSymbol{
		documented(goSym("a", "OpenFor().", "OpenFor", "Function", "func OpenFor(path string) error", "a/open.go"),
			"OpenFor reads the config at path and basically never writes."),
		// Reset is a stub only because Client, its owner, is a word the doc may repeat.
		documented(goSym("a", "Client#Reset().", "Reset", "Method", "func (*Client).Reset() *Client", "a/client.go"),
			"Reset returns a new Client."),
		// Not callable, so its name is not judged.
		documented(goSym("a", "WidgetFor#", "WidgetFor", "Struct", "type WidgetFor struct", "a/widget.go"),
			"WidgetFor holds the sub-agents a run spawned."),
		documented(goSym("a", "Clean().", "Clean", "Function", "func Clean()", "a/clean.go"), "Clean removes the stale lock files."),
		documented(goSym("a", "TestOpenFor().", "TestOpenFor", "Function", "func TestOpenFor(t *testing.T)", "a/open_test.go"),
			"TestOpenFor simply checks OpenFor."),
		documented(goSym("gen", "ValueFor().", "ValueFor", "Function", "func ValueFor() int", "gen/value.go"),
			"ValueFor simply returns a value."),
		documented(goSym("a", "DescFor().", "DescFor", "Function", "func DescFor() int", "a/desc.pb.go"), "DescFor basically describes."),
		{
			Key: "npm m 1.0 src/`open.ts`/openFile().", Label: "openFile", Language: "typescript",
			SymbolKind: "Function", Source: "src/open.ts:3", Defs: []string{"src/open.ts"},
			Doc: "openFile reads one file and closes it.",
		},
	}
	g := namingGraph(t, syms)
	opts := ProseOptions{Generated: map[string]bool{"gen/value.go": true}}

	findings, judged := g.Prose(opts)

	assert.Equal(t, []types.ProseFinding{
		{
			Node: symbolID(goNS("a") + "Client#Reset()."), Source: "a/client.go:1", Language: "go", Rule: "docstub",
			Message: "Doc comment only repeats the name Reset; state the contract a caller relies on (edge cases, errors, ownership), or keep it to what the name cannot say.",
			Match:   "Reset returns a new Client.",
		},
		{
			Node: symbolID(goNS("a") + "OpenFor()."), Source: "a/open.go:1", Language: "go", Rule: "filler",
			Message: "Drop 'basically': state the fact.", Match: "basically",
		},
		{
			Node: symbolID(goNS("a") + "OpenFor()."), Source: "a/open.go:1", Language: "go", Rule: "name-suffix",
			Message: "Rename 'OpenFor': no function or method name ends in the word Of or For.", Match: "OpenFor",
		},
		{
			Node: symbolID(goNS("a") + "WidgetFor#"), Source: "a/widget.go:1", Language: "go", Rule: "terms",
			Message: "Write 'subagents', not 'sub-agents'.", Match: "sub-agents",
		},
	}, findings, "test and generated sources are never judged")
	assert.Equal(t, map[string]int{"go": 4, "typescript": 1}, judged)

	again, _ := g.Prose(opts)
	assert.Equal(t, findings, again, "two runs over one graph agree")
}

func TestProseEmptyGraph(t *testing.T) {
	findings, judged := NewGraph().Prose(ProseOptions{})
	assert.Empty(t, findings)
	assert.Empty(t, judged, "no language was read, so none reads as passed")
}
