package eval

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/train/completion/fim"
)

type CompleteSuite struct{ suite.Suite }

func TestComplete(t *testing.T) { suite.Run(t, new(CompleteSuite)) }

func (s *CompleteSuite) TestEditSimilarityIsOneMinusNormalizedDistance() {
	s.Equal(1.0, EditSimilarity("", ""))
	s.Equal(1.0, EditSimilarity("abc", "abc"))
	s.InDelta(0.75, EditSimilarity("abcd", "abce"), 1e-9)
	s.Equal(0.0, EditSimilarity("abc", ""))
}

func (s *CompleteSuite) TestFirstLineDropsTheRestAndTheCarriageReturn() {
	s.Equal("a b", FirstLine("a b\r\nnext"))
	s.Equal("", FirstLine("\nx"))
}

func (s *CompleteSuite) TestIdentifierPRCountsSharedWords() {
	p, r, pOK, rOK := IdentifierPR("foo(bar, baz)", "foo(bar, qux, zed)")
	s.True(pOK)
	s.True(rOK)
	s.InDelta(2.0/3, p, 1e-9)
	s.InDelta(2.0/4, r, 1e-9)
	_, _, pOK, rOK = IdentifierPR("", "x")
	s.False(pOK)
	s.True(rOK)
}

func span(id, middle string) Span {
	return Span{ID: id, Group: "g" + id, Sample: fim.Sample{Middle: middle}}
}

func (s *CompleteSuite) TestScoreUsefulOnFirstLineOrSimilarAndChecked() {
	ref := span("1", "return a + b\n}")
	s.True(Score(ref, Pred{Text: "return a + b\n"}, Check{}).Useful)
	near := Pred{Text: "return a + c\n}"}
	s.False(Score(ref, near, Check{}).Useful)
	s.False(Score(ref, near, Check{Known: true, Pass: false}).Useful)
	s.True(Score(ref, Pred{Text: "return a + bb\n}"}, Check{Known: true, Pass: true}).Useful)
}

func (s *CompleteSuite) TestScoreAnEmptyReferenceIsNeverUsefulAndABlankPredictionAbstains() {
	empty := span("2", "  \n")
	sc := Score(empty, Pred{Text: "x"}, Check{})
	s.True(sc.RefEmpty)
	s.True(sc.Shown)
	s.False(sc.Useful)
	s.False(Score(span("3", "x"), Pred{Text: " "}, Check{}).Shown)
}

func (s *CompleteSuite) TestCurveAreaAndThresholds() {
	rows := []Scored{
		{ID: "a", Shown: true, Confidence: 0.9, Useful: true},
		{ID: "b", Shown: true, Confidence: 0.8, Useful: true},
		{ID: "c", Shown: true, Confidence: 0.6, Useful: false},
		{ID: "d", Shown: true, Confidence: 0.4, Useful: true},
		{ID: "e", RefEmpty: true, Shown: true, Confidence: 0.3},
		{ID: "f"},
	}
	pts := Curve(rows)
	s.Require().Len(pts, 5)
	s.Equal(Point{Tau: 0.9, Precision: 1, Recall: 0.2, Shown: 1, Useful: 1}, pts[0])
	s.InDelta(0.6, pts[3].Recall, 1e-9)
	s.InDelta(0.75, pts[3].Precision, 1e-9)
	s.InDelta(0.2*1+0.2*1+0.2*0.75, AUPRC(pts), 1e-9)
	at, ok := AtPrecision(pts, 0.7)
	s.True(ok)
	s.Equal(0.4, at.Tau)
	_, ok = AtPrecision(pts, 1.01)
	s.False(ok)
	s.Equal(2, AtTau(pts, 0.7).Shown)
	s.Equal(Point{}, AtTau(pts, 0.95))
}

func (s *CompleteSuite) TestCurveMergesEqualConfidence() {
	pts := Curve([]Scored{{Shown: true, Confidence: 0.5, Useful: true}, {Shown: true, Confidence: 0.5}})
	s.Len(pts, 1)
	s.Equal(2, pts[0].Shown)
}
