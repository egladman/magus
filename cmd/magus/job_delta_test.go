package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
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

// The fork door and job apply hold one derived claim to one rule: a word naming exactly one
// declaration is kept, one naming none is refused alike, and a file no diff driver places
// is claimed by neither.
func TestForkAndApplyHoldDerivedClaimsAlike(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepo(t, map[string]string{
		".gitattributes": "*.go diff=golang\n",
		"run.go":         "package run\n\nfunc A(ctx int) {\n\ta()\n}\n\nfunc B(ctx int) {\n\tb()\n}\n",
		"notes.txt":      "A\n",
	}).Root()
	for _, tc := range []struct {
		name    string
		fact    job.SymbolFact
		paths   []string
		refused string
	}{
		{"a word naming one declaration", job.SymbolFact{DefinedIn: []string{"run.go"}, Signature: "func A(ctx int)"},
			[]string{"other.go", "run.go#func A"}, ""},
		{"a file no driver places", job.SymbolFact{DefinedIn: []string{"notes.txt"}, Signature: "func A()"},
			[]string{"other.go"}, ""},
		{"an index behind the tree", job.SymbolFact{DefinedIn: []string{"run.go"}, Signature: "func Gone()"},
			nil, `"run.go#func Gone" names no declaration of run.go`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			read := func(context.Context, string, string) (job.SymbolFact, bool) { return tc.fact, true }
			goals := []types.Goal{{ID: "moved", Kind: types.GoalKindSymbol, Expect: types.ExpectChanged, Symbols: []string{"A"}}}
			store := job.NewStore(job.Location{StateBase: t.TempDir(), CacheDir: t.TempDir(), Root: root, Actor: &job.Actor{}})

			forked, _, forkErr := resolveForkGoals(t.Context(), store, types.Job{ID: "wave/job", WritePaths: []string{"other.go"}, Goals: goals}, read)
			plan, applyErr := job.Apply(t.Context(), store, []types.Declaration{{ID: "wave/job", WritePaths: []string{"other.go"},
				Check: &types.LeaseCheck{Target: "test", Project: "."}, Goals: goals}}, config.Jobs{}, read, nil, true)

			if tc.refused != "" {
				require.Error(t, forkErr)
				require.Error(t, applyErr)
				assert.Equal(t, forkErr.Error(), applyErr.Error(), "the two doors refuse with one answer")
				assert.Contains(t, forkErr.Error(), tc.refused)
				return
			}
			require.NoError(t, forkErr)
			require.NoError(t, applyErr)
			assert.Equal(t, tc.paths, forked.WritePaths)
			assert.Equal(t, tc.paths, plan[0].Next.WritePaths)
		})
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
