package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// verdictCases spans every shape the grammar routes differently: bare text, each matcher
// operator, wildcards, and a kind on either side of the lazy layer. The invariants below
// are asserted over all of it rather than over a hand-picked case, because both bugs this
// file pins were a shape nobody thought to list.
var verdictCases = []string{
	"", "guard_shell.go", "kind=file guard_shell.go", "kind=symbol Foo", "kind=dir cmd/magus",
	"kind=target build", "kind=author eli", "kind=spell", "kind=file kind=symbol x",
	"kind=fil*", "kind=tar*", "kind=~^fi", "kind=~^tar", "id=~guard", "id=target:*",
	"language=go x", "project=pkg/a", "relation=defines", "relation=uses", "symbol:example.com/a Foo#",
}

// The safety property behind every `absent`: relevance is a strict SUPERSET of seeding.
// If it ever inverts, a query loads the lazy layer while being classified as one the layer
// could not have held, which is the state that lets a skipped shard set report a verified
// absence.
func TestSeedsLazyLayerImpliesCouldMatchLazyLayer(t *testing.T) {
	for _, in := range verdictCases {
		if SeedsLazyLayer(in) {
			assert.Truef(t, CouldMatchLazyLayer(in), "%q seeds the lazy layer, so it must be allowed to caveat it", in)
		}
	}
}

