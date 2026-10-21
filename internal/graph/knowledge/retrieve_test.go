package knowledge

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/textrank"
	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/types"
)

func retrieveIDs(ms []types.KnowledgeMatch) []string {
	ids := make([]string, len(ms))
	for i, m := range ms {
		ids[i] = m.ID
	}
	return ids
}

// The package's tests never read the developer's cached table, which would move every
// ranking they pin once `magus graph pull -table` ran.
func init() { cachedTable = noTable }

func noTable() (*embed.Table, error) {
	return nil, fmt.Errorf("%w: the test binds none", embedtable.ErrAbsent)
}

func shipped() RetrieveOptions { return RetrieveOptions{Weights: ShippedWeights(), Table: noTable} }

var noTableSkipped = listEmbed + ": embedtable: no embedding table in the cache: the test binds none: run `" + pullTable + "` to replace it"

// fixtureTable holds parse and config on axis 0 and yaml on axis 1, so a text of parse
// and config points where the query parse does and one of parse and yaml at 45 degrees.
func fixtureTable(t *testing.T) func() (*embed.Table, error) {
	t.Helper()
	row := func(axis int) []int8 {
		r := make([]int8, 64)
		r[axis] = 127
		return r
	}
	var buf bytes.Buffer
	require.NoError(t, embed.EncodeTable(&buf, embed.TableSpec{
		Tokens:  []string{"config", "parse", "yaml"},
		Weights: []uint16{1, 1, 1},
		Rows:    [][]int8{row(0), row(0), row(1)},
		Prov: embed.Provenance{Teacher: "fixture", TeacherRevision: "1", TextsDigest: "t", PairsDigest: "p",
			Pipeline: "fixture", Run: "fixture", GOARCH: "any"},
	}))
	table, err := embed.ReadTable(buf.Bytes())
	require.NoError(t, err)
	return func() (*embed.Table, error) { return table, nil }
}

// embedFixture holds six nodes that score the same for "parse", so lexical order is id
// order and symbol:z comes last, and only symbol:z's doc points where parse does.
func embedFixture() *Graph {
	g := NewGraph()
	for _, id := range []string{"symbol:a1", "symbol:a2", "symbol:a3", "symbol:a4", "symbol:a5"} {
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: "parse", Doc: "yaml"})
	}
	g.AddNode(types.KnowledgeNode{ID: "symbol:z", Kind: types.KindSymbol, Label: "parse", Doc: "config"})
	return g
}

func TestRetrieveWhyNamesTheEmbedList(t *testing.T) {
	opts := shipped()
	opts.Table = fixtureTable(t)
	ms, ranking, err := embedFixture().Retrieve("parse", opts)
	require.NoError(t, err)
	assert.Equal(t, []string{listGraph + ": no reader path names a node"}, ranking.Skipped)
	z := ms[slices.Index(retrieveIDs(ms), "symbol:z")]
	assert.Equal(t, []types.KnowledgeListRank{{List: listLexical, Rank: 6}, {List: listExact, Rank: 6}, {List: listEmbed, Rank: 1}}, z.Why.Lists)
}

func TestRetrieveEmbedListMovesTheRanking(t *testing.T) {
	g := embedFixture()
	cold, _, err := g.Retrieve("parse", shipped())
	require.NoError(t, err)
	require.Equal(t, "symbol:z", cold[len(cold)-1].ID)

	opts := shipped()
	opts.Table = fixtureTable(t)
	warm, _, err := g.Retrieve("parse", opts)
	require.NoError(t, err)
	assert.Less(t, slices.Index(retrieveIDs(warm), "symbol:z"), len(warm)-1, "the match the query's embedding points at rises")
	assert.ElementsMatch(t, retrieveIDs(cold), retrieveIDs(warm), "the embed list reorders; it adds no match")
}

func TestRetrieveSkipsTheEmbedListWithNoTable(t *testing.T) {
	opts := shipped()
	opts.Table = func() (*embed.Table, error) {
		return embedtable.Load(t.Context(), embedtable.Options{Dir: t.TempDir()})
	}
	ms, ranking, err := embedFixture().Retrieve("parse", opts)
	require.NoError(t, err)
	require.Len(t, ranking.Skipped, 2)
	assert.True(t, strings.HasPrefix(ranking.Skipped[1], listEmbed+": embedtable: no embedding table in the cache"), ranking.Skipped[1])
	assert.Contains(t, ranking.Skipped[1], "run `"+pullTable+"`")
	assert.Equal(t, []string{"symbol:a1", "symbol:a2", "symbol:a3", "symbol:a4", "symbol:a5", "symbol:z"}, retrieveIDs(ms), "the lexical and exact lists still rank")
	assert.Equal(t, []types.KnowledgeListRank{{List: listLexical, Rank: 1}, {List: listExact, Rank: 1}}, ms[0].Why.Lists)
}

