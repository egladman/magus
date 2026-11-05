package pulls

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
	"github.com/egladman/magus/types"
)

type CadenceSuite struct{ suite.Suite }

func TestCadenceSuite(t *testing.T) { suite.Run(t, new(CadenceSuite)) }

func (s *CadenceSuite) TestActiveWhileAnyCheckRuns() {
	list := types.PullList{Pulls: []types.ForgePull{newPull(1, "ada", running("a"))}}

	c := chooseCadence(testConfig(), list, testNow)

	s.Equal(cadence{interval: time.Minute, reason: "checks running: every 1m"}, c)
}

func (s *CadenceSuite) TestIdleBacksOffWhenNothingRuns() {
	list := types.PullList{Pulls: []types.ForgePull{newPull(1, "ada", passing("a"))}}

	c := chooseCadence(testConfig(), list, testNow)

	s.Equal(cadence{interval: 5 * time.Minute, reason: "nothing running: backed off to 5m"}, c)
}

func (s *CadenceSuite) TestMainRunningCountsAsActive() {
	list := types.PullList{Base: types.BaseBranch{Checks: []types.ForgeCheck{running("a")}}}

	s.Equal(time.Minute, chooseCadence(testConfig(), list, testNow).interval)
}

func (s *CadenceSuite) TestNeverFasterThanTheFloor() {
	cfg := testConfig()
	cfg.Active = 5 * time.Second
	list := types.PullList{Pulls: []types.ForgePull{newPull(1, "ada", running("a"))}}

	c := chooseCadence(cfg, list, testNow)

	s.Equal(30*time.Second, c.interval)
	s.Contains(c.reason, "the poll floor")
}

func (s *CadenceSuite) TestNearTheReserveWaitsForTheReset() {
	list := types.PullList{
		Pulls: []types.ForgePull{newPull(1, "ada", running("a"))},
		Rate:  types.RateLimit{Limit: 5000, Remaining: 312, Reset: testNow.Add(20 * time.Minute).Format(time.RFC3339)},
	}

	c := chooseCadence(testConfig(), list, testNow)

	s.Equal(cadence{interval: 20 * time.Minute, reason: "312 requests left of 5000: waiting until 12:20 reset"}, c)
}

func (s *CadenceSuite) TestAPastResetDoesNotHoldThePoller() {
	list := types.PullList{
		Rate: types.RateLimit{Limit: 5000, Remaining: 10, Reset: testNow.Add(-time.Minute).Format(time.RFC3339)},
	}

	s.Equal(5*time.Minute, chooseCadence(testConfig(), list, testNow).interval)
}

func (s *CadenceSuite) TestAnUnknownRateIsNotNearTheReserve() {
	s.Equal(5*time.Minute, chooseCadence(testConfig(), types.PullList{}, testNow).interval)
}

type PollerSuite struct {
	suite.Suite
	now   time.Time
	store Store
	list  types.PullList
	err   error
	calls []types.ListPullsRequest
	p     *Poller
}

func TestPollerSuite(t *testing.T) { suite.Run(t, new(PollerSuite)) }

func (s *PollerSuite) SetupTest() {
	s.now = testNow
	s.err = nil
	s.calls = nil
	s.store = OpenStore(filepath.Join(s.T().TempDir(), "authors.jsonl"))
	s.list = types.PullList{
		Repo:   "acme/acme",
		Viewer: "ada",
		Pulls:  []types.ForgePull{newPull(1, "ada", passing("a")), newPull(2, "grace")},
		Rate:   types.RateLimit{Limit: 5000, Remaining: 4000},
	}
	s.p = s.poller()
}

func (s *PollerSuite) poller() *Poller {
	return NewPoller(PollerOptions{
		Config: testConfig(),
		Store:  s.store,
		List: func(_ context.Context, req types.ListPullsRequest) (types.PullList, error) {
			s.calls = append(s.calls, req)
			return s.list, s.err
		},
		Remote: func(context.Context) string { return "git@github.com:acme/acme.git" },
		Base:   "main",
		Now:    func() time.Time { return s.now },
	})
}

func (s *PollerSuite) board() *pullsv1.Board {
	b, err := s.p.Board()
	s.Require().NoError(err)
	return b
}

func (s *PollerSuite) TestBoardBeforeTheFirstSyncSaysItIsWaiting() {
	b := s.board()

	s.Empty(s.calls)
	s.Equal("waiting for the first sync", b.Sync.CadenceReason)
	s.Nil(b.Sync.SyncedAt)
}

