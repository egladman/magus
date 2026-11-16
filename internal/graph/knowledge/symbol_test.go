package knowledge

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func TestSymbolDecls(t *testing.T) {
	documented := func(s types.KnowledgeSymbol, doc string) types.KnowledgeSymbol {
		s.Doc = doc
		return s
	}
	syms := []types.KnowledgeSymbol{
		documented(goSym("a", "OpenFor().", "OpenFor", "Function", "func OpenFor(path string) error", "a/open.go"),
			"OpenFor reads the config at path and basically never writes."),
		documented(goSym("a", "Client#Reset().", "Reset", "Method", "func (*Client).Reset() *Client", "a/client.go"),
			"Reset returns a new Client."),
		documented(goSym("a", "WidgetFor#", "WidgetFor", "Struct", "type WidgetFor struct", "a/widget.go"),
			"WidgetFor holds the sub-agents a run spawned."),
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
	opts := SymbolDeclOptions{Generated: map[string]bool{"gen/value.go": true}}

	got := g.SymbolDecls(opts)

	assert.Equal(t, []types.SymbolDecl{
		{
			Node: symbolID(goNS("a") + "Client#Reset()."), Source: "a/client.go:1", Language: "go",
			Name: "Reset", Kind: "method", Owner: "Client", Doc: "Reset returns a new Client.",
		},
		{
			Node: symbolID(goNS("a") + "OpenFor()."), Source: "a/open.go:1", Language: "go",
			Name: "OpenFor", Kind: "function", Doc: "OpenFor reads the config at path and basically never writes.",
		},
		{
			Node: symbolID(goNS("a") + "WidgetFor#"), Source: "a/widget.go:1", Language: "go",
			Name: "WidgetFor", Kind: "struct", Doc: "WidgetFor holds the sub-agents a run spawned.",
		},
		{
			Node: symbolID("npm m 1.0 src/`open.ts`/openFile()."), Source: "src/open.ts:3", Language: "typescript",
			Name: "openFile", Kind: "function", Doc: "openFile reads one file and closes it.",
		},
	}, got, "test and generated sources are never listed, and the doc is not judged")

	assert.Equal(t, got, g.SymbolDecls(opts), "two runs over one graph agree")
}

// TestSymbolDeclsCarryTheWholeDoc pins that a doc reaches the record with its newlines and
// its full length, past the 256 bytes every other attr is cut to.
func TestSymbolDeclsCarryTheWholeDoc(t *testing.T) {
	const sentence = "The cache keeps one entry per key until it expires.\n"
	long := "Long keeps entries.\n" + strings.Repeat(sentence, 26)
	syms := []types.KnowledgeSymbol{{
		Key: goNS("a") + "Long().", Label: "Long", Language: "go", SymbolKind: "Function",
		Source: "a/long.go:1", Defs: []string{"a/long.go"}, Doc: long,
	}}
	g := namingGraph(t, syms)

	got := g.SymbolDecls(SymbolDeclOptions{})

	if assert.Len(t, got, 1) {
		assert.Equal(t, strings.TrimSpace(long), got[0].Doc, "the doc is whole, lines kept")
		assert.Greater(t, len(got[0].Doc), 256)
	}
}

func TestSymbolDeclsEmptyGraph(t *testing.T) {
	assert.Empty(t, NewGraph().SymbolDecls(SymbolDeclOptions{}))
}

// TestSymbolDeclsNameTheFileEachDocCameFrom pins a function written once per GOOS: one node,
// a doc per file. Each doc is listed with its own file, so a rule flagging the windows doc
// names the windows file, and a generated definition is still dropped.
func TestSymbolDeclsNameTheFileEachDocCameFrom(t *testing.T) {
	sym := goSym("a", "run().", "run", "Function", "func run() error", "a/run_unix.go")
	sym.Source, sym.Doc = "a/run_unix.go:25", "run replaces the process."
	sym.Defs = []string{"a/run_gen.go", "a/run_unix.go", "a/run_windows.go"}
	sym.Definitions = []types.KnowledgeSymbolDefinition{
		{Source: "a/run_gen.go:3", Doc: "run is generated."},
		{Source: "a/run_unix.go:25", Doc: "run replaces the process."},
		{Source: "a/run_windows.go:10", Doc: "run starts a child and waits."},
	}
	g := namingGraph(t, []types.KnowledgeSymbol{sym})

	got := g.SymbolDecls(SymbolDeclOptions{Generated: map[string]bool{"a/run_gen.go": true}})

	node := symbolID(goNS("a") + "run().")
	assert.Equal(t, []types.SymbolDecl{
		{Node: node, Source: "a/run_unix.go:25", Language: "go", Name: "run", Kind: "function", Doc: "run replaces the process."},
		{Node: node, Source: "a/run_windows.go:10", Language: "go", Name: "run", Kind: "function", Doc: "run starts a child and waits."},
	}, got)
}
