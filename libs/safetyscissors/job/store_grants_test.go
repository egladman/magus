package job

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

type StoreGrantsSuite struct {
	suite.Suite
	store *Store
}

func TestStoreGrantsSuite(t *testing.T) { suite.Run(t, new(StoreGrantsSuite)) }

func (s *StoreGrantsSuite) SetupTest() {
	st, err := Open(s.T().TempDir(), writable(WithOrigin(func(context.Context) Origin { return Origin{User: "eli"} }))...)
	s.Require().NoError(err)
	s.store = st
	_, err = st.Update(s.T().Context(), "a", func(r *Job) { r.State = StateRunning })
	s.Require().NoError(err)
}

// lowers reports whether job a's last grant of rule lowers it now.
func (s *StoreGrantsSuite) lowers(rule string) bool {
	ctx := s.T().Context()
	rows, err := s.store.List(ctx)
	s.Require().NoError(err)
	grants, err := s.store.Grants(ctx)
	s.Require().NoError(err)
	tries, err := s.store.Attempts(ctx)
	s.Require().NoError(err)
	attempt := NewAttempts(tries).LiveN(rows[0].ID)
	for _, g := range grants {
		if g.Rule == rule && g.Lowers(rows[0], attempt, s.store.Now().Unix()) {
			return true
		}
	}
	return false
}

func (s *StoreGrantsSuite) TestAGrantsLogIsReadableByItsOwnerAlone() {
	_, err := s.store.Grant(s.T().Context(), "a", "hooks-off", 0)
	s.Require().NoError(err)
	path, err := s.store.Path()
	s.Require().NoError(err)

	info, err := os.Stat(filepath.Join(filepath.Dir(path), grantsFileName))

	s.Require().NoError(err)
	s.Equal(os.FileMode(0o600), info.Mode().Perm())
}

func (s *StoreGrantsSuite) TestAGrantLowersItsRuleWhileTheJobRuns() {
	g, err := s.store.Grant(s.T().Context(), "a", "hooks-off", 0)
	s.Require().NoError(err)
	s.Equal("eli", g.By.User)

	s.True(s.lowers("hooks-off"))
}

func (s *StoreGrantsSuite) TestARevokedGrantLowersNothing() {
	_, err := s.store.Grant(s.T().Context(), "a", "hooks-off", 0)
	s.Require().NoError(err)
	_, err = s.store.Revoke(s.T().Context(), "a", "hooks-off")
	s.Require().NoError(err)

	s.False(s.lowers("hooks-off"))
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
