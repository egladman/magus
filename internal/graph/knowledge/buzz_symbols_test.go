package knowledge

import (
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/types"
)

// buzzOcc is one occurrence in a fake scip-buzz index: the moniker, its 0-based line, and
// for a definition the last line of the body it encloses.
type buzzOcc struct {
	moniker string
	line    int32
	def     bool
	endLine int32
}

// buzzFun is scip-buzz's moniker for the top-level fun name in the workspace file rel: the
// file a Namespace descriptor, the fun a Method one.
func buzzFun(rel, name string) string { return "scip-buzz buzz . . `" + rel + "`/" + name + "()." }

// buzzIndexSymbols reads a fake scip-buzz index through the real ingestion, so the symbols
// reach the graph keyed and labeled the way a built index's do.
func buzzIndexSymbols(t *testing.T, docs map[string][]buzzOcc) []types.KnowledgeSymbol {
	t.Helper()
	idx := &scip.Index{}
	for rel, occs := range docs {
		doc := &scip.Document{RelativePath: rel, Language: "buzz"}
		for _, o := range occs {
			occ := &scip.Occurrence{Symbol: o.moniker, Range: []int32{o.line, 0, 1}}
			if o.def {
				occ.SymbolRoles = int32(scip.SymbolRole_Definition)
				//nolint:staticcheck // real indexes set the packed field, which ingestion reads, not the oneof.
				occ.EnclosingRange = []int32{o.line, 0, o.endLine, 1}
			}
			doc.Occurrences = append(doc.Occurrences, occ)
		}
		idx.Documents = append(idx.Documents, doc)
	}
	syms := symbols.ParseDecoded(t.Context(), idx, ".", "buzz")
	require.NotEmpty(t, syms)
	return syms
}

func symbolNamed(t *testing.T, syms []types.KnowledgeSymbol, label string) types.KnowledgeSymbol {
	t.Helper()
	for _, s := range syms {
		if s.Label == label {
			return s
		}
	}
	t.Fatalf("no symbol labeled %s", label)
	return types.KnowledgeSymbol{}
}

