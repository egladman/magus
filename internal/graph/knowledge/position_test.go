package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

type positionSuite struct {
	suite.Suite
	root string
	g    *Graph
}

func TestPosition(t *testing.T) { suite.Run(t, new(positionSuite)) }

// The position's file is app/main.buzz, whose middle defines middleOnly and references
// secret. lib/a.buzz and lib/b.buzz both define parse, held/c.buzz defines heldName.
func (s *positionSuite) SetupTest() {
	s.root = s.T().TempDir()
	files := map[string]string{
		"lib/a.buzz":    "// parse reads a config.\nfun parse() > void {\n}\nfun helper() > void {}\n",
		"lib/b.buzz":    "fun parse() > void {}\n",
		"lib/s.buzz":    "fun secret() > void {}\n",
		"held/c.buzz":   "fun heldName() > void {}\n",
		"app/main.buzz": "fun local() > void {}\nfun middleOnly() > void { secret(); }\n",
	}
	g := NewGraph()
	g.SetRoot(s.root)
	for path, text := range files {
		full := filepath.Join(s.root, filepath.FromSlash(path))
		s.Require().NoError(os.MkdirAll(filepath.Dir(full), 0o755))
		s.Require().NoError(os.WriteFile(full, []byte(text), 0o600))
		g.AddNode(types.KnowledgeNode{ID: fileID(path), Kind: types.KindFile, Label: path, Source: path, Attrs: map[string]string{AttrLanguage: "buzz"}})
	}
	define := func(key, label, file string, line, end int) {
		attrs := map[string]string{}
		if end > 0 {
			attrs[attrDefEndLine] = strconv.Itoa(end)
		}
		g.AddNode(types.KnowledgeNode{ID: symbolID(key), Kind: types.KindSymbol, Label: label, Source: fmt.Sprintf("%s:%d", file, line), Attrs: attrs})
		g.AddEdge(types.KnowledgeEdge{Source: fileID(file), Target: symbolID(key), Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1})
	}
	define("a/parse", "parse", "lib/a.buzz", 2, 3)
	define("a/helper", "helper", "lib/a.buzz", 4, 0)
	define("b/parse", "parse", "lib/b.buzz", 1, 0)
	define("s/secret", "secret", "lib/s.buzz", 1, 0)
	define("c/heldName", "heldName", "held/c.buzz", 1, 0)
	define("main/local", "local", "app/main.buzz", 1, 0)
	define("main/middleOnly", "middleOnly", "app/main.buzz", 2, 0)
	g.AddEdge(types.KnowledgeEdge{Source: symbolID("main/middleOnly"), Target: symbolID("s/secret"), Relation: types.RelationCalls, Confidence: types.ConfidenceExtracted, Score: 1})
	g.AddEdge(types.KnowledgeEdge{Source: fileID("app/main.buzz"), Target: symbolID("s/secret"), Relation: types.RelationReferences, Confidence: types.ConfidenceExtracted, Score: 1})
	s.g = g
}

func (s *positionSuite) position() Position {
	return Position{Path: "app/main.buzz", Offset: 22, Prefix: "fun local() > void {}\nparse(heldName, local", Suffix: "helper()\n"}
}

func (s *positionSuite) options(exclude ...string) RetrieveOptions {
	pos := s.position()
	options := shipped()
	options.Position, options.Exclude = &pos, exclude
	return options
}

func (s *positionSuite) TestRanksTheSymbolsTheWindowNames() {
	ms, ranking, err := s.g.Retrieve("", s.options())
	s.Require().NoError(err)
	s.ElementsMatch([]string{"symbol:a/helper", "symbol:a/parse", "symbol:b/parse", "symbol:c/heldName"}, retrieveIDs(ms))
	// helper is the nearest identifier and its label names one symbol; parse splits over two.
	s.Equal("symbol:a/helper", ms[0].ID)
	s.Contains(ranking.Note, "position app/main.buzz:22")
}

func (s *positionSuite) TestWithNoReaderTheWalkIsSkipped() {
	_, ranking, err := s.g.Retrieve("", s.options())
	s.Require().NoError(err)
	s.Equal([]string{listGraph + ": no reader path names a node"}, ranking.Skipped)
}

