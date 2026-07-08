package knowledge

import (
	"testing"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleGraph() *Graph { return mergeAll(AssembleShards(sampleInputs())) }

func matchIDs(ms []types.KnowledgeMatch) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestParseQuery(t *testing.T) {
	q := parseQuery(`kind:spell project:pkg/foo build -kind:op -legacy`)
	assert.Equal(t, []string{"build"}, q.terms)
	assert.Equal(t, []string{"legacy"}, q.negTerms)
	assert.Equal(t, []string{"spell"}, q.fields["kind"])
	assert.Equal(t, []string{"pkg/foo"}, q.fields["project"])
	assert.Equal(t, []string{"op"}, q.negFields["kind"])
}

func TestParseQueryPhrase(t *testing.T) {
	q := parseQuery(`"two words" solo`)
	assert.Equal(t, []string{"two words", "solo"}, q.terms)
}

func TestResolveByKind(t *testing.T) {
	ids := matchIDs(sampleGraph().Resolve("kind:spell", 0))
	assert.Equal(t, []string{"spell:go"}, ids)
}

func TestResolveByTerm(t *testing.T) {
	ids := matchIDs(sampleGraph().Resolve("build", 0))
	assert.Contains(t, ids, "target:pkg/a:build")
	assert.Contains(t, ids, "target:pkg/b:build")
	assert.NotContains(t, ids, "target:pkg/a:gen")
}

func TestResolveByProject(t *testing.T) {
	ids := matchIDs(sampleGraph().Resolve("project:pkg/a", 0))
	assert.ElementsMatch(t, []string{"project:pkg/a", "target:pkg/a:build", "target:pkg/a:gen"}, ids)
}

func TestResolveNegationExcludesKind(t *testing.T) {
	// "go" matches both spell:go and op:go:* nodes; -kind:op drops the ops.
	ids := matchIDs(sampleGraph().Resolve("go -kind:op", 0))
	assert.Contains(t, ids, "spell:go")
	for _, id := range ids {
		assert.NotContains(t, id, "op:")
	}
}

func TestResolveLimit(t *testing.T) {
	assert.Len(t, sampleGraph().Resolve("kind:target", 2), 2)
}

func TestExplainByID(t *testing.T) {
	out, ok := sampleGraph().Explain("target:pkg/a:build")
	require.True(t, ok)
	assert.Equal(t, types.KindTarget, out.Node.Kind)

	outRel := func(edges []types.KnowledgeEdgeRef, rel, other string) bool {
		for _, e := range edges {
			if e.Relation == rel && e.Other == other {
				return true
			}
		}
		return false
	}
	assert.True(t, outRel(out.Out, types.RelationDependsOn, "target:pkg/a:gen"), "out depends_on gen")
	assert.True(t, outRel(out.Out, types.RelationUses, "op:go:go-build"), "out uses op")
	assert.True(t, outRel(out.In, types.RelationContains, "project:pkg/a"), "in contains from project")
	assert.True(t, outRel(out.In, types.RelationReferences, "charm:rw"), "in referenced by charm")
	assert.GreaterOrEqual(t, out.BlastRadius, 2)
}

func TestExplainResolvesByName(t *testing.T) {
	out, ok := sampleGraph().Explain("gen")
	require.True(t, ok)
	assert.Equal(t, "target:pkg/a:gen", out.Node.ID)
}

func TestExplainUnknown(t *testing.T) {
	_, ok := sampleGraph().Explain("nonesuch-xyz")
	assert.False(t, ok)
}

func TestPathConnects(t *testing.T) {
	out, ok := sampleGraph().Path("charm:rw", "target:pkg/a:gen")
	require.True(t, ok)
	require.True(t, out.Found)
	// charm:rw --references--> build --depends_on--> gen
	require.Len(t, out.Steps, 2)
	assert.Equal(t, "charm:rw", out.Steps[0].From)
	assert.Equal(t, "target:pkg/a:gen", out.Steps[len(out.Steps)-1].To)
}

func TestPathSameNode(t *testing.T) {
	out, ok := sampleGraph().Path("target:pkg/a:build", "target:pkg/a:build")
	require.True(t, ok)
	assert.True(t, out.Found)
	assert.Empty(t, out.Steps)
}

func TestPathUnresolved(t *testing.T) {
	_, ok := sampleGraph().Path("target:pkg/a:build", "nonesuch-xyz")
	assert.False(t, ok)
}

func TestQueryNeighborhoodRespectsBudget(t *testing.T) {
	out := sampleGraph().Query("build", 3)
	assert.LessOrEqual(t, len(out.Nodes), 3)
	assert.Positive(t, out.MatchCount)
}

func TestQueryDeterministic(t *testing.T) {
	a, _ := codec.Marshal(sampleGraph().Query("kind:target", 50))
	b, _ := codec.Marshal(sampleGraph().Query("kind:target", 50))
	assert.Equal(t, string(a), string(b))
}

func TestSelectNeighborhoodExport(t *testing.T) {
	g := sampleGraph()
	full := g.Output()
	sub := g.Select("build", 5)
	// The selected subgraph is a real neighborhood: non-empty but smaller than
	// the whole graph, and its counts agree with the node/link slices.
	assert.Positive(t, sub.NodeCount)
	assert.Less(t, sub.NodeCount, full.NodeCount)
	assert.Equal(t, sub.NodeCount, len(sub.Nodes))
	assert.Equal(t, sub.EdgeCount, len(sub.Links))
}

func TestSelectRespectsBudget(t *testing.T) {
	assert.LessOrEqual(t, sampleGraph().Select("build", 3).NodeCount, 3)
}

func TestSelectNoMatchEmpty(t *testing.T) {
	sub := sampleGraph().Select("zzznotarealnode", 50)
	assert.Zero(t, sub.NodeCount)
	assert.Empty(t, sub.Nodes)
	assert.Empty(t, sub.Links)
}
