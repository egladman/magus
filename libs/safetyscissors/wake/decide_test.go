package wake

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type DecideSuite struct{ suite.Suite }

func TestDecide(t *testing.T) { suite.Run(t, new(DecideSuite)) }

// begun is a wake begun at t0 with an hour, 5 wakes, 2 idle and a minute's gap.
func begun() Wake {
	t := terms("prs")
	t.At = t0
	return Wake{Terms: t}
}

// reported is w after n turns that each acted on one item, the last ending at ended.
func reported(w Wake, n int, ended int64) Wake {
	w.N = n
	w.Last = Report{Wake: w.Terms.ID, N: n, Ended: ended, Acted: 1, Pending: 1}
	return w
}

func (s *DecideSuite) TestBeforeTheFirstReportATurnMayStartAtOnce() {
	next := Decide(begun(), t0)
	s.Equal(Next{Again: true, NotBefore: t0, WakesLeft: 5, SecondsLeft: 3600}, next)
}

func (s *DecideSuite) TestAfterAReportTheNextWaitsTheMinimumGap() {
	next := Decide(reported(begun(), 1, t0+100), t0+100)
	s.True(next.Again)
	s.Equal(t0+160, next.NotBefore)
	s.Equal(4, next.WakesLeft)
}

func (s *DecideSuite) TestAnOpenTurnHoldsTheNextWithNoEnd() {
	w := begun()
	w.Open = Start{Wake: "prs", N: 1, At: t0}
	next := Decide(w, t0+10)
	s.False(next.Again)
	s.Equal(1, next.Open)
	s.Empty(next.End)
}

func (s *DecideSuite) TestAnOpenTurnAtTheDeadlineLapses() {
	w := begun()
	w.Open = Start{Wake: "prs", N: 1, At: t0}
	next := Decide(w, t0+3600)
	s.False(next.Again)
	s.Equal(EndLapsed, next.End)
	s.Equal(t0+3600, next.EndedAt)
}

func (s *DecideSuite) TestAReportActingOnNothingWithNothingPendingIsDone() {
	w := reported(begun(), 1, t0+5)
	w.Last.Acted, w.Last.Pending = 0, 0
	next := Decide(w, t0+5)
	s.Equal(EndDone, next.End)
	s.Equal(t0+5, next.EndedAt)
}

func (s *DecideSuite) TestHeldItemsDoNotHoldTheWakeOpen() {
	w := reported(begun(), 1, t0+5)
	w.Last.Acted, w.Last.Pending = 0, 0
	w.Last.Held = []Held{{Item: "#12", Why: "a merge needs a person"}}
	s.Equal(EndDone, Decide(w, t0+5).End)
}

func (s *DecideSuite) TestSpendReachingTheBoundEnds() {
	w := reported(begun(), 1, t0+5)
	w.Terms.MaxUSD, w.SpentUSD = 2, 2
	next := Decide(w, t0+5)
	s.Equal(EndSpend, next.End)
	s.Zero(next.USDLeft)
}

func (s *DecideSuite) TestSpendBelowTheBoundLeavesTheRest() {
	w := reported(begun(), 1, t0+5)
	w.Terms.MaxUSD, w.SpentUSD = 2, 0.5
	next := Decide(w, t0+5)
	s.True(next.Again)
	s.InDelta(1.5, next.USDLeft, 1e-9)
}

func (s *DecideSuite) TestNoSpendBoundLeavesNoUSD() {
	w := reported(begun(), 1, t0+5)
	w.SpentUSD = 3
	next := Decide(w, t0+5)
	s.True(next.Again)
	s.Zero(next.USDLeft)
}

func (s *DecideSuite) TestMaxIdleTurnsInARowEnd() {
	w := reported(begun(), 2, t0+5)
	w.Last.Acted, w.Idle = 0, 2
	s.Equal(EndIdle, Decide(w, t0+5).End)
	w.Idle = 1
	s.True(Decide(w, t0+5).Again)
}

func (s *DecideSuite) TestMaxWakesReportsEnd() {
	next := Decide(reported(begun(), 5, t0+5), t0+5)
	s.Equal(EndCount, next.End)
	s.Zero(next.WakesLeft)
}

func (s *DecideSuite) TestTheDeadlineEndsWithNoTurnOpen() {
	next := Decide(reported(begun(), 1, t0+5), t0+4000)
	s.Equal(EndUntil, next.End)
	s.Equal(t0+3600, next.EndedAt)
	s.Zero(next.SecondsLeft)
}

func (s *DecideSuite) TestStoppedWinsOverEveryOtherEnd() {
	w := reported(begun(), 5, t0+5)
	w.Last.Acted, w.Last.Pending, w.Idle = 0, 0, 2
	w.Terms.MaxUSD, w.SpentUSD = 1, 1
	w.Stopped = Stop{Wake: "prs", At: t0 + 7}
	next := Decide(w, t0+4000)
	s.Equal(EndStopped, next.End)
	s.Equal(t0+7, next.EndedAt)
}

func (s *DecideSuite) TestLapsedWinsOverDone() {
	w := reported(begun(), 1, t0+5)
	w.Last.Acted, w.Last.Pending = 0, 0
	w.Open = Start{Wake: "prs", N: 2, At: t0 + 100}
	s.Equal(EndLapsed, Decide(w, t0+3600).End)
}

func (s *DecideSuite) TestDoneWinsOverSpendIdleCountAndUntil() {
	w := reported(begun(), 5, t0+5)
	w.Last.Acted, w.Last.Pending, w.Idle = 0, 0, 2
	w.Terms.MaxUSD, w.SpentUSD = 1, 1
	s.Equal(EndDone, Decide(w, t0+4000).End)
}

func (s *DecideSuite) TestSpendWinsOverIdleCountAndUntil() {
	w := reported(begun(), 5, t0+5)
	w.Last.Acted, w.Idle = 0, 2
	w.Terms.MaxUSD, w.SpentUSD = 1, 1
	s.Equal(EndSpend, Decide(w, t0+4000).End)
}

func (s *DecideSuite) TestIdleWinsOverCountAndUntil() {
	w := reported(begun(), 5, t0+5)
	w.Last.Acted, w.Idle = 0, 2
	s.Equal(EndIdle, Decide(w, t0+4000).End)
}

func (s *DecideSuite) TestCountWinsOverUntil() {
	s.Equal(EndCount, Decide(reported(begun(), 5, t0+5), t0+4000).End)
}

func (s *DecideSuite) TestAnEndStaysPutAsTimePasses() {
	w := reported(begun(), 5, t0+5)
	early, late := Decide(w, t0+5), Decide(w, t0+5000)
	s.Equal(EndCount, late.End)
	s.Equal(early.EndedAt, late.EndedAt)
}
