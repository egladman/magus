package knowledge

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

const (
	apiDir                    = "internal/a"
	apiA, apiB, apiTest       = "internal/a/a.go", "internal/a/b.go", "internal/a/a_test.go"
	apiUser, apiUser2, apiCmd = "internal/b/b.go", "internal/b/c.go", "cmd/tool/main.go"
	apiOtherTest              = "internal/b/b_test.go"
)

// apiSym is a Go symbol in internal/a, labeled with its last descriptor, defined in file and
// referenced from refs.
func apiSym(desc, label, file string, refs ...string) types.KnowledgeSymbol {
	s := conformanceSym("/"+apiDir, desc, file, 3, 5, 20, "", 0, refs...)
	s.Label = label
	return s
}

func apiSymbolID(desc string) string { return symbolID(conformanceNS("/"+apiDir) + desc) }

// apiDirShard is internal/a's dir node as @packagedirs mints it.
func apiDirShard() Shard {
	return Shard{Name: packageDirsShardName, Nodes: []types.KnowledgeNode{{
		ID: dirID(apiDir), Kind: types.KindDir, Label: apiDir, Source: apiDir,
		Attrs: map[string]string{types.AttrLanguage: "go"},
	}}}
}

// apiFixture is internal/a with symbols used outside it, used only inside it, used only by
// tests, defined by a test, a field, a local and its namespace. F is defined at fFile:fLine.
func apiFixture(fFile string, fLine int) *Graph {
	f := apiSym("F().", "F", fFile, apiUser, apiUser2, apiCmd)
	f.Source = fmt.Sprintf("%s:%d", fFile, fLine)
	f.Signature = "func F() error"
	local := apiSym("", "x", apiA, apiCmd)
	local.Key = "local 7"
	syms := []types.KnowledgeSymbol{
		conformancePkg("/"+apiDir, []string{apiA, apiB, apiTest}, apiUser, apiCmd),
		f,
		apiSym("T#", "T", apiA, apiUser),
		apiSym("T#M().", "M", apiB, apiCmd),
		apiSym("T#Field.", "Field", apiA, apiUser, apiCmd),
		apiSym("V.", "V", apiA, apiCmd),
		apiSym("inner().", "inner", apiA, apiB),
		apiSym("OnlyTests().", "OnlyTests", apiA, apiOtherTest),
		apiSym("Helper().", "Helper", apiTest, apiUser),
		local,
	}
	return mergeAll([]Shard{apiDirShard(), assembleSymbols(".", syms, nil)})
}

func apiIDs(syms []types.PublicSymbol) []string {
	ids := make([]string, len(syms))
	for i, s := range syms {
		ids[i] = s.ID
	}
	return ids
}

func TestPublicAPIListsSymbolsUsedOutsideTheirDir(t *testing.T) {
	got := apiFixture(apiA, 3).PublicAPI([]string{apiDir})
	require.NotEmpty(t, got)
	assert.Equal(t, types.PublicSymbol{
		ID: apiSymbolID("F()."), Label: "F", Dir: apiDir, Signature: "func F() error", UsedBy: 3,
	}, got[0], "three files outside the dir reference F")
	assert.Equal(t, []string{apiSymbolID("F()."), apiSymbolID("T#"), apiSymbolID("T#M()."), apiSymbolID("V.")},
		apiIDs(got), "functions, types, methods and values; never a field, a local or the namespace")
}

func TestPublicAPIIgnoresTestFilesAndUsesInside(t *testing.T) {
	ids := apiIDs(apiFixture(apiA, 3).PublicAPI([]string{apiDir}))
	assert.NotContains(t, ids, apiSymbolID("inner()."), "a use inside the dir is not public")
	assert.NotContains(t, ids, apiSymbolID("OnlyTests()."), "a use from a test file is not public")
	assert.NotContains(t, ids, apiSymbolID("Helper()."), "a test file's definition is not the dir's API")
}

func TestPublicAPIOrdersByUsedByThenID(t *testing.T) {
	syms := []types.KnowledgeSymbol{
		apiSym("B().", "B", apiA, apiUser),
		apiSym("A().", "A", apiA, apiUser),
		apiSym("C().", "C", apiA, apiUser, apiCmd),
	}
	g := mergeAll([]Shard{assembleSymbols(".", syms, nil)})
	got := g.PublicAPI([]string{apiDir})
	assert.Equal(t, []string{apiSymbolID("C()."), apiSymbolID("A()."), apiSymbolID("B().")}, apiIDs(got))
	assert.Equal(t, []int{2, 1, 1}, []int{got[0].UsedBy, got[1].UsedBy, got[2].UsedBy})
}

func TestPublicAPIIgnoresWhereADefinitionSits(t *testing.T) {
	want := apiFixture(apiA, 3).PublicAPI([]string{apiDir})
	assert.Equal(t, want, apiFixture(apiA, 8).PublicAPI([]string{apiDir}), "a moved line")
	assert.Equal(t, want, apiFixture(apiB, 3).PublicAPI([]string{apiDir}), "a moved file inside the dir")
}

func TestPublicAPIGapWithoutSymbols(t *testing.T) {
	out, ok := mergeAll([]Shard{apiDirShard()}).Explain(dirID(apiDir))
	require.True(t, ok)
	assert.Empty(t, out.PublicAPI)
	assert.Contains(t, out.PublicAPIGap, "no go symbols for "+apiDir, "an empty list without symbols says why")

	g := mergeAll([]Shard{apiDirShard(), assembleSymbols(".", []types.KnowledgeSymbol{
		apiSym("inner().", "inner", apiA, apiB),
	}, nil)})
	out, ok = g.Explain(dirID(apiDir))
	require.True(t, ok)
	assert.Empty(t, out.PublicAPI)
	assert.Empty(t, out.PublicAPIGap, "with symbols loaded an empty list means nothing outside uses the dir")

	out, ok = apiFixture(apiA, 3).Explain(dirID(apiDir))
	require.True(t, ok)
	assert.Len(t, out.PublicAPI, 4)
	assert.Empty(t, out.PublicAPIGap)
}
