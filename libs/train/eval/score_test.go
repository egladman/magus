package eval

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/train/completion/fim"
)

type ScoreSuite struct{ suite.Suite }

func TestScore(t *testing.T) { suite.Run(t, new(ScoreSuite)) }

func metric(rows []Row, name string) Row {
	for _, r := range rows {
		if r.Metric == name {
			return r
		}
	}
	return Row{Metric: "missing " + name}
}

// fixture: 12 spans in 4 groups; the first 8 predictions are right, the next 2 wrong,
// the last 2 are blank-reference spans where the model answers anyway at low confidence.
func fixture(lang string) (Set, map[string]Pred) {
	set := Set{Name: lang + "-test", Language: lang, Of: 40}
	preds := map[string]Pred{}
	for i := range 12 {
		id := fmt.Sprintf("s%02d", i)
		mid := fmt.Sprintf("return value%d", i)
		sp := Span{ID: id, Group: fmt.Sprintf("g%d", i%4), Sample: fim.Sample{Middle: mid}}
		pred := Pred{ID: id, Text: mid, Confidence: 0.95 - float64(i)*0.01}
		switch {
		case i >= 10:
			sp.Middle = ""
			pred = Pred{ID: id, Text: "x", Confidence: 0.2}
		case i >= 8:
			pred.Text = "something else entirely"
			pred.Confidence = 0.4
		}
		set.Spans = append(set.Spans, sp)
		preds[id] = pred
	}
	return set, preds
}

func (s *ScoreSuite) TestScoreCompleteRowsHoldThePlanMetrics() {
	sys := System{Name: "m", Order: fim.SPM, Context: "none"}
	set, preds := fixture("go")
	m := ScoreComplete(sys, set, preds, map[string]Check{}, "r1")
	p := metric(m.Rows, "precision@p80")
	s.Require().NotNil(p.Value)
	s.InDelta(0.8, *p.Value, 1e-9)
	r := metric(m.Rows, "recall@p80")
	s.InDelta(8.0/10, *r.Value, 1e-9)
	s.Equal(40, r.Of)
	s.InDelta(0.4, *metric(m.Rows, "tau@p80").Value, 1e-9)
	half := metric(m.Rows, "precision@0.5")
	s.InDelta(1.0, *half.Value, 1e-9)
	s.NotNil(metric(m.Rows, "auprc").Value)
	s.NotNil(metric(m.Rows, "ident_recall").Value)
	ab := metric(m.Rows, "abstention@p80")
	s.InDelta(1.0, *ab.Value, 1e-9)
	s.Equal(2, ab.N)
	s.Equal("m", p.System)
	s.Equal("spm", p.Order)
	s.Equal("none", p.Context)
}

func (s *ScoreSuite) TestTypeScriptCheckMetricsAreNotScored() {
	sys := System{Name: "m", Order: fim.SPM, Context: "none"}
	set, preds := fixture("typescript")
	m := ScoreComplete(sys, set, preds, map[string]Check{}, "r1")
	c := metric(m.Rows, "check_precision")
	s.Nil(c.Value)
	s.Contains(c.NotScored, "tsgo")
	s.Equal("missing check_precision@p80", metric(m.Rows, "check_precision@p80").Metric)
}

func (s *ScoreSuite) TestNoThresholdAtPrecisionIsSaidNotSilentlyDropped() {
	sys := System{Name: "m", Order: fim.SPM, Context: "none"}
	set, preds := fixture("go")
	for id, p := range preds {
		p.Text = "wrong"
		preds[id] = p
	}
	m := ScoreComplete(sys, set, preds, map[string]Check{}, "r1")
	r := metric(m.Rows, "precision@p80")
	s.Nil(r.Value)
	s.Contains(r.NotScored, "precision 0.8")
}

func (s *ScoreSuite) TestCompareFillsPValuesAgainstTheReference() {
	sys := System{Name: "m", Order: fim.SPM, Context: "none"}
	set, preds := fixture("go")
	a := ScoreComplete(sys, set, preds, map[string]Check{}, "r1")
	worse := map[string]Pred{}
	for id, p := range preds {
		p.Text = "wrong"
		worse[id] = p
	}
	b := ScoreComplete(System{Name: "n", Order: fim.SPM, Context: "none"}, set, worse, map[string]Check{}, "r1")
	a.Compare(b, "n.spm.none")
	r := metric(a.Rows, "ident_recall")
	s.Require().NotNil(r.P)
	s.Equal("n.spm.none", r.Ref)
	s.Nil(metric(b.Rows, "ident_recall").P)
}

func (s *ScoreSuite) TestResolveSystemRefusesAnUnknownOrder() {
	_, err := ResolveSystem(nil, "/", "x", "psm", "none")
	s.ErrorContains(err, "pins 0 GGUFs")
}
