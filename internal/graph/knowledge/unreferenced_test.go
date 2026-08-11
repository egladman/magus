package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// unrefGraph assembles a symbol shard from records, the same path a real workspace takes.
func unrefGraph(t *testing.T, syms []types.KnowledgeSymbol) *Graph {
	t.Helper()
	return mergeAll([]Shard{assembleSymbols("pkg/a", syms, []types.TargetGraphProject{{Path: "pkg/a"}})})
}

func ids(entries []types.UnreferencedEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

func sym(key, file string) types.KnowledgeSymbol {
	return types.KnowledgeSymbol{Key: key, Label: key, Source: file + ":1", Defs: []string{file}}
}

// The base case: a symbol defined here that nothing calls and no other file names.
func TestUnreferencedFindsUnnamedSymbol(t *testing.T) {
	g := unrefGraph(t, []types.KnowledgeSymbol{sym("x Lonely().", "pkg/a/a.go")})
	got := g.Unreferenced()
	require.Len(t, got, 1)
	assert.Equal(t, "symbol:x Lonely().", got[0].ID)
	assert.Equal(t, "pkg/a/a.go:1", got[0].Source)
}

// A call from ANOTHER file makes it referenced.
func TestUnreferencedExcludesSymbolCalledFromAnotherFile(t *testing.T) {
	caller := sym("x Caller().", "pkg/a/a.go")
	caller.Calls = []types.KnowledgeSymbolCall{{Key: "x Callee().", Count: 1}}
	g := unrefGraph(t, []types.KnowledgeSymbol{caller, sym("x Callee().", "pkg/a/b.go")})

	assert.Equal(t, []string{"symbol:x Caller()."}, ids(g.Unreferenced()),
		"the callee is reached from a different file, so it is used")
}

// A call from within the SAME file does not count, exactly as a same-file reference does
// not. The two clauses have to agree here: treating a same-file call as use while
// treating a same-file reference as non-use would hide every helper used once in its own
// file and list every type sitting in precisely that position.
func TestUnreferencedTreatsSameFileCallLikeSameFileReference(t *testing.T) {
	caller := sym("x Caller().", "pkg/a/a.go")
	caller.Calls = []types.KnowledgeSymbolCall{{Key: "x Callee().", Count: 1}}
	g := unrefGraph(t, []types.KnowledgeSymbol{caller, sym("x Callee().", "pkg/a/a.go")})

	assert.Equal(t, []string{"symbol:x Callee().", "symbol:x Caller()."}, ids(g.Unreferenced()),
		"both are confined to one file, so both are worth surfacing (sorted by ID)")
}

// A package or namespace is never called or referenced in this model - its imports are
// file-to-file edges - so listing them would report the workspace's shape, not its code.
// The test is here because the exclusion reads the SCIP descriptor grammar off the node
// ID rather than SymbolInformation.Kind, which scip-typescript never populates.
func TestUnreferencedSkipsNamespaces(t *testing.T) {
	pkg := types.KnowledgeSymbol{
		Key: "x `example.com/x/pkg`/", Label: "pkg", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"},
	}
	g := unrefGraph(t, []types.KnowledgeSymbol{pkg, sym("x Lonely().", "pkg/a/a.go")})
	assert.Equal(t, []string{"symbol:x Lonely()."}, ids(g.Unreferenced()))
}

// A reference from another file counts// A reference from another file counts, which is what covers the things a call edge
// cannot be: structs, fields, constants.
func TestUnreferencedExcludesCrossFileReference(t *testing.T) {
	s := sym("x Kind#", "pkg/a/a.go")
	s.Refs = []types.KnowledgeSymbolRef{{Path: "pkg/a/b.go", Count: 1, Lines: []int{7}}}
	assert.Empty(t, unrefOne(t, s), "another file names it")
}

// A reference from the SAME file it is defined in does NOT count. A symbol used only
// where it is declared is exactly the case worth surfacing, and counting that use would
// hide every one of them.
func TestUnreferencedCountsSelfFileReferenceAsUnreferenced(t *testing.T) {
	s := sym("x Local().", "pkg/a/a.go")
	s.Refs = []types.KnowledgeSymbolRef{{Path: "pkg/a/a.go", Count: 3, Lines: []int{2, 3, 4}}}
	assert.Equal(t, []string{"symbol:x Local()."}, ids(unrefOne(t, s)))
}

// A symbol with no definition in this workspace belongs to a dependency; it was minted
// only because something here referenced it, and it is not ours to call dead.
func TestUnreferencedSkipsSymbolDefinedElsewhere(t *testing.T) {
	external := types.KnowledgeSymbol{
		Key:   "dep Helper().",
		Label: "Helper",
		Refs:  []types.KnowledgeSymbolRef{{Path: "pkg/a/a.go", Count: 1, Lines: []int{9}}},
	}
	assert.Empty(t, ids(unrefGraph(t, []types.KnowledgeSymbol{external}).Unreferenced()))
}

// The list is a checklist someone works through, so it must not reorder between runs.
func TestUnreferencedIsSortedByID(t *testing.T) {
	g := unrefGraph(t, []types.KnowledgeSymbol{
		sym("x Zeta().", "pkg/a/a.go"),
		sym("x Alpha().", "pkg/a/a.go"),
		sym("x Mid().", "pkg/a/a.go"),
	})
	assert.Equal(t, []string{"symbol:x Alpha().", "symbol:x Mid().", "symbol:x Zeta()."}, ids(g.Unreferenced()))
}

// Attrs the report triages on ride through: an unreferenced exported Function reads very
// differently from an unreferenced Field.
func TestUnreferencedCarriesKindAndLanguage(t *testing.T) {
	s := sym("x Thing#", "pkg/a/a.go")
	s.SymbolKind = "Struct"
	s.Language = "go"
	got := unrefOne(t, s)
	require.Len(t, got, 1)
	assert.Equal(t, "Struct", got[0].Kind)
	assert.Equal(t, "go", got[0].Language)
}

func unrefOne(t *testing.T, s types.KnowledgeSymbol) []types.UnreferencedEntry {
	t.Helper()
	return unrefGraph(t, []types.KnowledgeSymbol{s}).Unreferenced()
}
