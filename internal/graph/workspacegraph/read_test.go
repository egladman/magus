package workspacegraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

type ReadSuite struct{ suite.Suite }

func TestReadSuite(t *testing.T) { suite.Run(t, new(ReadSuite)) }

// recordedCoverage is a CoverageFunc that notes what it was asked and answers want.
func recordedCoverage(want knowledge.Coverage, asked *[]string) CoverageFunc {
	return func(input string, seeded bool) knowledge.Coverage {
		*asked = append(*asked, input)
		want.Seeded = seeded
		return want
	}
}

func (s *ReadSuite) TestAnswerQueryJudgesWithTheCoverageOfTheInput() {
	var asked []string
	cov := recordedCoverage(knowledge.Coverage{}, &asked)
	read := knowledge.GraphRead{Input: "zzz-nothing"}
	res := AnswerQuery(context.Background(), knowledge.NewGraph(), read, types.KnowledgeQueryOutput{}, false, cov)
	s.Equal([]string{"zzz-nothing"}, asked)
	s.Equal(knowledge.Answer("zzz-nothing", false, knowledge.Coverage{}), res.Out.Answer)
}

func (s *ReadSuite) TestAnswerQueryNamesTheNearestNodeOnlyWhenAsked() {
	var asked []string
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "target:build", Kind: "target", Label: "build"})
	cov := recordedCoverage(knowledge.Coverage{}, &asked)
	without := AnswerQuery(context.Background(), g, knowledge.GraphRead{Input: "bild"}, types.KnowledgeQueryOutput{}, false, cov)
	with := AnswerQuery(context.Background(), g, knowledge.GraphRead{Input: "bild", Nearest: true}, types.KnowledgeQueryOutput{}, false, cov)
	s.Empty(without.Nearest)
	s.Equal(g.NearestNode("bild"), with.Nearest)
}

func (s *ReadSuite) TestExplainLoadedAMissCarriesTheVerdictAndTheNearestNode() {
	var asked []string
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "target:build", Kind: "target", Label: "build"})
	res := ExplainLoaded(context.Background(), g, knowledge.GraphRead{Input: "target:buld"}, recordedCoverage(knowledge.Coverage{}, &asked))
	s.False(res.Found)
	s.Equal([]string{"target:buld"}, asked)
	s.Equal(g.NearestNode("target:buld"), res.Nearest)
}