func TestRetrieveSkipsATableThatIsNotThePin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, embedtable.FileName), []byte("not the pinned table"), 0o600))
	opts := shipped()
	opts.Table = func() (*embed.Table, error) {
		return embedtable.Load(t.Context(), embedtable.Options{Dir: dir})
	}
	ms, ranking, err := embedFixture().Retrieve("parse", opts)
	require.NoError(t, err)
	require.Len(t, ranking.Skipped, 2)
	assert.Contains(t, ranking.Skipped[1], "hashes to")
	assert.Contains(t, ranking.Skipped[1], "run `"+pullTable+"` to replace it")
	assert.Len(t, ms, 6)
}

// A partial match that scores higher outranks a full one, and both say how many of the
// query's tokens they hold: ranking matched terms first lost to score order everywhere it
// was measured.
func TestRetrieveRanksPartialMatchesByScore(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "symbol:x", Kind: types.KindSymbol, Label: "parse"})
	g.AddNode(types.KnowledgeNode{ID: "doc:y.md", Kind: types.KindDoc, Label: "y", Doc: "how to parse a config file and what the loader does after"})
	for _, id := range []string{"doc:f1.md", "doc:f2.md", "doc:f3.md"} {
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindDoc, Label: "f", Doc: "config"})
	}

	ms, _, err := g.Retrieve("parse config", shipped())
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(ms), 2)
	assert.Equal(t, []string{"symbol:x", "doc:y.md"}, retrieveIDs(ms[:2]))
	require.Len(t, ms[0].Why.Terms, 1)
	assert.Equal(t, &types.KnowledgeMatchWhy{
		Matched: 1,
		Of:      2,
		Terms:   []types.KnowledgeTermScore{{Term: "parse", Field: textrank.FieldName, Score: ms[0].Why.Terms[0].Score}},
		Lists:   []types.KnowledgeListRank{{List: listLexical, Rank: 1}},
		Prior:   1,
	}, ms[0].Why)
	assert.Equal(t, 2, ms[1].Why.Matched, "the full match is labeled, not dropped")
	assert.Greater(t, ms[0].Score, ms[1].Score)
}

// A stored index carries section bodies, which the graph's nodes do not, and every
// index reaches a symbol by a word of its signature.
func TestRetrieveReachesSymbolsAndBodies(t *testing.T) {
	sym := types.KnowledgeNode{ID: "symbol:pkg Frobnicate().", Kind: types.KindSymbol, Label: "Frobnicate", Source: "pkg/f.go:3",
		Attrs: map[string]string{AttrSignature: "func Frobnicate(w Widget) error"}}
	sec := types.KnowledgeNode{ID: "docsection:docs/a.md#setup", Kind: types.KindDocSection, Label: "Setup", Source: "docs/a.md"}
	g := NewGraph()
	g.AddNode(sym)
	g.AddNode(sec)

	ms, _, err := g.Retrieve("widget", shipped())
	require.NoError(t, err)
	assert.Equal(t, []string{sym.ID}, retrieveIDs(ms))

	ms, _, err = g.Retrieve("zanzibar", shipped())
	require.NoError(t, err)
	assert.Empty(t, ms, "the in-memory index holds no bodies")

	opts := shipped()
	opts.Lexical = textrank.NewIndex([]textrank.Document{lexicalDocument(sym, ""), lexicalDocument(sec, "Install the zanzibar toolchain first.")})
	ms, _, err = g.Retrieve("zanzibar", opts)
	require.NoError(t, err)
	assert.Equal(t, []string{sec.ID}, retrieveIDs(ms))
}

func TestRetrieveIsDeterministic(t *testing.T) {
	nodes := []types.KnowledgeNode{
		{ID: "target:a:build", Kind: types.KindTarget, Label: "build"},
		{ID: "target:b:build", Kind: types.KindTarget, Label: "build"},
		{ID: "doc:build.md", Kind: types.KindDoc, Label: "build", Doc: "build the build"},
		{ID: "symbol:p Build().", Kind: types.KindSymbol, Label: "Build"},
	}
	forward, backward := NewGraph(), NewGraph()
	for i := range nodes {
		forward.AddNode(nodes[i])
		backward.AddNode(nodes[len(nodes)-1-i])
	}
	first, rankA, err := forward.Retrieve("build", shipped())
	require.NoError(t, err)
	again, _, err := forward.Retrieve("build", shipped())
	require.NoError(t, err)
	other, rankB, err := backward.Retrieve("build", shipped())
	require.NoError(t, err)
	assert.Equal(t, first, again)
	assert.Equal(t, first, other)
	assert.Equal(t, rankA, rankB)
	ids := retrieveIDs(first)
	a, b := slices.Index(ids, "target:a:build"), slices.Index(ids, "target:b:build")
	assert.Equal(t, a+1, b, "equal scores break ties by id")
}

