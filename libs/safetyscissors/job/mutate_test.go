package job

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
)

type MutateSuite struct {
	suite.Suite
}

func TestMutateSuite(t *testing.T) { suite.Run(t, new(MutateSuite)) }

func (s *MutateSuite) open(opts ...StoreOption) *Store {
	st, err := Open(s.T().TempDir(), opts...)
	s.Require().NoError(err)
	return st
}

func (s *MutateSuite) TestTheHookSeesEveryStageOfAWriteThatEndsARow() {
	var stages []WriteStage
	var after bool
	st := s.open(WithWriteHook(func(_ context.Context, e *WriteEvent) error {
		stages = append(stages, e.Stage)
		if e.Stage == StageEnd {
			e.Row.EndReason = "ended by the test"
		}
		if e.Stage == StagePlaced {
			e.After = append(e.After, func(context.Context) error { after = true; return nil })
		}
		return nil
	}))
	_, err := st.Update(s.T().Context(), "a", func(*Job) {})
	s.Require().NoError(err)
	stages = nil

	row, err := st.Update(s.T().Context(), "a", func(r *Job) { r.State = StateNoReturn })

	s.Require().NoError(err)
	s.Equal([]WriteStage{StageAuthorize, StageEnd, StageTier, StagePlaced}, stages)
	s.Equal("ended by the test", row.EndReason)
	s.True(after, "what the hook queued ran once the write landed")
}

func (s *MutateSuite) TestARefusingHookWritesNothing() {
	refused := errors.New("refused")
	st := s.open(WithWriteHook(func(_ context.Context, e *WriteEvent) error {
		if e.Stage == StageTier {
			return refused
		}
		return nil
	}))

	_, err := st.Update(s.T().Context(), "a", func(*Job) {})

	s.ErrorIs(err, refused)
	rows, err := st.List()
	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *MutateSuite) TestAReforkStoresTheFoldTheHookMakes() {
	st := s.open(WithWriteHook(func(_ context.Context, e *WriteEvent) error {
		if e.Stage == StageRefork {
			fold := e.Attempts.Live(e.ID)
			fold.Status.State = e.Prev.State
			e.Fold = &fold
		}
		return nil
	}))
	_, err := st.Update(s.T().Context(), "a", func(r *Job) { r.State = StateFail })
	s.Require().NoError(err)

	row, err := st.Update(s.T().Context(), "a", func(r *Job) { r.State = StateDeclared })

	s.Require().NoError(err)
	s.Equal(StateDeclared, row.State)
	tries, err := st.Attempts()
	s.Require().NoError(err)
	s.Require().Len(tries, 1)
	s.Equal(StateFail, tries[0].Status.State, "the ended attempt was folded")
}

func (s *MutateSuite) TestAForkPastItsCapIsRefusedWithNoHook() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(r *Job) { r.State, r.MaxAttempts = StateFail, 1 })
	s.Require().NoError(err)
	s.Require().NoError(st.AppendAttempts(s.T().Context(), Attempt{Job: "a", N: 1}))

	_, err = st.Update(s.T().Context(), "a", func(r *Job) { r.State = StateDeclared })

	s.ErrorIs(err, ErrMaxAttempts)
}

func (s *MutateSuite) TestAClaimIsGradedByTheHookAlone() {
	graded := false
	st := s.open(WithWriteHook(func(_ context.Context, e *WriteEvent) error {
		if e.Stage == StageAuthorize && e.Kind == AsClaim {
			graded = true
		}
		return nil
	}))
	_, err := st.Update(s.T().Context(), "a", func(r *Job) { r.State = StateRunning })
	s.Require().NoError(err)

	_, err = st.Mutate(s.T().Context(), "a", AsClaim, func(cur *Job, _ bool, _ int64) error {
		cur.Paths.Write = append(cur.Paths.Write, "b.go")
		return nil
	})

	s.Require().NoError(err)
	s.True(graded)
}

func (s *MutateSuite) TestTheOriginStampsWhoRegisteredARow() {
	st := s.open(WithOrigin(func(context.Context) Origin { return Origin{User: "eli", EntryPoint: EntryPointCLI} }))

	row, err := st.Update(s.T().Context(), "a", func(*Job) {})

	s.Require().NoError(err)
	s.Equal("eli", row.RegisteredBy.User)
}

func (s *MutateSuite) TestAnUnboundForkRecordsTheHeadAsAuthority() {
	st := s.open(WithHead(func(context.Context) string { return "abc123" }))

	row, err := st.Update(s.T().Context(), "a", func(*Job) {})

	s.Require().NoError(err)
	s.Equal("abc123", row.Authority)
}

func (s *MutateSuite) TestAWideningOfTheWritePathsKeepsTheGeneration() {
	prev := Job{Paths: Paths{Write: []string{"internal/job/store.go"}}, Generation: 2}
	widened := Job{Paths: Paths{Write: []string{"internal/**"}}}
	moved := Job{Paths: Paths{Write: []string{"docs"}}}

	s.Equal(2, nextGeneration(prev, widened, true, AsDeclaration))
	s.Equal(3, nextGeneration(prev, moved, true, AsDeclaration))
	s.Equal(1, nextGeneration(Job{}, moved, false, AsDeclaration))
}
