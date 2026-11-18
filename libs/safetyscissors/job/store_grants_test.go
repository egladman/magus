package job

import (
	"context"
	"errors"
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
	rows, err := s.store.List(s.T().Context())
	s.Require().NoError(err)
	return s.lowersAt(rows[0], rule, s.store.Now().Unix())
}

// lowersAt reports whether row's last grant of rule lowers it at now.
func (s *StoreGrantsSuite) lowersAt(row Job, rule string, now int64) bool {
	ctx := s.T().Context()
	grants, err := s.store.Grants(ctx)
	s.Require().NoError(err)
	tries, err := s.store.Attempts(ctx)
	s.Require().NoError(err)
	attempt := NewAttempts(tries).LiveN(row.ID)
	for _, g := range grants {
		if g.Rule == rule && g.Lowers(row, attempt, now) {
			return true
		}
	}
	return false
}

func (s *StoreGrantsSuite) running() Job {
	rows, err := s.store.List(s.T().Context())
	s.Require().NoError(err)
	return rows[0]
}

func (s *StoreGrantsSuite) TestTheLastLinePerJobAndRuleWins() {
	ctx := s.T().Context()
	now := s.store.Now().Unix()
	_, err := s.store.Grant(ctx, "a", "hooks-off", now+60)
	s.Require().NoError(err)
	_, err = s.store.Grant(ctx, "a", "hooks-off", 0)
	s.Require().NoError(err)

	s.True(s.lowersAt(s.running(), "hooks-off", now+120), "the second grant has no expiry and replaced the first")
	grants, err := s.store.Grants(ctx)
	s.Require().NoError(err)
	s.Require().Len(grants, 1, "one entry per job and rule")
	s.Zero(grants[0].Until)
}

func (s *StoreGrantsSuite) TestAGrantLapsesAtItsDuration() {
	now := s.store.Now().Unix()
	_, err := s.store.Grant(s.T().Context(), "a", "hooks-off", now+60)
	s.Require().NoError(err)
	row := s.running()

	s.True(s.lowersAt(row, "hooks-off", now))
	s.False(s.lowersAt(row, "hooks-off", now+60), "a grant ends at its --for")
	s.False(s.lowersAt(row, "stage-all", now), "a grant lowers only its own rule")
}

func (s *StoreGrantsSuite) TestAGrantEndsWithTheJobAndItsAttempt() {
	ctx := s.T().Context()
	_, err := s.store.Grant(ctx, "a", "hooks-off", 0)
	s.Require().NoError(err)
	row, now := s.running(), s.store.Now().Unix()

	ended := row
	ended.State = StatePass
	s.False(s.lowersAt(ended, "hooks-off", now), "a grant ends at the job's terminal state")
	s.Require().NoError(s.store.AppendAttempts(ctx, Attempt{Job: "a", N: 1, Status: AttemptStatus{State: StateFail}}))
	s.False(s.lowersAt(row, "hooks-off", now), "the next attempt is not the one the person approved")
}

func (s *StoreGrantsSuite) TestARevocationIsKeptOnRecord() {
	ctx := s.T().Context()
	_, err := s.store.Grant(ctx, "a", "hooks-off", 0)
	s.Require().NoError(err)
	_, err = s.store.Revoke(ctx, "a", "hooks-off")
	s.Require().NoError(err)

	grants, err := s.store.Grants(ctx)
	s.Require().NoError(err)
	s.Require().Len(grants, 1)
	s.True(grants[0].Revoked, "the revocation is kept, so who revoked it stays on record")
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

func (s *StoreGrantsSuite) TestAMemberANewerWriterStoredInAGrantIsRefused() {
	ctx := s.T().Context()
	path, err := s.store.Path()
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(filepath.Join(filepath.Dir(path), grantsFileName),
		[]byte(`{"job":"a","rule":"hooks-off","by":{"user":"eli"},"at":1,"paths":["pkg/**"]}`+"\n"), 0o600))

	_, err = s.store.Grants(ctx)

	refused, ok := errors.AsType[*UnknownMemberError](err)
	s.Require().True(ok, err)
	s.Equal("grantRecord", refused.Record)
	s.Equal([]string{"paths"}, refused.Members)
	_, err = s.store.Grant(ctx, "a", "hooks-off", 0)
	s.Error(err, "a grant whose write could compact the line away is refused too")
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