// readerFixture holds six nodes that score the same for "parse", so lexical order is id
// order and symbol:z comes last, and two files a reader can name that reference z.
func readerFixture() *Graph {
	g := NewGraph()
	for _, id := range []string{"symbol:a1", "symbol:a2", "symbol:a3", "symbol:a4", "symbol:a5", "symbol:z"} {
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: "parse"})
	}
	for _, p := range readerPaths {
		g.AddNode(types.KnowledgeNode{ID: fileID(p), Kind: types.KindFile, Label: p, Source: p})
		g.AddEdge(extractedEdge(fileID(p), "symbol:z", types.RelationReferences, p+":4"))
	}
	return g
}

var readerPaths = []string{"pkg/x.go", "pkg/y.go"}

func TestRetrieveReaderContextLiftsNeighbors(t *testing.T) {
	g := readerFixture()
	cold, _, err := g.Retrieve("parse", shipped())
	require.NoError(t, err)
	require.Equal(t, "symbol:z", cold[len(cold)-1].ID)

	opts := shipped()
	opts.Reader = readerPaths
	warm, ranking, err := g.Retrieve("parse", opts)
	require.NoError(t, err)
	assert.Less(t, slices.Index(retrieveIDs(warm), "symbol:z"), len(warm)-1, "the reader's neighbor rises")
	assert.ElementsMatch(t, retrieveIDs(cold), retrieveIDs(warm), "reader context reorders; it adds no match")
	assert.Equal(t, readerPaths, ranking.Reader)
}

func TestRetrieveSaysWhenTheLexicalIndexIsMissing(t *testing.T) {
	g := readerFixture()
	_, ranking, err := g.Retrieve("parse", shipped())
	require.NoError(t, err)
	assert.Contains(t, ranking.Note, "no stored lexical index")
	assert.Equal(t, ShippedWeights().Digest(), ranking.Weights)

	opts := shipped()
	opts.Lexical = g.ensureSearchIndex().lexicalIndex()
	_, ranking, err = g.Retrieve("parse", opts)
	require.NoError(t, err)
	assert.Empty(t, ranking.Note)
}

func TestRetrieveWhyNamesEveryList(t *testing.T) {
	g := readerFixture()
	opts := shipped()
	opts.Reader = readerPaths
	ms, ranking, err := g.Retrieve("parse", opts)
	require.NoError(t, err)
	assert.Equal(t, []string{noTableSkipped}, ranking.Skipped)
	z := ms[slices.Index(retrieveIDs(ms), "symbol:z")]
	why := *z.Why
	why.Seeds = slices.Clone(why.Seeds)
	slices.Sort(why.Seeds)
	require.Len(t, why.Terms, 1)
	assert.Equal(t, types.KnowledgeMatchWhy{
		Matched: 1,
		Of:      1,
		Terms:   []types.KnowledgeTermScore{{Term: "parse", Field: textrank.FieldName, Score: why.Terms[0].Score}},
		Lists:   []types.KnowledgeListRank{{List: listLexical, Rank: 6}, {List: listExact, Rank: 6}, {List: listGraph, Rank: 1}},
		Seeds:   []string{fileID("pkg/x.go"), fileID("pkg/y.go"), "symbol:z"},
		Prior:   1,
	}, why)

	ms, ranking, err = g.Retrieve("parse", shipped())
	require.NoError(t, err)
	assert.Equal(t, []types.KnowledgeListRank{{List: listLexical, Rank: 1}, {List: listExact, Rank: 1}}, ms[0].Why.Lists)
	assert.Equal(t, []string{listGraph + ": no reader path names a node", noTableSkipped}, ranking.Skipped)

	ms, _, err = g.Retrieve("parse x.go", shipped())
	require.NoError(t, err)
	assert.Equal(t, []types.KnowledgeListRank{{List: listLexical, Rank: 1}}, ms[0].Why.Lists, "no node is named that")
}

// A node whose doc repeats a word must not outrank the node named by it.
func TestRetrieveRanksTheNodeNamedOverOneThatMentionsIt(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "target:.:build", Kind: types.KindTarget, Label: "build", Source: "."})
	g.AddNode(types.KnowledgeNode{ID: "op:ts:tsc-build", Kind: types.KindOp, Label: "tsc-build",
		Doc: "tsc-build runs the incremental build mode, so a build reuses the last build's output"})
	ms, _, err := g.Retrieve("build", shipped())
	require.NoError(t, err)
	assert.Equal(t, []string{"target:.:build", "op:ts:tsc-build"}, retrieveIDs(ms))
}

func TestRetrieveRefusesUnusableWeights(t *testing.T) {
	opts := shipped()
	opts.Reader = readerPaths
	opts.Weights.PPR.Restart = 0
	_, _, err := readerFixture().Retrieve("parse", opts)
	require.ErrorContains(t, err, "ppr.restart")
}
