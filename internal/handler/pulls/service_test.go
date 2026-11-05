package pulls

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/suite"

	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
	"github.com/egladman/magus/types"
)

type PullsServiceSuite struct {
	suite.Suite
	store Store
	calls int
	svc   *Service
	p     *Poller
}

func TestPullsServiceSuite(t *testing.T) { suite.Run(t, new(PullsServiceSuite)) }

func (s *PullsServiceSuite) SetupTest() {
	s.calls = 0
	s.store = OpenStore(filepath.Join(s.T().TempDir(), "authors.jsonl"))
	s.p = NewPoller(PollerOptions{
		Config: testConfig(),
		Store:  s.store,
		List: func(context.Context, types.ListPullsRequest) (types.PullList, error) {
			s.calls++
			return types.PullList{Viewer: "ada", Pulls: []types.ForgePull{pullOf(1, "ada"), pullOf(2, "grace")}}, nil
		},
		Remote: func(context.Context) string { return "r" },
		Now:    func() time.Time { return testNow },
	})
	s.svc = NewService(s.p, s.store)
}

func (s *PullsServiceSuite) TestGetPullsNeverCallsTheForge() {
	_, err := s.svc.GetPulls(s.T().Context(), connect.NewRequest(&pullsv1.GetPullsRequest{}))

	s.Require().NoError(err)
	s.Zero(s.calls)
}

func (s *PullsServiceSuite) TestGetPullsServesTheLastBoard() {
	s.p.Sync(s.T().Context())

	resp, err := s.svc.GetPulls(s.T().Context(), connect.NewRequest(&pullsv1.GetPullsRequest{}))

	s.Require().NoError(err)
	s.Len(resp.Msg.Board.Pulls, 1)
	s.Equal(1, s.calls)
}

func (s *PullsServiceSuite) TestSetAuthorsSavesTheFilterAndReturnsTheBoardUnderIt() {
	s.p.Sync(s.T().Context())

	resp, err := s.svc.SetAuthors(s.T().Context(), connect.NewRequest(&pullsv1.SetAuthorsRequest{Authors: []string{"grace"}}))

	s.Require().NoError(err)
	s.Equal([]string{"grace"}, resp.Msg.Board.Filter.Authors)
	s.False(resp.Msg.Board.Filter.IsDefault)
	s.Require().Len(resp.Msg.Board.Pulls, 1)
	s.EqualValues(2, resp.Msg.Board.Pulls[0].Number)
}

func (s *PullsServiceSuite) TestSetAuthorsAsksForOneSyncWithoutCallingTheForgeItself() {
	_, err := s.svc.SetAuthors(s.T().Context(), connect.NewRequest(&pullsv1.SetAuthorsRequest{}))

	s.Require().NoError(err)
	s.Zero(s.calls)
	select {
	case <-s.p.kick:
	default:
		s.Fail("no sync was requested")
	}
}

func (s *PullsServiceSuite) TestSetAuthorsEmptyMeansEveryAuthor() {
	s.p.Sync(s.T().Context())

	resp, err := s.svc.SetAuthors(s.T().Context(), connect.NewRequest(&pullsv1.SetAuthorsRequest{}))

	s.Require().NoError(err)
	s.True(resp.Msg.Board.Filter.EveryAuthor)
	s.Len(resp.Msg.Board.Pulls, 2)
}
