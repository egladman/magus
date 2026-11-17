package job

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type StoreGrantsSuite struct {
	suite.Suite
	store *Store
}

func TestStoreGrantsSuite(t *testing.T) { suite.Run(t, new(StoreGrantsSuite)) }

func (s *StoreGrantsSuite) SetupTest() {
	st, err := Open(s.T().TempDir(), WithOrigin(func(context.Context) Origin { return Origin{User: "eli"} }))
	s.Require().NoError(err)
	s.store = st
	_, err = st.Update(s.T().Context(), "a", func(r *Job) { r.State = StateRunning })
	s.Require().NoError(err)
}

func (s *StoreGrantsSuite) TestAGrantLowersItsRuleWhileTheJobRuns() {
	g, err := s.store.Grant(s.T().Context(), "a", "hooks-off", 0)
	s.Require().NoError(err)
	s.Equal("eli", g.By.User)

	rows, err := s.store.List()
	s.Require().NoError(err)
	_, live, err := s.store.LiveGrant(rows[0], "hooks-off", time.Now().Unix())
	s.Require().NoError(err)
	s.True(live)
}

func (s *StoreGrantsSuite) TestARevokedGrantLowersNothing() {
	_, err := s.store.Grant(s.T().Context(), "a", "hooks-off", 0)
	s.Require().NoError(err)
	_, err = s.store.Revoke(s.T().Context(), "a", "hooks-off")
	s.Require().NoError(err)

	rows, err := s.store.List()
	s.Require().NoError(err)
	_, live, err := s.store.LiveGrant(rows[0], "hooks-off", time.Now().Unix())
	s.Require().NoError(err)
	s.False(live)
}

func (s *StoreGrantsSuite) TestAHolderGrantsNothing() {
	_, err := s.store.As(Actor{Lease: "a"}).Grant(s.T().Context(), "a", "hooks-off", 0)

	var refused *GrantRefusedError
	s.Require().ErrorAs(err, &refused)
	s.Equal("a", refused.Lease)
}

func (s *StoreGrantsSuite) TestAGrantOfAnUnknownJobIsRefused() {
	_, err := s.store.Grant(s.T().Context(), "nobody", "hooks-off", 0)

	s.ErrorIs(err, ErrUnknownJob)
}