// The bug this file exists for, stated as a rule rather than as one query: a lookup that
// did not load a layer it could have matched must never claim it searched everywhere.
func TestAnswerNeverAbsentWhenARelevantLayerWasSkipped(t *testing.T) {
	for _, in := range verdictCases {
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
	root := []types.KnowledgeStaleIndex{{Project: ".", Language: "go", Op: "scip"}}
	ans := Answer("Foo", false, Coverage{Seeded: false, Probed: false, Stale: root})
	assert.Equal(t, types.KnowledgeAnswer{
		Verdict: types.VerdictUnknown, Reason: types.ReasonCoverageUnknown, StaleIndexes: []string{"."}, StaleIndexDetails: root,
	}, ans)
}

// A stale index downgrades every miss and no hit. `absent` claims magus searched
// everything it could reach, which a lookup answered from an index magus knows is behind
// cannot say; query used to print that claim directly under its own "stale index" line.
func TestAnswerIndexStaleDowngradesEveryMiss(t *testing.T) {
	pkgA := []types.KnowledgeStaleIndex{{Project: "pkg/a"}}
	stale := Coverage{Seeded: true, Probed: true, Stale: pkgA}

	assert.Equal(t, types.KnowledgeAnswer{
		Verdict: types.VerdictUnknown, Reason: types.ReasonIndexStale, StaleIndexes: []string{"pkg/a"}, StaleIndexDetails: pkgA,
	}, Answer("Foo", false, stale), "a miss against an index older than the tree is not a verified absence")

	assert.Equal(t, types.KnowledgeAnswer{Verdict: types.VerdictFound, StaleIndexes: []string{"pkg/a"}, StaleIndexDetails: pkgA},
		Answer("Foo", true, stale), "the sites it did return are still facts")

	assert.Equal(t, types.KnowledgeAnswer{Verdict: types.VerdictAbsent},
		Answer("Foo", false, Coverage{Seeded: true, Probed: true}),
		"a current index still verifies an absence")
}

// The caveat has to ride the payload, not the console: -o json and MCP emitted a bare
// verdict where a human reading the same lookup was told the index was behind.
func TestAnswerCarriesStaleIndexesOnEveryVerdict(t *testing.T) {
	cov := Coverage{Seeded: true, Probed: true, Stale: []types.KnowledgeStaleIndex{
		{Project: "pkg/a", Language: "buzz", Op: "scip-buzz"}, {Project: "pkg/a", Language: "go", Op: "scip"}, {Project: "pkg/b", Language: "go", Op: "scip"},
	}}
	for _, matched := range []bool{true, false} {
		ans := Answer("Foo", matched, cov)
		assert.Equal(t, []string{"pkg/a", "pkg/b"}, ans.StaleIndexes, "each project once")
		assert.Equal(t, cov.Stale, ans.StaleIndexDetails, "and each of its indexes with its language")
	}
}

// A lookup that resolved to one symbol is qualified only by the indexes that could hold
// it: a Go symbol's sites are in no Buzz index. A lookup with no language to narrow by keeps
// every caveat, and an index whose language is unknown could hold anything.
func TestCoverageForNarrowsToTheSymbolsLanguage(t *testing.T) {
	staleGo := types.KnowledgeStaleIndex{Project: ".", Language: "go", Op: "scip"}
	staleBuzz := types.KnowledgeStaleIndex{Project: ".", Language: "buzz", Op: "scip-buzz"}
	staleAny := types.KnowledgeStaleIndex{Project: "libs/x"}
	missingBuzz := types.KnowledgeSymbolGap{Project: types.NewProjectRef(".", ""), Language: "buzz", State: types.SymbolIndexNotBuilt}
	missingAny := types.KnowledgeSymbolGap{Project: types.NewProjectRef("docs", ""), State: types.SymbolIndexNotBuilt}
	cov := Coverage{
		Seeded: true, Probed: true,
		Gaps:  []types.KnowledgeSymbolGap{missingBuzz, missingAny},
		Stale: []types.KnowledgeStaleIndex{staleBuzz, staleGo, staleAny},
	}

	assert.Equal(t, Coverage{
		Seeded: true, Probed: true,
		Gaps:  []types.KnowledgeSymbolGap{missingAny},
		Stale: []types.KnowledgeStaleIndex{staleGo, staleAny},
	}, cov.For("go"))
	assert.Equal(t, Coverage{
		Seeded: true, Probed: true,
		Gaps:  []types.KnowledgeSymbolGap{missingBuzz, missingAny},
		Stale: []types.KnowledgeStaleIndex{staleBuzz, staleAny},
	}, cov.For("Buzz"), "languages compare without case")
	assert.Equal(t, cov, cov.For(""), "no single symbol, no narrowing")

	assert.Equal(t, types.KnowledgeAnswer{Verdict: types.VerdictAbsent},
		Answer("probeTimeoutFrom", false, Coverage{Seeded: true, Probed: true, Stale: []types.KnowledgeStaleIndex{staleBuzz}}.For("go")),
		"a miss beside only another language's stale index is a verified absence")
}

// The language a lookup is narrowed by is the one the graph recorded for the symbol: its
// node's, else its defining file's, and none for a ref that names no symbol.
func TestGraphSymbolLanguage(t *testing.T) {
	g := NewGraph()
	defines := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}
	}
	g.Merge([]types.KnowledgeNode{
		{ID: "symbol:go a/Run().", Kind: types.KindSymbol, Label: "Run", Attrs: map[string]string{types.AttrLanguage: "go"}},
		{ID: "symbol:buzz a/probe().", Kind: types.KindSymbol, Label: "probe"},
		{ID: "file:spell.buzz", Kind: types.KindFile, Label: "spell.buzz", Attrs: map[string]string{types.AttrLanguage: "buzz"}},
		{ID: "symbol:x a/bare().", Kind: types.KindSymbol, Label: "bare"},
	}, []types.KnowledgeEdge{defines("a.go", "symbol:go a/Run()."), defines("spell.buzz", "symbol:buzz a/probe()."), defines("bare", "symbol:x a/bare().")})

	assert.Equal(t, "go", g.SymbolLanguage("symbol:go a/Run()."))
	assert.Equal(t, "buzz", g.SymbolLanguage("symbol:buzz a/probe()."), "the defining file's language when the node has none")
	assert.Empty(t, g.SymbolLanguage("symbol:x a/bare()."), "neither says")
	assert.Empty(t, g.SymbolLanguage("symbol:nothing here()."))
}

// A layer that could not have held the answer draws no caveat at all: pointing a
// `kind=author` miss at the symbol index sends the reader somewhere that was never in scope.
func TestAnswerIrrelevantLayerAssertsAbsence(t *testing.T) {
	ans := Answer("kind=author nobody", false, Coverage{Seeded: false, Stale: []types.KnowledgeStaleIndex{{Project: "pkg/a"}}})
	require.Equal(t, types.KnowledgeAnswer{Verdict: types.VerdictAbsent}, ans)
}