// A file the Buzz index covers keeps its file node, import edges and rationale, while its
// function nodes give way to the index's symbols and the rationale moves to the enclosing
// function's symbol. A file no index covers keeps its function nodes, and an old function
// ID still reaches refs through the symbol that replaced it.
func TestSupersedeBuzzFunctionsLeavesOneNodePerFunction(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.buzz", `import "b";
export fun build() > void {
    // NOTE: build is tricky
    helper();
}
fun helper() > void {}
`)
	writeFile(t, root, "b.buzz", "export fun thing() > void {}\nfun other() > void { thing(); }\n")

	syms := buzzIndexSymbols(t, map[string][]buzzOcc{"a.buzz": {
		{moniker: buzzFun("a.buzz", "build"), line: 1, def: true, endLine: 4},
		{moniker: buzzFun("a.buzz", "helper"), line: 3},
		{moniker: buzzFun("a.buzz", "helper"), line: 5, def: true, endLine: 5},
	}})
	build, helper := symbolNamed(t, syms, "build"), symbolNamed(t, syms, "helper")
	assembled := assembleSymbols(".", syms, []types.TargetGraphProject{{Path: "."}})

	g := mergeAll([]Shard{assembleBuzz(root)})
	g.Merge(assembled.Nodes, assembled.Edges)
	g.supersedeBuzzFunctions()
	out := g.Output()

	for _, id := range []string{"function:a.buzz:build", "function:a.buzz:helper"} {
		_, ok := nodeByID(out, id)
		assert.Falsef(t, ok, "%s duplicates the index's symbol", id)
	}
	for _, id := range []string{"file:a.buzz", "function:b.buzz:thing", "function:b.buzz:other"} {
		_, ok := nodeByID(out, id)
		assert.Truef(t, ok, "%s is kept", id)
	}
	assert.True(t, hasEdge(out, "file:a.buzz", "file:b.buzz", types.RelationImports), "imports stay")
	assert.True(t, hasEdge(out, "function:b.buzz:other", "function:b.buzz:thing", types.RelationCalls), "an uncovered file keeps its calls")
	assert.True(t, hasEdge(out, symbolID(build.Key), symbolID(helper.Key), types.RelationCalls), "the index's call stands in for the AST's")

	var rationale []types.KnowledgeEdge
	for _, e := range out.Links {
		if e.Relation == types.RelationRationaleFor {
			rationale = append(rationale, e)
		}
	}
	require.Len(t, rationale, 1)
	assert.Equal(t, symbolID(build.Key), rationale[0].Target, "the NOTE explains build's symbol")
	for _, e := range out.Links {
		assert.NotContains(t, []string{e.Source, e.Target}, "function:a.buzz:build", "no edge dangles at a retired node")
	}
	assert.Empty(t, g.UndeclaredEdges())

	refs, ok := g.Refs("function:a.buzz:build")
	require.True(t, ok, "a retired function ID still resolves")
	assert.Equal(t, symbolID(build.Key), refs.Symbol)
}

// A function the index defines no symbol for keeps its node: an extern fun, which
// scip-buzz records only as a forward definition, and a fun it placed nowhere. Retiring
// either would delete the only node the function has.
func TestSupersedeBuzzFunctionsKeepsAFunctionNoSymbolSupersedes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.buzz", "export fun build() > void { native(); }\nextern fun native() > void;\nfun unplaced() > void {}\n")

	syms := buzzIndexSymbols(t, map[string][]buzzOcc{"a.buzz": {
		{moniker: buzzFun("a.buzz", "build"), line: 0, def: true, endLine: 0},
		{moniker: buzzFun("a.buzz", "native"), line: 0},
		{moniker: buzzFun("a.buzz", "native"), line: 1},
	}})
	assembled := assembleSymbols(".", syms, []types.TargetGraphProject{{Path: "."}})
	g := mergeAll([]Shard{assembleBuzz(root)})
	_, ok := nodeByID(g.Output(), "function:a.buzz:native")
	require.True(t, ok, "the AST gives an extern fun a function node")
	g.Merge(assembled.Nodes, assembled.Edges)
	g.supersedeBuzzFunctions()
	out := g.Output()

	_, ok = nodeByID(out, "function:a.buzz:build")
	assert.False(t, ok, "build has a defined symbol")
	_, ok = nodeByID(out, "function:a.buzz:unplaced")
	assert.True(t, ok, "no symbol names unplaced")
	_, ok = nodeByID(out, "function:a.buzz:native")
	assert.True(t, ok, "the index only forward-defines native")
}

// Only a scip-buzz top-level function supersedes a function node: a method of the same
// name, another indexer's symbol spelled like one, or a moniker that does not parse
// matches nothing.
func TestBuzzFunctionParsesTheGrammar(t *testing.T) {
	rel, name, ok := buzzFunction(buzzFun("hack/ci/run.buzz", "run"))
	require.True(t, ok)
	assert.Equal(t, "hack/ci/run.buzz", rel)
	assert.Equal(t, "run", name)

	for _, moniker := range []string{
		"scip-buzz buzz . . `hack/ci/run.buzz`/Rect#area().",
		"scip-buzz buzz . . `hack/ci/run.buzz`/run().(x)",
		"scip-go gomod example.com/m v1 `hack/ci/run.buzz`/run().",
		"scip-buzz npm . . `hack/ci/run.buzz`/run().",
		"scip-buzz buzz . . `unterminated/run().",
		"",
	} {
		_, _, ok := buzzFunction(moniker)
		assert.Falsef(t, ok, "%q is no Buzz top-level function", moniker)
	}
}

// Without a Buzz symbol in the graph (no index built, or the project binds no buzz spell)
// the pass changes nothing.
func TestSupersedeBuzzFunctionsWithoutABuzzIndexKeepsEverything(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.buzz", "export fun build() > void { helper(); }\nfun helper() > void {}\n")
	goSym := assembleSymbols(".", []types.KnowledgeSymbol{{
		Key: "gomod example.com/a `example.com/a`/Build().", Moniker: "scip-go gomod example.com/a v1 `example.com/a`/Build().",
		Label: "Build", Language: "go", Source: "a.go:1", Defs: []string{"a.go"},
	}}, nil)

	g := mergeAll([]Shard{assembleBuzz(root)})
	g.Merge(goSym.Nodes, goSym.Edges)
	before := g.Output()
	g.supersedeBuzzFunctions()

	assert.Equal(t, before, g.Output())
}
