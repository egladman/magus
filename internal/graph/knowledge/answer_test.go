package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// verdictCorpus spans every shape the grammar routes differently: bare text, each matcher
// operator, wildcards, and a kind on either side of the lazy layer. The invariants below
// are asserted over all of it rather than over a hand-picked case, because both bugs this
// file pins were a shape nobody thought to list.
var verdictCorpus = []string{
	"", "guard_shell.go", "kind=file guard_shell.go", "kind=symbol Foo", "kind=dir cmd/magus",
	"kind=target build", "kind=author eli", "kind=spell", "kind=file kind=symbol x",
	"kind=fil*", "kind=tar*", "kind=~^fi", "kind=~^tar", "id=~guard", "id=target:*",
	"language=go x", "project=pkg/a", "relation=defines", "relation=uses", "symbol:example.com/a Foo#",
}

// The safety property behind every `absent`: relevance is a strict SUPERSET of seeding.
// If it ever inverts, a query loads the lazy layer while being classified as one the layer
// could not have held - which is the state that lets a skipped shard set report a verified
// absence.
func TestSeedsLazyLayerImpliesCouldMatchLazyLayer(t *testing.T) {
	for _, in := range verdictCorpus {
		if SeedsLazyLayer(in) {
			assert.Truef(t, CouldMatchLazyLayer(in), "%q seeds the lazy layer, so it must be allowed to caveat it", in)
		}
	}
}

// The bug this file exists for, stated as a rule rather than as one query: a lookup that
// did not load a layer it could have matched must never claim it searched everywhere.
func TestAnswerNeverAbsentWhenARelevantLayerWasSkipped(t *testing.T) {
	for _, in := range verdictCorpus {
		// Gated on EITHER predicate, deliberately: keyed on CouldMatchLazyLayer alone, a
		// regression that shrank it below SeedsLazyLayer would silently drop the offending
		// query out of the loop and leave this test green while shipping the bug.
		if !SeedsLazyLayer(in) && !CouldMatchLazyLayer(in) {
			continue
		}
		ans := Answer(in, false, Coverage{Seeded: false, Probed: true})
		assert.Equalf(t, types.KnowledgeAnswer{Verdict: types.VerdictUnknown, Reason: types.ReasonSymbolsNotLoaded}, ans,
			"%q could match the lazy layer and did not load it", in)
	}
}

// `kind=file <name>` is the exact query that reported a verified absence about a node three
// other spellings retrieved. The kind names a layer the @symbols shards hold, so it must
// load them.
func TestSeedsLazyLayerForKindsItHolds(t *testing.T) {
	for _, in := range []string{
		"kind=file guard_shell.go", "kind:file", "kind=dir cmd/magus", "kind=fil*", "kind=~^fi",
		"kind=file kind=target x",
	} {
		assert.Truef(t, SeedsLazyLayer(in), "%q names a kind the @symbols shards hold", in)
	}
	for _, in := range []string{"kind=target build", "kind=author eli", "kind=spell go", "kind=tar*"} {
		assert.Falsef(t, SeedsLazyLayer(in), "%q names no kind the @symbols shards hold", in)
	}
}

// Why kind=file has to seed at all: a Go source file's node is minted BY the symbol shard,
// so it is unreachable from the default graph no matter how the query is phrased.
func TestGoFileNodeLivesOnlyInTheLazyShard(t *testing.T) {
	in := sampleInputs()
	in.Symbols = map[string][]types.KnowledgeSymbol{
		"pkg/a": {{Key: "example.com/foo Bar#", Label: "Bar", Language: "go", Source: "pkg/a/a.go:1", Defs: []string{"pkg/a/a.go"}}},
	}
	shards := AssembleShards(in)

	def, lazy := NewGraph(), NewGraph()
	for _, sh := range shards {
		if isSymbolsShard(sh.Name) || isCoverageShard(sh.Name) {
			lazy.Merge(sh.Nodes, sh.Edges)
			continue
		}
		def.Merge(sh.Nodes, sh.Edges)
	}

	q := "kind=file a.go"
	assert.Empty(t, matchIDs(def.Resolve(q, 0)), "the default graph cannot answer this")
	def.Merge(lazyNodes(lazy), nil)
	assert.Equal(t, []string{"file:pkg/a/a.go"}, matchIDs(def.Resolve(q, 0)), "the lazy shard is where the node is")
	assert.True(t, SeedsLazyLayer(q), "so the query has to load it")
}

// lazyNodes flattens a merged graph back to its nodes, for a test that loads one graph's
// layer into another.
func lazyNodes(g *Graph) []types.KnowledgeNode {
	out := make([]types.KnowledgeNode, 0, len(g.nodes))
	for id, n := range g.nodes {
		n.ID = id
		out = append(out, n)
	}
	return out
}

// A probe that did not run outranks every other reason: magus cannot report what a layer
// was missing when it could not establish what it had.
func TestAnswerFailedProbeOutranksTheRest(t *testing.T) {
	ans := Answer("Foo", false, Coverage{Seeded: false, Probed: false, Stale: []string{"."}, IndexOnly: true})
	assert.Equal(t, types.KnowledgeAnswer{
		Verdict: types.VerdictUnknown, Reason: types.ReasonCoverageUnknown, StaleIndexes: []string{"."},
	}, ans)
}

// A stale index downgrades only the verb whose whole evidence base IS the index, and only
// on a miss. Anything wider would make the verdict noise in an actively edited tree.
func TestAnswerIndexStaleOnlyForAnIndexOnlyMiss(t *testing.T) {
	stale := Coverage{Seeded: true, Probed: true, Stale: []string{"pkg/a"}}

	indexOnly := stale
	indexOnly.IndexOnly = true
	assert.Equal(t, types.KnowledgeAnswer{
		Verdict: types.VerdictUnknown, Reason: types.ReasonIndexStale, StaleIndexes: []string{"pkg/a"},
	}, Answer("Foo", false, indexOnly), "refs cannot verify a miss against an index older than the tree")

	assert.Equal(t, types.KnowledgeAnswer{Verdict: types.VerdictAbsent, StaleIndexes: []string{"pkg/a"}},
		Answer("Foo", false, stale), "a general query reads layers the index has no bearing on")
	assert.Equal(t, types.KnowledgeAnswer{Verdict: types.VerdictFound, StaleIndexes: []string{"pkg/a"}},
		Answer("Foo", true, indexOnly), "the sites it did return are still facts")
}

// The caveat has to ride the payload, not the console: -o json and MCP emitted a bare
// verdict where a human reading the same lookup was told the index was behind.
func TestAnswerCarriesStaleIndexesOnEveryVerdict(t *testing.T) {
	cov := Coverage{Seeded: true, Probed: true, Stale: []string{"pkg/a", "pkg/b"}}
	for _, matched := range []bool{true, false} {
		assert.Equal(t, []string{"pkg/a", "pkg/b"}, Answer("Foo", matched, cov).StaleIndexes)
	}
}

// A layer that could not have held the answer draws no caveat at all: pointing a
// `kind=author` miss at the symbol index sends the reader somewhere that was never in scope.
func TestAnswerIrrelevantLayerAssertsAbsence(t *testing.T) {
	ans := Answer("kind=author nobody", false, Coverage{Seeded: false, Stale: []string{"pkg/a"}})
	require.Equal(t, types.KnowledgeAnswer{Verdict: types.VerdictAbsent}, ans)
}
