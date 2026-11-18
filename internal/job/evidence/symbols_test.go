package evidence

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Explain resolves a bare name fuzzily, which is right for a person typing
// `magus explain build` and wrong for evidence: asked for "cmd/magus" it once answered
// target:.:release-sign, and a blast radius from an unrelated node is worse than silence.
func TestLeaseGraphEvidenceTakesOnlyAnExactNode(t *testing.T) {
	t.Parallel()

	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "dir:cmd/magus", Kind: types.KindDir, Label: "cmd/magus"})
	g.AddNode(types.KnowledgeNode{ID: "file:internal/ledger/brief.go", Kind: types.KindFile, Label: "brief.go"})

	got, ok := pathEvidence(g, "cmd/magus")
	require.True(t, ok)
	assert.Equal(t, job.TermsEvidence{Path: "cmd/magus", Node: "dir:cmd/magus"}, got)
	// "brief.go" resolves to file:internal/ledger/brief.go by name. It is a match and not
	// evidence: the lease declared a path at the workspace root, and this node is not it.
	_, ok = pathEvidence(g, "brief.go")
	assert.False(t, ok, "a fuzzy match is not evidence")
}

// describe reads the graph evidence as it reads the symbol postconditions: from the graph as built,
// and from none while a symbol index is stale, which it reports cold.
func TestLeaseGraphEvidenceBuildsNothingOverAStaleIndex(t *testing.T) {
	t.Parallel()

	paths := []string{"code.go"}
	evidence := func(rebuild bool, stale []types.KnowledgeStaleIndex) (cold bool, loads int) {
		graph := &Graph{
			rebuild: rebuild,
			stale:   func(context.Context) []types.KnowledgeStaleIndex { return stale },
			domain: func(context.Context) (*knowledge.Graph, error) {
				loads++
				if !rebuild && len(stale) > 0 {
					t.Error("describe built the graph over a stale index")
				}
				return deltaGraph(nil), nil
			},
		}
		_, cold = LeaseGraphEvidence(t.Context(), graph, paths)
		_, again := LeaseGraphEvidence(t.Context(), graph, paths)
		assert.Equal(t, cold, again, "a second ask answers alike")
		return cold, loads
	}

	cold, loads := evidence(false, nil)
	assert.False(t, cold)
	assert.Equal(t, 1, loads, "one load serves every ask")

	stale := []types.KnowledgeStaleIndex{{Project: "pkg", Language: "go"}}
	cold, loads = evidence(false, stale)
	assert.True(t, cold, "a stale index is reported, not read as nothing depending on the paths")
	assert.Zero(t, loads)

	cold, loads = evidence(true, stale)
	assert.False(t, cold)
	assert.Equal(t, 1, loads)
}

func TestCallersSymbolRefusesAnAmbiguousName(t *testing.T) {
	const a, b = "symbol:x `a`/Parse().", "symbol:x `b`/Parse()."
	defines := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}
	}
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{{ID: a, Kind: types.KindSymbol, Label: "Parse"}, {ID: b, Kind: types.KindSymbol, Label: "Parse"}},
		[]types.KnowledgeEdge{defines("a/a.go", a), defines("b/b.go", b)})
	pick := func(_ *knowledge.Graph, name string) (string, []string) {
		if name == "Parse" {
			return "", []string{a, b}
		}
		return fmt.Sprintf("symbol:x `c`/%s().", name), nil
	}

	_, err := callersSymbol(g, "Parse", pick)
	require.Error(t, err)
	assert.ErrorContains(t, err, a)
	assert.ErrorContains(t, err, b)

	_, err = callersSymbol(g, "Missing", pick)
	assert.ErrorContains(t, err, `callers "Missing" names no symbol the index holds`)
}

func TestReadSymbolDelta(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepository(t, map[string]string{
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

	delta, ok := ReadSymbolDelta(t.Context(), repo.Root(), g, res.VCS, base, []string{"api.go"})
	require.True(t, ok)
	assert.Equal(t, job.SymbolDelta{
		Added: []string{"Added"},
		Left:  []string{"helper (util.go:3)"},
	}, delta, "an unexported addition is no export, and a name the diff still holds is not left behind")

	none, ok := ReadSymbolDelta(t.Context(), repo.Root(), g, res.VCS, base, nil)
	assert.True(t, ok)
	assert.Equal(t, job.SymbolDelta{}, none, "an empty diff is read, and holds nothing")

	for name, read := range map[string]func() (job.SymbolDelta, bool){
		"no graph": func() (job.SymbolDelta, bool) {
			return ReadSymbolDelta(t.Context(), repo.Root(), nil, res.VCS, base, []string{"api.go"})
		},
		"no vcs": func() (job.SymbolDelta, bool) {
			return ReadSymbolDelta(t.Context(), repo.Root(), g, nil, base, []string{"api.go"})
		},
		"no base": func() (job.SymbolDelta, bool) {
			return ReadSymbolDelta(t.Context(), repo.Root(), g, res.VCS, "", []string{"api.go"})
		},
		"no symbols": func() (job.SymbolDelta, bool) {
			return ReadSymbolDelta(t.Context(), repo.Root(), knowledge.NewGraph(), res.VCS, base, []string{"api.go"})
		},
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
		{"symbol:go a `github.com/x/types`/Postcondition#Signature.", "Postcondition#Signature", true},
		{"symbol:go a `a`/ResolveSymbolPostconditions().", "ResolveSymbolPostconditions", true},
		{"symbol:go a `a`/postcondition#Signature.", "", false},
		{"symbol:go a `a`/Postcondition#Resolve().(ctx)", "", false},
	} {
		name, exported := goExport(tc.id)
		assert.Equal(t, fmt.Sprint(tc.name, tc.exported), fmt.Sprint(name, exported), tc.id)
	}
}
