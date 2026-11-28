package wake

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

const t0 int64 = 1_800_000_000

// The caller owns the loop: it asks Decide, waits for NotBefore its own way, starts a
// turn, does it, and files the report. The store refuses any step past the terms.
func Example() {
	ctx := context.Background()
	now := time.Unix(t0, 0)
	store, err := Open(&MemoryLog{}, WithClock(func() time.Time { return now }))
	if err != nil {
		fmt.Println(err)
		return
	}
	w, err := store.Begin(ctx, Terms{
		ID: "pull-requests", Until: now.Unix() + 3600, MaxWakes: 5, MaxIdle: 2, MinGap: 600,
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	waiting := 3
	for {
		next := Decide(w, now.Unix())
		if !next.Again {
			fmt.Printf("ended %s after %d turns\n", next.End, w.N)
			return
		}
		// A real caller sleeps, sets a timer, or exits and is run again here.
		now = time.Unix(max(now.Unix(), next.NotBefore), 0)
		started, err := store.Start(ctx, w.Terms.ID)
		if err != nil {
			var ended *EndedError
			if errors.As(err, &ended) {
				fmt.Printf("ended %s after %d turns\n", ended.End, w.N)
			}
			return
		}
		acted := min(waiting, 1)
		waiting -= acted
		w, err = store.RecordWake(ctx, Report{Wake: w.Terms.ID, N: started.Open.N, Acted: acted, Pending: waiting})
		if err != nil {
			fmt.Println(err)
			return
		}
	}
	// Output: ended done after 4 turns
}

type clock struct{ now int64 }

func (c *clock) Now() time.Time { return time.Unix(c.now, 0) }

type pricer map[string]budget.Price

func (p pricer) Price(model string) (budget.Price, bool) {
	r, ok := p[model]
	return r, ok
}

// keyLog is a MemoryLog that also keeps the key of every append.
type keyLog struct {
	MemoryLog
	keys []string
}

func (l *keyLog) Append(ctx context.Context, key string, line []byte) error {
	l.keys = append(l.keys, key)
	return l.MemoryLog.Append(ctx, key, line)
}

// touchLog fails any call, so a test can see that nothing called it.
type touchLog struct{ touched bool }

func (l *touchLog) Append(context.Context, string, []byte) error {
	l.touched = true
	return errors.New("touched")
}

func (l *touchLog) Read(context.Context) ([][]byte, error) {
	l.touched = true
	return nil, errors.New("touched")
}

func terms(id string) Terms {
	return Terms{ID: id, Until: t0 + 3600, MaxWakes: 5, MaxIdle: 2, MinGap: 60}
}

type StoreSuite struct {
	suite.Suite
	ctx   context.Context
	log   *keyLog
	clock *clock
	store *Store
}

func TestStore(t *testing.T) { suite.Run(t, new(StoreSuite)) }

func (s *StoreSuite) SetupTest() {
	s.ctx = context.Background()
	s.log = &keyLog{}
	s.clock = &clock{now: t0}
	s.store = s.open()
}

func (s *StoreSuite) open(opts ...Option) *Store {
	store, err := Open(s.log, append([]Option{WithClock(s.clock.Now)}, opts...)...)
	s.Require().NoError(err)
	return store
}

func (s *StoreSuite) begin(t Terms) Wake {
	w, err := s.store.Begin(s.ctx, t)
	s.Require().NoError(err)
	return w
}

func (s *StoreSuite) start(id string) Wake {
	w, err := s.store.Start(s.ctx, id)
	s.Require().NoError(err)
	return w
}

func (s *StoreSuite) report(r Report) Wake {
	w, err := s.store.RecordWake(s.ctx, r)
	s.Require().NoError(err)
	return w
}

func (s *StoreSuite) appendLine(key string, entry any) {
	line, err := codec.Marshal(entry)
	s.Require().NoError(err)
	s.Require().NoError(s.log.Append(s.ctx, key, line))
}

func (s *StoreSuite) TestOpenRefusesANilClockAndANilPricerTogether() {
	_, err := Open(&MemoryLog{}, WithClock(nil), WithPricer(nil))
	var opt *OptionError
	s.Require().ErrorAs(err, &opt)
	s.ErrorContains(err, "WithClock")
	s.ErrorContains(err, "WithPricer")
}

func (s *StoreSuite) TestOpenRefusesANilLog() {
	_, err := Open(nil)
	s.ErrorContains(err, "needs a Log")
}

func (s *StoreSuite) TestOpenDoesNoIO() {
	l := &touchLog{}
	_, err := Open(l)
	s.Require().NoError(err)
	s.False(l.touched)
}

func (s *StoreSuite) TestBeginSetsAtAndWritesTheTermsKey() {
	w := s.begin(terms("prs"))
	s.Equal(t0, w.Terms.At)
	s.Equal([]string{"terms prs"}, s.log.keys)
	got, err := s.store.Get(s.ctx, "prs")
	s.Require().NoError(err)
	s.Equal(w, got)
}

func (s *StoreSuite) TestBeginRefusesEveryMissingBoundAtOnce() {
	_, err := s.store.Begin(s.ctx, Terms{ID: "prs"})
	for _, field := range []string{"Until", "MaxWakes", "MaxIdle", "MinGap"} {
		s.ErrorContains(err, "terms "+field+": is required")
	}
	var te *TermsError
	s.ErrorAs(err, &te)
	s.Empty(s.log.keys, "refused terms write nothing")
}

func (s *StoreSuite) TestBeginRefusesAnAtTheCallerSupplied() {
	t := terms("prs")
	t.At = t0
	_, err := s.store.Begin(s.ctx, t)
	s.ErrorContains(err, "terms At: the store sets it")
}

func (s *StoreSuite) TestBeginRefusesAnIDHoldingWhitespace() {
	_, err := s.store.Begin(s.ctx, terms("pull requests"))
	s.ErrorContains(err, "terms ID")
	_, err = s.store.Begin(s.ctx, terms(""))
	s.ErrorContains(err, "terms ID")
}

func (s *StoreSuite) TestBeginRefusesADeadlineNotAfterNow() {
	t := terms("prs")
	t.Until = t0
	_, err := s.store.Begin(s.ctx, t)
	s.ErrorContains(err, "terms Until: ")
	s.ErrorContains(err, "is not after now")
}

func (s *StoreSuite) TestBeginRefusesASpendBoundWithNoPricer() {
	t := terms("prs")
	t.MaxUSD = 5
	_, err := s.store.Begin(s.ctx, t)
	s.ErrorContains(err, "terms MaxUSD: a spend bound needs a pricer")
}

func (s *StoreSuite) TestBeginRefusesANegativeSpendBound() {
	t := terms("prs")
	t.MaxUSD = -1
	_, err := s.store.Begin(s.ctx, t)
	s.ErrorContains(err, "terms MaxUSD: is below 0")
}

func (s *StoreSuite) TestBeginRefusesAnIDAlreadyBegun() {
	s.begin(terms("prs"))
	_, err := s.store.Begin(s.ctx, terms("prs"))
	s.ErrorIs(err, ErrDuplicateWake)
}

func (s *StoreSuite) TestEveryCallRefusesAnUnknownWake() {
	_, err := s.store.Start(s.ctx, "none")
	s.ErrorIs(err, ErrUnknownWake)
	_, err = s.store.RecordWake(s.ctx, Report{Wake: "none", N: 1})
	s.ErrorIs(err, ErrUnknownWake)
	_, err = s.store.Stop(s.ctx, "none", job.Origin{}, "")
	s.ErrorIs(err, ErrUnknownWake)
	_, err = s.store.Get(s.ctx, "none")
	s.ErrorIs(err, ErrUnknownWake)
}

func (s *StoreSuite) TestStartOpensTheNextTurnUnderItsKey() {
	s.begin(terms("prs"))
	w := s.start("prs")
	s.Equal(Start{Wake: "prs", N: 1, At: t0}, w.Open)
	s.Equal([]string{"terms prs", "start prs 1"}, s.log.keys)
}

func (s *StoreSuite) TestStartRefusesWhileATurnIsOpen() {
	s.begin(terms("prs"))
	s.start("prs")
	_, err := s.store.Start(s.ctx, "prs")
	var open *OpenError
	s.Require().ErrorAs(err, &open)
	s.Equal(1, open.N)
}

func (s *StoreSuite) TestStartRefusesBeforeTheMinimumGap() {
	s.begin(terms("prs"))
	s.start("prs")
	s.clock.now += 10
	s.report(Report{Wake: "prs", N: 1, Acted: 1, Pending: 1})
	s.clock.now += 59
	_, err := s.store.Start(s.ctx, "prs")
	var soon *TooSoonError
	s.Require().ErrorAs(err, &soon)
	s.Equal(t0+70, soon.NotBefore)
	s.clock.now++
	s.start("prs")
}

func (s *StoreSuite) TestStartRefusesAtTheDeadline() {
	s.begin(terms("prs"))
	s.clock.now = t0 + 3600
	_, err := s.store.Start(s.ctx, "prs")
	var ended *EndedError
	s.Require().ErrorAs(err, &ended)
	s.Equal(EndUntil, ended.End)
}

func (s *StoreSuite) TestRecordWakeRefusesAReportWithNoOpenTurn() {
	s.begin(terms("prs"))
	_, err := s.store.RecordWake(s.ctx, Report{Wake: "prs", N: 1})
	var nn *NotNextError
	s.Require().ErrorAs(err, &nn)
	s.Equal(0, nn.Want)
}

func (s *StoreSuite) TestRecordWakeRefusesAReportForAnotherTurn() {
	s.begin(terms("prs"))
	s.start("prs")
	_, err := s.store.RecordWake(s.ctx, Report{Wake: "prs", N: 2})
	var nn *NotNextError
	s.Require().ErrorAs(err, &nn)
	s.Equal(1, nn.Want)
	s.Equal(2, nn.Got)
}

func (s *StoreSuite) TestRecordWakeStampsTheTurnAndWritesItsKey() {
	s.begin(terms("prs"))
	s.start("prs")
	s.clock.now += 30
	w := s.report(Report{Wake: "prs", N: 1, Acted: 2, Pending: 1})
	s.Equal(1, w.N)
	s.Equal(t0, w.Last.Started)
	s.Equal(t0+30, w.Last.Ended)
	s.Zero(w.Open)
	s.Equal("report prs 1", s.log.keys[len(s.log.keys)-1])
}

func (s *StoreSuite) TestRecordWakeRefusesWhatTheStoreSets() {
	s.begin(terms("prs"))
	s.start("prs")
	_, err := s.store.RecordWake(s.ctx, Report{Wake: "prs", N: 1, Started: 1, Ended: 2, USD: 3, Acted: -1})
	var re *ReportError
	s.Require().ErrorAs(err, &re)
	for _, field := range []string{"Started", "Ended", "USD", "Acted"} {
		s.ErrorContains(err, "report 1 "+field+":")
	}
}

func (s *StoreSuite) TestRecordWakeRefusesAReportAfterTheTurnLapsed() {
	s.begin(terms("prs"))
	s.start("prs")
	s.clock.now = t0 + 3600
	_, err := s.store.RecordWake(s.ctx, Report{Wake: "prs", N: 1, Acted: 1})
	var ended *EndedError
	s.Require().ErrorAs(err, &ended)
	s.Equal(EndLapsed, ended.End)
	s.Equal(t0+3600, ended.At)
}

func (s *StoreSuite) TestNoSpendBoundPricesNothing() {
	s.begin(terms("prs"))
	s.start("prs")
	w := s.report(Report{Wake: "prs", N: 1, Acted: 1, Model: "m", Calls: []Usage{{Input: 1_000_000}}})
	s.Zero(w.Last.USD)
	s.Zero(w.SpentUSD)
}

func (s *StoreSuite) spendStore(p pricer) {
	s.store = s.open(WithPricer(p))
	t := terms("prs")
	t.MaxUSD = 2
	s.begin(t)
	s.start("prs")
}

func (s *StoreSuite) TestASpendBoundNeedsAModelAndCalls() {
	s.spendStore(pricer{})
	_, err := s.store.RecordWake(s.ctx, Report{Wake: "prs", N: 1, Acted: 1})
	s.ErrorContains(err, "report 1 Model:")
	s.ErrorContains(err, "report 1 Calls:")
}

func (s *StoreSuite) TestASpendBoundRefusesAModelThePricerDoesNotKnow() {
	s.spendStore(pricer{})
	_, err := s.store.RecordWake(s.ctx, Report{Wake: "prs", N: 1, Acted: 1, Model: "m", Calls: []Usage{{Input: 1}}})
	var up *UnpricedError
	s.Require().ErrorAs(err, &up)
	s.Equal(UnpricedError{Model: "m"}, *up)
}

func (s *StoreSuite) TestAZeroRateIsUnknownNotFree() {
	s.spendStore(pricer{"m": {Input: 3}})
	_, err := s.store.RecordWake(s.ctx, Report{Wake: "prs", N: 1, Acted: 1, Model: "m", Calls: []Usage{{Input: 1, Output: 1}}})
	var up *UnpricedError
	s.Require().ErrorAs(err, &up)
	s.Equal("output", up.Class)
}

func (s *StoreSuite) TestTheStoreKeepsThePricedSpendOnTheReport() {
	s.spendStore(pricer{"m": {
		Input: 3, Output: 15, CacheRead: 0.3,
		LongContext: &budget.PriceStep{Above: 200_000, Multiplier: 2},
	}})
	calls := []Usage{
		{Input: 100_000, Output: 10_000},
		{Input: 150_000, CacheRead: 100_000, Output: 10_000},
	}
	w := s.report(Report{Wake: "prs", N: 1, Acted: 1, Pending: 1, Model: "m", Calls: calls})
	// 0.3 + 0.15, then (0.45 + 0.03 + 0.15) doubled past 200k prompt tokens.
	s.InDelta(0.45+1.26, w.Last.USD, 1e-9)
	s.InDelta(0.45+1.26, w.SpentUSD, 1e-9)

	reopened, err := Open(s.log, WithClock(s.clock.Now))
	s.Require().NoError(err)
	got, err := reopened.Get(s.ctx, "prs")
	s.Require().NoError(err)
	s.InDelta(w.SpentUSD, got.SpentUSD, 1e-9, "folding needs no pricer")
}

func (s *StoreSuite) TestSpendReachingTheBoundEndsTheWake() {
	s.spendStore(pricer{"m": {Output: 1}})
	w := s.report(Report{Wake: "prs", N: 1, Acted: 1, Pending: 1, Model: "m", Calls: []Usage{{Output: 2_500_000}}})
	s.InDelta(2.5, w.SpentUSD, 1e-9, "the report that passes the bound is kept")
	s.Equal(EndSpend, Decide(w, s.clock.now).End)
}

func (s *StoreSuite) TestIdleTurnsInARowEndTheWake() {
	s.begin(terms("prs"))
	var w Wake
	for n := 1; n <= 2; n++ {
		s.start("prs")
		w = s.report(Report{Wake: "prs", N: n, Pending: 1})
		s.clock.now += 60
	}
	s.Equal(2, w.Idle)
	s.Equal(EndIdle, Decide(w, s.clock.now).End)
	_, err := s.store.Start(s.ctx, "prs")
	var ended *EndedError
	s.Require().ErrorAs(err, &ended)
	s.Equal(EndIdle, ended.End)
}

func (s *StoreSuite) TestStopEndsTheWakeAndRecordsWhoAndWhy() {
	s.begin(terms("prs"))
	by := job.Origin{User: "eli", EntryPoint: job.EntryPointCLI}
	w, err := s.store.Stop(s.ctx, "prs", by, "merged by hand")
	s.Require().NoError(err)
	s.Equal(Stop{Wake: "prs", At: t0, By: by, Why: "merged by hand"}, w.Stopped)
	s.Equal("stop prs", s.log.keys[len(s.log.keys)-1])
	_, err = s.store.Start(s.ctx, "prs")
	var ended *EndedError
	s.Require().ErrorAs(err, &ended)
	s.Equal(EndStopped, ended.End)
}

func (s *StoreSuite) TestStopNeverReplacesAnEndAlreadyReached() {
	s.begin(terms("prs"))
	s.start("prs")
	s.report(Report{Wake: "prs", N: 1})
	_, err := s.store.Stop(s.ctx, "prs", job.Origin{}, "")
	var ended *EndedError
	s.Require().ErrorAs(err, &ended)
	s.Equal(EndDone, ended.End)
}

func (s *StoreSuite) TestReadFoldsEveryWakeInTheOrderBegun() {
	s.begin(terms("b"))
	s.begin(terms("a"))
	s.start("a")
	w := s.report(Report{Wake: "a", N: 1, Acted: 1, Pending: 1})
	wakes, err := s.store.Read(s.ctx)
	s.Require().NoError(err)
	s.Require().Len(wakes, 2)
	s.Equal("b", wakes[0].Terms.ID)
	s.Equal(w, wakes[1])
}

func (s *StoreSuite) TestTheLastLineOfAKeyWins() {
	t := terms("prs")
	t.At, t.Subject = t0, "first"
	s.appendLine("terms prs", t)
	t.Subject = "second"
	s.appendLine("terms prs", t)
	w, err := s.store.Get(s.ctx, "prs")
	s.Require().NoError(err)
	s.Equal("second", w.Terms.Subject)
}

func (s *StoreSuite) TestALineThatDoesNotDecodeIsCorrupt() {
	s.Require().NoError(s.log.Append(s.ctx, "x", []byte("not json")))
	_, err := s.store.Read(s.ctx)
	var c *CorruptError
	s.Require().ErrorAs(err, &c)
	s.Equal(1, c.Line)
}

func (s *StoreSuite) TestALineOfAnotherVersionIsCorrupt() {
	s.begin(terms("prs"))
	s.Require().NoError(s.log.Append(s.ctx, "stop prs", []byte(`{"api_version":"safetyscissors.wake/v1beta1","kind":"Stop","wake":"prs"}`)))
	_, err := s.store.Read(s.ctx)
	var c *CorruptError
	s.Require().ErrorAs(err, &c)
	s.Equal(2, c.Line)
	var refused *apiversion.RefusedError
	s.ErrorAs(err, &refused)
}

func (s *StoreSuite) TestAReportForAWakeNeverBegunIsCorrupt() {
	s.appendLine("report prs 1", Report{Wake: "prs", N: 1})
	_, err := s.store.Read(s.ctx)
	s.ErrorContains(err, "no terms before it")
}

func (s *StoreSuite) TestAReportOutOfTurnIsCorrupt() {
	s.begin(terms("prs"))
	s.appendLine("report prs 2", Report{Wake: "prs", N: 2})
	_, err := s.store.Read(s.ctx)
	var c *CorruptError
	s.Require().ErrorAs(err, &c)
	s.ErrorContains(err, "follows report 0")
}
