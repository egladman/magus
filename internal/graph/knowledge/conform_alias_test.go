package knowledge

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// aliasPkg is Go package pkg declaring name in files, imported by each importer file under
// the alias it maps to, "" for none.
func aliasPkg(pkg, name string, files []string, importers map[string]string) types.KnowledgeSymbol {
	s := conformPkg(pkg, files)
	s.PackageName = name
	for _, f := range slices.Sorted(maps.Keys(importers)) {
		s.Refs = append(s.Refs, types.KnowledgeSymbolRef{Path: f, Count: 1, Lines: []int{3}, Alias: importers[f]})
	}
	return s
}

// importersOf is n files internal/use/<prefix>NN.go, the first of them aliasing as the
// spellings say in order.
func importersOf(prefix string, n int, spellings ...string) map[string]string {
	out := map[string]string{}
	for i := range n {
		alias := ""
		if i < len(spellings) {
			alias = spellings[i]
		}
		out[fmt.Sprintf("internal/use/%s%02d.go", prefix, i)] = alias
	}
	return out
}

// aliasFixture is a workspace whose packages show each case alias-design.md names.
func aliasFixture() []types.KnowledgeSymbol {
	queue := importersOf("q", 13, "qtypes", "qtypes", "qtypes", "queuetypes", "queuetypes")
	root := importersOf("t", 12)
	for i := range 8 {
		root[fmt.Sprintf("internal/use/q%02d.go", i)] = ""
	}
	mcp := importersOf("m", 4, "mcplib", "mcplib", "mcplib", "mcplib")
	mcp["internal/mcp/server.go"] = "mcplib"
	return []types.KnowledgeSymbol{
		// 8 of internal/queue/types' 13 importers also import types (20): 62%, blamed on the lesser.
		aliasPkg("/internal/queue/types", "types", []string{"internal/queue/types/types.go"}, queue),
		aliasPkg("/types", "types", []string{"types/types.go"}, root),
		// Every importer, one spelling.
		aliasPkg("/internal/spell/remote", "remote", []string{"internal/spell/remote/remote.go"}, importersOf("r", 6, "remotespell", "remotespell", "remotespell", "remotespell", "remotespell", "remotespell")),
		// Aliased to its own name: never needed.
		aliasPkg("/internal/json", "json", []string{"internal/json/json.go"}, importersOf("j", 6, "json", "json", "json", "json", "json", "json")),
		// One importer sits inside a package of the same name.
		aliasPkg("/lib/mcp", "mcp", []string{"lib/mcp/mcp.go"}, mcp),
		aliasPkg("/internal/mcp", "mcp", []string{"internal/mcp/server.go"}, nil),
		// Below MinSubject, and generated.
		aliasPkg("/internal/ci", "ci", []string{"internal/ci/ci.go"}, importersOf("c", 3, "internalci", "internalci", "internalci")),
		aliasPkg("/internal/a/gen", "gen", []string{"internal/a/gen/gen.go"}, importersOf("g", 6, "agen", "agen", "agen", "agen", "agen", "agen")),
	}
}

func aliasGraph(syms ...types.KnowledgeSymbol) *Graph {
	return mergeAll([]Shard{assembleSymbols(".", append(aliasFixture(), syms...), nil)})
}

func aliasOptions() types.ConformOptions {
	return types.ConformOptions{
		Limits: map[types.ConformKey]types.ConformLimit{
			couplingKey(types.ConformAliasShare): {Percentile: 95, Anchor: 30, MinSubject: 5},
			couplingKey(types.ConformNameClash):  {Percentile: 99, Anchor: 25, MinSubject: 5},
		},
		MinSample: 1,
		Step:      0.05,
		Gates:     testGates(),
	}
}

func aliasFacts(t *testing.T, r types.ImportAliasReport, dir string) types.AliasFacts {
	t.Helper()
	i := slices.IndexFunc(r.Packages, func(f types.AliasFacts) bool { return f.Dir == dir })
	require.GreaterOrEqual(t, i, 0, dir)
	return r.Packages[i]
}

