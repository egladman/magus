package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// deltaGraph defines each symbol name at file:line, with nothing referencing any of them.
func deltaGraph(defs map[string]string) *knowledge.Graph {
	var nodes []types.KnowledgeNode
	var edges []types.KnowledgeEdge
	for name, at := range defs {
		id := "symbol:go a `a`/" + name + "()."
		file := at[:len(at)-2]
		nodes = append(nodes, types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: name, Source: at})
		edges = append(edges, types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines,
			Confidence: types.ConfidenceExtracted, Score: 1})
	}
	g := knowledge.NewGraph()
	g.Merge(nodes, edges)
	return g
}

func TestReadSymbolDelta(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, map[string]string{
		"api.go":  "package a\n\nfunc Old() {}\n\nfunc Keep() { helper() }\n",
		"util.go": "package a\n\nfunc helper() {}\n",
	})
	base := repo.Head()
	repo.Write("api.go", "package a\n\nfunc Old() {}\n\nfunc Keep() {}\n\nfunc Added() {}\n\nfunc lower() {}\n")
	res, err := vcs.Resolve(t.Context(), repo.Root(), "", types.VCSOptions{})
	require.NoError(t, err)
	g := deltaGraph(map[string]string{
		"Old": "api.go:3", "Keep": "api.go:5", "Added": "api.go:7", "lower": "api.go:9", "helper": "util.go:3",
	})

	delta, ok := readSymbolDelta(t.Context(), repo.Root(), g, res.VCS, base, []string{"api.go"})
	require.True(t, ok)
	assert.Equal(t, symbolDelta{
		Added: []string{"Added"},
		Left:  []string{"helper (util.go:3)"},
	}, delta, "an unexported addition is no export, and a name the diff still holds is not left behind")

	none, ok := readSymbolDelta(t.Context(), repo.Root(), g, res.VCS, base, nil)
	assert.True(t, ok)
	assert.Equal(t, symbolDelta{}, none, "an empty diff is read, and holds nothing")

	for name, read := range map[string]func() (symbolDelta, bool){
		"no graph":   func() (symbolDelta, bool) { return readSymbolDelta(t.Context(), repo.Root(), nil, res.VCS, base, []string{"api.go"}) },
		"no vcs":     func() (symbolDelta, bool) { return readSymbolDelta(t.Context(), repo.Root(), g, nil, base, []string{"api.go"}) },
		"no base":    func() (symbolDelta, bool) { return readSymbolDelta(t.Context(), repo.Root(), g, res.VCS, "", []string{"api.go"}) },
		"no symbols": func() (symbolDelta, bool) { return readSymbolDelta(t.Context(), repo.Root(), knowledge.NewGraph(), res.VCS, base, []string{"api.go"}) },
	} {
		_, ok := read()
		assert.False(t, ok, "%s: a delta nobody could read is unknown, not empty", name)
	}
}

func TestGoExport(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		id, name string
		exported bool
	}{
		{"symbol:go a `github.com/x/types`/Goal#Signature.", "Goal#Signature", true},
		{"symbol:go a `a`/ResolveSymbolGoals().", "ResolveSymbolGoals", true},
		{"symbol:go a `a`/goal#Signature.", "", false},
		{"symbol:go a `a`/Goal#Resolve().(ctx)", "", false},
	} {
		name, exported := goExport(tc.id)
		assert.Equal(t, fmt.Sprint(tc.name, tc.exported), fmt.Sprint(name, exported), tc.id)
	}
}
