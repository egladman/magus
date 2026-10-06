package knowledge

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// buzzSymbolKey is the key scip-buzz's symbol for a top-level fun reaches the graph under:
// package `buzz . .` with its version stripped, the file a namespace, the fun a method.
func buzzSymbolKey(rel, name string) string { return "buzz . `" + rel + "`/" + name + "()." }

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

	sym := func(name string, line int) types.KnowledgeSymbol {
		return types.KnowledgeSymbol{
			Key: buzzSymbolKey("a.buzz", name), Label: name, Language: "buzz",
			Source: "a.buzz:" + strconv.Itoa(line), Defs: []string{"a.buzz"},
		}
	}
	build, helper := sym("build", 2), sym("helper", 6)
	build.Calls = []types.KnowledgeSymbolCall{{Key: helper.Key, Count: 1}}
	symbols := assembleSymbols(".", []types.KnowledgeSymbol{build, helper}, []types.TargetGraphProject{{Path: "."}})

	g := mergeAll([]Shard{assembleBuzz(root)})
	g.Merge(symbols.Nodes, symbols.Edges)
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

// Without a Buzz symbol in the graph (no index built, or the project binds no buzz spell)
// the pass changes nothing.
func TestSupersedeBuzzFunctionsWithoutABuzzIndexKeepsEverything(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.buzz", "export fun build() > void { helper(); }\nfun helper() > void {}\n")
	goSym := assembleSymbols(".", []types.KnowledgeSymbol{{
		Key: "gomod example.com/a `example.com/a`/Build().", Label: "Build", Language: "go", Source: "a.go:1", Defs: []string{"a.go"},
	}}, nil)

	g := mergeAll([]Shard{assembleBuzz(root)})
	g.Merge(goSym.Nodes, goSym.Edges)
	before := g.Output()
	g.supersedeBuzzFunctions()

	assert.Equal(t, before, g.Output())
}