func (s *PollerSuite) TestSyncFillsTheSyncState() {
	s.list.Requests, s.list.Revalidated = 12, 9
	s.p.Sync(s.T().Context())

	st := s.board().Sync
	s.Equal(s.now, st.SyncedAt.AsTime())
	s.Equal(s.now.Add(5*time.Minute), st.NextSyncAt.AsTime())
	s.EqualValues(4000, st.RateRemaining)
	s.EqualValues(12, st.Requests)
	s.EqualValues(9, st.Revalidated)
	s.Empty(st.SyncError)
}

func (s *PollerSuite) TestFirstSyncAsksForEveryoneBecauseTheViewerIsUnknown() {
	s.p.Sync(s.T().Context())
	s.p.Sync(s.T().Context())

	s.Require().Len(s.calls, 2)
	s.Nil(s.calls[0].Authors)
	s.Equal([]string{"ada"}, s.calls[1].Authors)
	s.Equal(types.ListPullsRequest{Base: "main", Remote: "git@github.com:acme/acme.git"}, s.calls[0])
}

func (s *PollerSuite) TestAFailedSyncKeepsTheLastGoodBoard() {
	s.p.Sync(s.T().Context())
	s.now = s.now.Add(time.Minute)
	s.err = errors.New("HTTP 502")
	s.p.Sync(s.T().Context())

	b := s.board()
	s.Require().Len(b.Pulls, 1)
	s.Contains(b.Sync.SyncError, "HTTP 502")
	s.Equal(s.now, b.Sync.FailedAt.AsTime())
	s.Equal(testNow, b.Sync.SyncedAt.AsTime())
}

func (s *PollerSuite) TestAReadReasonIsAFailureNotAnEmptyBoard() {
	s.p.Sync(s.T().Context())
	s.list = types.PullList{Reason: "no credential"}
	s.p.Sync(s.T().Context())

	b := s.board()
	s.Len(b.Pulls, 1)
	s.Contains(b.Sync.SyncError, "no credential")
}

func (s *PollerSuite) TestARecoveredSyncClearsTheError() {
	s.err = errors.New("HTTP 502")
	s.p.Sync(s.T().Context())
	s.err = nil
	s.p.Sync(s.T().Context())

	b := s.board()
	s.Empty(b.Sync.SyncError)
	s.Nil(b.Sync.FailedAt)
}

func (s *PollerSuite) TestNoRemoteFailsLoudly() {
	p := NewPoller(PollerOptions{
		Config: testConfig(),
		Store:  s.store,
		Remote: func(context.Context) string { return "" },
		Now:    func() time.Time { return s.now },
	})
	p.Sync(s.T().Context())

	b, err := p.Board()
	s.Require().NoError(err)
	s.Contains(b.Sync.SyncError, "no remote")
}

func (s *PollerSuite) TestKickWithinTheFloorWaitsOutTheRest() {
	s.p.Sync(s.T().Context())
	s.now = s.now.Add(10 * time.Second)

	s.Equal(20*time.Second, s.p.floorWait())
}

func (s *PollerSuite) TestKickAfterTheFloorSyncsNow() {
	s.p.Sync(s.T().Context())
	s.now = s.now.Add(time.Minute)

	s.Zero(s.p.floorWait())
}

func (s *PollerSuite) TestNextWaitNeverUndercutsTheFloor() {
	s.p.Sync(s.T().Context())
	s.now = s.now.Add(5*time.Minute - time.Second)

	s.Equal(30*time.Second, s.p.nextWait())
}

func (s *PollerSuite) TestFilterChangeAppliesToTheLastListAtOnce() {
	s.p.Sync(s.T().Context())
	s.Require().NoError(s.store.Set(s.T().Context(), []string{"grace"}))

	b := s.board()
	s.Require().Len(b.Pulls, 1)
	s.EqualValues(2, b.Pulls[0].Number)
}

func (s *PollerSuite) TestUnfetchedBaseNoteJoinsTheReason() {
	p := NewPoller(PollerOptions{
		Config:   testConfig(),
		Store:    s.store,
		List:     func(context.Context, types.ListPullsRequest) (types.PullList, error) { return s.list, nil },
		Remote:   func(context.Context) string { return "r" },
		BaseNote: "vcs.base_ref is unset: reading pull requests against main",
		Now:      func() time.Time { return s.now },
	})
	p.Sync(s.T().Context())

	b, err := p.Board()
	s.Require().NoError(err)
	s.Contains(b.Sync.CadenceReason, "vcs.base_ref is unset")
}