func TestImportAliases(t *testing.T) {
	r := aliasGraph().ImportAliases()

	require.True(t, r.Indexed)
	var dirs []string
	for _, f := range r.Packages {
		dirs = append(dirs, f.Dir)
	}
	assert.Equal(t, []string{
		"internal/a/gen", "internal/ci", "internal/json", "internal/queue/types", "internal/spell/remote", "lib/mcp", "types",
	}, dirs, "internal/mcp has no importer")

	assert.Equal(t, types.AliasFacts{
		Dir: "internal/queue/types", Name: "types", Importers: 13, Aliased: 5, Needed: 5,
		Aliases: map[string]int{"qtypes": 3, "queuetypes": 2},
		Clashes: []types.AliasClash{{Dir: "types", Name: "types", Both: 8, Importers: 20, Ours: true}},
	}, aliasFacts(t, r, "internal/queue/types"))
	assert.Equal(t, 0, aliasFacts(t, r, "internal/json").Needed, "an alias equal to the name is never needed")
	assert.Equal(t, []int{5, 4}, []int{aliasFacts(t, r, "lib/mcp").Aliased, aliasFacts(t, r, "lib/mcp").Needed},
		"internal/mcp/server.go is itself package mcp")
}

func TestImportAliasesWithoutAnIndex(t *testing.T) {
	r := NewGraph().ImportAliases()
	assert.False(t, r.Indexed)
	assert.NotNil(t, r.Packages)
	assert.Empty(t, r.Packages)
}

func TestAliasSamples(t *testing.T) {
	samples := aliasSamples(aliasGraph(), aliasOptions())

	assert.Equal(t, []types.ConformItem{
		{ID: "internal/json", Source: "internal/json/json.go:1", Measure: 0},
		{ID: "internal/queue/types", Source: "internal/queue/types/types.go:1", Measure: 38},
		{ID: "internal/spell/remote", Source: "internal/spell/remote/remote.go:1", Measure: 100},
		{ID: "lib/mcp", Source: "lib/mcp/mcp.go:1", Measure: 80},
		{ID: "types", Source: "types/types.go:1", Measure: 0},
	}, samples[couplingKey(types.ConformAliasShare)], "internal/ci is under MinSubject; internal/a/gen is generated")
	assert.Equal(t, []types.ConformItem{
		{ID: "internal/queue/types", Source: "internal/queue/types/types.go:1", Measure: 62},
	}, samples[couplingKey(types.ConformNameClash)], "the clash blames the lesser package only")
}

func TestConformAliasRows(t *testing.T) {
	g := aliasGraph()
	o := aliasOptions()

	out := g.Conform(o)
	share := conformRowFor(t, out, types.ConformAliasShare, types.ConformPopSource, "")
	assert.Equal(t, []string{"internal/spell/remote", "lib/mcp", "internal/queue/types"}, itemIDs(share.Violations))
	clash := conformRowFor(t, out, types.ConformNameClash, types.ConformPopSource, "")
	assert.Equal(t, []string{"internal/queue/types"}, itemIDs(clash.Violations))

	o.Ratchet = types.ConformRatchet{Schema: 1, Known: []types.ConformKnown{{
		Metric: types.ConformAliasShare, Items: []string{"internal/spell/remote", "internal/gone"}, Reason: "rename pending", Line: 3,
	}}}
	out = g.Conform(o)
	share = conformRowFor(t, out, types.ConformAliasShare, types.ConformPopSource, "")
	assert.Equal(t, 1, share.Known)
	assert.Equal(t, []string{"lib/mcp", "internal/queue/types"}, itemIDs(share.Violations))
	assert.Equal(t, []types.ConformKnown{{
		Metric: types.ConformAliasShare, Items: []string{"internal/gone"}, Reason: "rename pending", Line: 3,
	}}, out.Stale)
}

func itemIDs(items []types.ConformItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func TestAliasSamplesAreDeterministic(t *testing.T) {
	first := aliasSamples(aliasGraph(), aliasOptions())
	for range 10 {
		assert.Equal(t, first, aliasSamples(aliasGraph(), aliasOptions()))
	}
}