func (s *positionSuite) TestAReaderPathAddsTheWalk() {
	options := s.options()
	options.Reader = []string{"lib/a.buzz"}
	ms, ranking, err := s.g.Retrieve("", options)
	s.Require().NoError(err)
	s.Empty(ranking.Skipped)
	s.Contains(ms[0].Why.Lists, types.KnowledgeListRank{List: listGraph, Rank: 1})
}

// A reader path inside the position's own file seeds nothing: the file carries the middle.
func (s *positionSuite) TestAReaderPathInTheOwnFileSeedsNothing() {
	options := s.options()
	options.Reader = []string{"app/main.buzz"}
	_, ranking, err := s.g.Retrieve("", options)
	s.Require().NoError(err)
	s.Equal([]string{listGraph + ": no reader path names a node"}, ranking.Skipped)
}

// The file is on disk whole, so its own symbols and edges carry the middle: local sits in
// the prefix and still never comes back, and secret, named only by the middle, never does.
func (s *positionSuite) TestNeverReturnsThePositionsOwnFile() {
	ms, _, err := s.g.Retrieve("", s.options())
	s.Require().NoError(err)
	s.NotContains(retrieveIDs(ms), "symbol:main/local")
	s.NotContains(retrieveIDs(ms), "symbol:s/secret")
}

func (s *positionSuite) TestExcludedPathsDropBeforeRanking() {
	ms, _, err := s.g.Retrieve("", s.options("held"))
	s.Require().NoError(err)
	s.NotContains(retrieveIDs(ms), "symbol:c/heldName")
}

func (s *positionSuite) TestExcludedPathsDropFromAFreeTextQuery() {
	options := shipped()
	options.Exclude = []string{"lib/a.buzz"}
	ms, _, err := s.g.Retrieve("parse", options)
	s.Require().NoError(err)
	s.Equal([]string{"symbol:b/parse"}, retrieveIDs(ms))
}

func (s *positionSuite) TestFieldFiltersNarrowAPosition() {
	ms, _, err := s.g.Retrieve("id=~parse$", s.options())
	s.Require().NoError(err)
	s.ElementsMatch([]string{"symbol:a/parse", "symbol:b/parse"}, retrieveIDs(ms))
}

func (s *positionSuite) TestFreeTextBesideAPositionIsRefused() {
	_, _, err := s.g.Retrieve("parse", s.options())
	s.ErrorContains(err, "field filters only")
}

func (s *positionSuite) TestTheWalkNeverEntersASkippedNode() {
	seeds := map[string]float64{symbolID("main/middleOnly"): 1, symbolID("a/parse"): 1}
	res, err := s.g.personalizedPageRank(seeds, ShippedWeights().PPR, func(id string) bool { return id == symbolID("main/middleOnly") || id == fileID("app/main.buzz") })
	s.Require().NoError(err)
	s.Equal([]string{symbolID("main/middleOnly")}, res.Unplaced)
	for _, sc := range res.Scores {
		s.NotEqual(symbolID("s/secret"), sc.ID)
	}
}

func (s *positionSuite) TestRetrieveIsByteIdenticalAcrossRuns() {
	first, _, err := s.g.Retrieve("", s.options("held"))
	s.Require().NoError(err)
	a, err := json.Marshal(first)
	s.Require().NoError(err)
	for range 5 {
		again, _, err := s.g.Retrieve("", s.options("held"))
		s.Require().NoError(err)
		b, err := json.Marshal(again)
		s.Require().NoError(err)
		s.Equal(string(a), string(b))
	}
}

func (s *positionSuite) covered() Coverage { return Coverage{Seeded: true, Probed: true} }

func (s *positionSuite) TestRefusesWhenTheProbeDidNotRun() {
	err := s.g.RefusePosition(s.position(), Coverage{Seeded: true})
	s.ErrorIs(err, errPositionCoverage)
	s.ErrorContains(err, "probe did not run")
}

func (s *positionSuite) TestRefusesWhenTheSymbolsWereNotLoaded() {
	s.ErrorContains(s.g.RefusePosition(s.position(), Coverage{Probed: true}), "symbol shards were not loaded")
}

