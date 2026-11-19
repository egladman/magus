package job

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type MutateSuite struct {
	suite.Suite
}

func TestMutateSuite(t *testing.T) { suite.Run(t, new(MutateSuite)) }

func (s *MutateSuite) open(opts ...Option) *Store {
	st, err := Open(s.T().Context(), Config{Dir: s.T().TempDir()}, append(writable(), opts...)...)
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
	rows, err := st.List(s.T().Context())
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
	tries, err := st.Attempts(s.T().Context())
	s.Require().NoError(err)
	s.Require().Len(tries, 1)
	s.Equal(StateFail, tries[0].Status.State, "the ended attempt was folded")
}

// The fold is appended while the row's lock is still held, so no other writer reads the
// attempts between the row and its fold.
func (s *MutateSuite) TestAReforksFoldIsAppendedUnderTheRowsLock() {
	st := s.open(WithWriteHook(func(_ context.Context, e *WriteEvent) error {
		if e.Stage == StageRefork {
			fold := e.Attempts.Live(e.ID)
			fold.Status.State = e.Prev.State
			e.Fold = &fold
		}
		return nil
	}))
	ctx := s.T().Context()
	_, err := st.Update(ctx, "a", func(r *Job) { r.State = StateFail })
	s.Require().NoError(err)
	path, err := st.Path()
	s.Require().NoError(err)

	attemptsHeld, releaseAttempts := make(chan struct{}), make(chan struct{})
	go func() {
		_ = codec.WithLock(ctx, filepath.Join(filepath.Dir(path), attemptsFileName)+".lock", time.Second, func() error {
			close(attemptsHeld)
			<-releaseAttempts
			return nil
		})
	}()
	<-attemptsHeld
	done := make(chan error, 1)
	go func() {
		_, err := st.Update(ctx, "a", func(r *Job) { r.State = StateDeclared })
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)

	probe := codec.WithLock(ctx, path+".lock", 0, func() error { return nil })
	close(releaseAttempts)

	s.Require().NoError(<-done)
	s.Error(probe, "the row's lock is held while the fold waits on the attempts")
}

func (s *MutateSuite) TestAForkPastItsCapIsRefusedWithNoHook() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(r *Job) { r.State, r.MaxAttempts = StateFail, 1 })
	s.Require().NoError(err)
	s.Require().NoError(st.appendAttempts(s.T().Context(), Attempt{Job: "a", N: 1}))

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

	for want, next := range map[int]Job{2: widened, 3: moved} {
		got, err := nextGeneration(prev, next, true, AsDeclaration, specChecks{})
		s.Require().NoError(err)
		s.Equal(want, got)
	}
	got, err := nextGeneration(Job{}, moved, false, AsDeclaration, specChecks{})
	s.Require().NoError(err)
	s.Equal(1, got)
}
