package pulls

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/suite"

	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
	"github.com/egladman/magus/types"
)

const checkoutRecordJSON = `{"path":"/cache/checkout-abc1234","revision":"abc1234def","vcs":"git",` +
	`"bootstrap":{"ran":true,"install":["go","console"],"harnesses":true,"hook":"hack/dev/bootstrap-checkout.buzz"},` +
	`"remove":"magus vcs checkout --remove /cache/checkout-abc1234"}`

type CheckoutSuite struct {
	suite.Suite
	svc  *Service
	args []string
	out  string
	err  error
}

func TestCheckoutSuite(t *testing.T) { suite.Run(t, new(CheckoutSuite)) }

func (s *CheckoutSuite) SetupTest() {
	s.args, s.out, s.err = nil, checkoutRecordJSON, nil
	store := OpenStore(filepath.Join(s.T().TempDir(), "authors.jsonl"))
	pull := newPull(7, "ada")
	pull.HeadSHA = "abc1234def"
	pull.FetchRef = "refs/pull/7/head"
	poller := NewPoller(PollerOptions{
		Config: testConfig(),
		Store:  store,
		List: func(context.Context, types.ListPullsRequest) (types.PullList, error) {
			return types.PullList{Viewer: "ada", Pulls: []types.ForgePull{pull}}, nil
		},
		Remote: func(context.Context) string { return "r" },
		Now:    func() time.Time { return testNow },
	})
	poller.Sync(s.T().Context())
	s.svc = NewService(poller, store)
	s.svc.run = func(_ context.Context, args ...string) ([]byte, error) {
		s.args = args
		return []byte(s.out), s.err
	}
}

func (s *CheckoutSuite) TestRunsVcsCheckoutWithTheHeadAndFetchRef() {
	_, err := s.svc.CheckoutPull(s.T().Context(), connect.NewRequest(&pullsv1.CheckoutPullRequest{Number: 7}))

	s.Require().NoError(err)
	s.Equal([]string{"vcs", "checkout", "abc1234def", "--fetch", "refs/pull/7/head", "-o", "json"}, s.args)
}

func (s *CheckoutSuite) TestReturnsThePathTheRemoveCommandAndTheLog() {
	resp, err := s.svc.CheckoutPull(s.T().Context(), connect.NewRequest(&pullsv1.CheckoutPullRequest{Number: 7}))

	s.Require().NoError(err)
	s.Equal("/cache/checkout-abc1234", resp.Msg.Path)
	s.Equal("magus vcs checkout --remove /cache/checkout-abc1234", resp.Msg.Remove)
	s.Equal([]string{
		"checked out abc1234 with git",
		"installed: go, console",
		"harnesses installed",
		"hook ran: hack/dev/bootstrap-checkout.buzz",
	}, resp.Msg.Log)
	s.Empty(resp.Msg.Error)
}

func (s *CheckoutSuite) TestAnUnknownNumberIsAnErrorNamingIt() {
	_, err := s.svc.CheckoutPull(s.T().Context(), connect.NewRequest(&pullsv1.CheckoutPullRequest{Number: 99}))

	s.Require().Error(err)
	s.Equal(connect.CodeNotFound, connect.CodeOf(err))
	s.Contains(err.Error(), "#99")
	s.Nil(s.args)
}

func (s *CheckoutSuite) TestAFailedRunReturnsItsStderrAsTheError() {
	s.out, s.err = "", errors.New("fatal: couldn't find remote ref refs/pull/7/head")

	resp, err := s.svc.CheckoutPull(s.T().Context(), connect.NewRequest(&pullsv1.CheckoutPullRequest{Number: 7}))

	s.Require().NoError(err)
	s.Equal("fatal: couldn't find remote ref refs/pull/7/head", resp.Msg.Error)
	s.Empty(resp.Msg.Path)
}

func (s *CheckoutSuite) TestARecordThatIsNotJSONIsAnError() {
	s.out = "not json"

	resp, err := s.svc.CheckoutPull(s.T().Context(), connect.NewRequest(&pullsv1.CheckoutPullRequest{Number: 7}))

	s.Require().NoError(err)
	s.Contains(resp.Msg.Error, "read the checkout record")
}

func (s *CheckoutSuite) TestABootstrapThatDidNotRunSaysSo() {
	s.out = `{"path":"/p","revision":"abc1234def","vcs":"git","bootstrap":{"ran":false},"remove":"r"}`

	resp, err := s.svc.CheckoutPull(s.T().Context(), connect.NewRequest(&pullsv1.CheckoutPullRequest{Number: 7}))

	s.Require().NoError(err)
	s.Equal([]string{"checked out abc1234 with git", "bootstrap did not run"}, resp.Msg.Log)
}
