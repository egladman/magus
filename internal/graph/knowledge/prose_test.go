package knowledge

import (
	"strings"
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

// TestProseJudgesTheWholeDoc pins that a doc reaches prose.Judge with its newlines and its
// full length, past the 256 bytes every other attr is cut to: a 260-word doc is over the
// block budget, and a fenced block's words are code the budget never counts.
func TestProseJudgesTheWholeDoc(t *testing.T) {
	const sentence = "The cache keeps one entry per key until it expires.\n" // ten words
	long := "Long keeps entries.\n" + strings.Repeat(sentence, 26)
	fenced := "Fenced keeps entries.\n" + strings.Repeat(sentence, 20) +
		"\n```\n" + strings.Repeat(sentence, 6) + "x := a - b\n```\n"
	syms := []types.KnowledgeSymbol{
		{
			Key: goNS("a") + "Long().", Label: "Long", Language: "go", SymbolKind: "Function",
			Source: "a/long.go:1", Defs: []string{"a/long.go"}, Doc: long,
		},
		{
			Key: goNS("a") + "Fenced().", Label: "Fenced", Language: "go", SymbolKind: "Function",
			Source: "a/fenced.go:1", Defs: []string{"a/fenced.go"}, Doc: fenced,
		},
	}
	g := namingGraph(t, syms)

	assert.Equal(t, strings.TrimSpace(long), g.nodes[symbolID(goNS("a")+"Long().")].Attrs[AttrDoc],
		"the doc is stored whole, lines kept")

	findings, _ := g.Prose(ProseOptions{})

	assert.Equal(t, []types.ProseFinding{{
		Node: symbolID(goNS("a") + "Long()."), Source: "a/long.go:1", Language: "go", Rule: "comment-block",
		Message: "Keep a comment block under 250 words: say why, and move the rest to docs.",
	}}, findings, "the fenced doc's prose is 203 words, its code neither counted nor read as an aside")
}

func TestProseEmptyGraph(t *testing.T) {
	findings, judged := NewGraph().Prose(ProseOptions{})
	assert.Empty(t, findings)
	assert.Empty(t, judged, "no language was read, so none reads as passed")
}