func (s *positionSuite) TestRefusesAFileTheGraphDoesNotHold() {
	pos := s.position()
	pos.Path = "app/new.buzz"
	s.ErrorContains(s.g.RefusePosition(pos, s.covered()), "no file node for app/new.buzz")
}

func (s *positionSuite) TestRefusesAGapInTheOwningProject() {
	cov := s.covered()
	cov.Gaps = []types.KnowledgeSymbolGap{{Project: types.ProjectRef{Path: "app"}, Language: "buzz", State: types.SymbolIndexNotBuilt}}
	err := s.g.RefusePosition(s.position(), cov)
	s.ErrorIs(err, errPositionCoverage)
	s.ErrorContains(err, "app")
}

func (s *positionSuite) TestRefusesAStaleIndexInTheOwningProject() {
	cov := s.covered()
	cov.Stale = []types.KnowledgeStaleIndex{{Project: ".", Language: "buzz"}}
	s.ErrorContains(s.g.RefusePosition(s.position(), cov), "is stale")
}

// A gap in another project, or of another language, cannot hold the position's symbols.
func (s *positionSuite) TestAnswersPastAGapElsewhere() {
	cov := s.covered()
	cov.Gaps = []types.KnowledgeSymbolGap{
		{Project: types.ProjectRef{Path: "lib"}, Language: "buzz", State: types.SymbolIndexNotBuilt},
		{Project: types.ProjectRef{Path: "."}, Language: "go", State: types.SymbolIndexNotBuilt},
	}
	s.NoError(s.g.RefusePosition(s.position(), cov))
}

func (s *positionSuite) TestPositionTextIsTheDocAndDeclarationLine() {
	ms, _, err := s.g.Retrieve("id=~parse$", s.options())
	s.Require().NoError(err)
	sites, dropped := s.g.PositionText(ms, 1, func(string) bool { return true })
	s.Zero(dropped)
	s.Equal([]ContextSite{{MatchID: "symbol:a/parse", Symbol: "parse", KnowledgeDefinitionSite: types.KnowledgeDefinitionSite{
		File: "lib/a.buzz", StartLine: 2, EndLine: 3, Status: types.DefinitionVerified,
		Source: "// parse reads a config.\nfun parse() > void {", SourceStartLine: 1,
	}}}, sites)
}

func (s *positionSuite) TestPositionTextDropsWhatItCannotVerify() {
	ms, _, err := s.g.Retrieve("", s.options())
	s.Require().NoError(err)
	full := filepath.Join(s.root, "lib", "b.buzz")
	s.Require().NoError(os.WriteFile(full, []byte("fun renamed() > void {}\n"), 0o600))
	s.Require().NoError(os.Chtimes(full, time.Now(), time.Now()))
	sites, dropped := s.g.PositionText(ms, 0, func(file string) bool { return file != "held/c.buzz" })
	files := make([]string, len(sites))
	for i, site := range sites {
		files[i] = site.File
	}
	s.ElementsMatch([]string{"lib/a.buzz", "lib/a.buzz"}, files)
	s.Equal(2, dropped, "b.buzz no longer declares parse and held/c.buzz is not fresh")
}

func (s *positionSuite) TestQueryPositionFillsTheContextAndSaysWhatItLeftOut() {
	out, sites, err := s.g.QueryPosition("", 0, 0, 0, s.options(), func(file string) bool { return file != "held/c.buzz" })
	s.Require().NoError(err)
	s.Len(out.Context, len(sites))
	s.Len(sites, 3)
	s.Contains(out.Ranking.Note, "1 site(s) did not verify")
}

func (s *positionSuite) TestQueryPositionNeedsAPosition() {
	_, _, err := s.g.QueryPosition("", 0, 0, 0, shipped(), nil)
	s.ErrorContains(err, "needs a position")
}

func (s *positionSuite) TestPositionTextStopsAtTheBudget() {
	ms, _, err := s.g.Retrieve("", s.options())
	s.Require().NoError(err)
	sites, _ := s.g.PositionText(ms, 1, func(string) bool { return true })
	s.Len(sites, 1)
}
