package job

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

type applyDroppedGoalsSuite struct {
	suite.Suite
	store *Store
}

func TestApplyDroppedGoals(t *testing.T) { suite.Run(t, new(applyDroppedGoalsSuite)) }

func (s *applyDroppedGoalsSuite) SetupTest() {
	s.store, _ = validateWorkspace(s.T())
	_, err := Apply(s.T().Context(), s.store, []types.Declaration{s.record(
		[]string{"notes/a.txt", "notes/b.txt"}, "keep-a", "keep-b")}, config.Jobs{}, nil, nil, false)
	s.Require().NoError(err)
}

func (s *applyDroppedGoalsSuite) record(paths []string, goals ...string) types.Declaration {
	rec := types.Declaration{ID: "w", WritePaths: paths, Check: forkCheck()}
	for _, id := range goals {
		rec.Goals = append(rec.Goals, types.Goal{ID: id, Kind: types.GoalKindPaths, Paths: []string{"notes/a.txt"}})
	}
	return rec
}

func (s *applyDroppedGoalsSuite) apply(rec types.Declaration) error {
	_, err := Apply(s.T().Context(), s.store, []types.Declaration{rec}, config.Jobs{}, nil, nil, true)
	return err
}

func (s *applyDroppedGoalsSuite) TestANarrowingThatDropsGoalsIsRefused() {
	err := s.apply(s.record([]string{"notes/a.txt"}))

	s.Require().ErrorIs(err, types.JobRecordInvalid)
}

func (s *applyDroppedGoalsSuite) TestTheRefusalCarriesOneRestoreEditPerDroppedGoal() {
	rec := s.record([]string{"notes/a.txt"})

	_, edits := FixApply(s.T().Context(), s.store, []types.Declaration{rec}, config.Jobs{}, nil, nil)

	s.Require().Len(edits["w"], 2)
	s.Equal("keep-a", edits["w"][0].AddGoal.ID)
	s.Equal("keep-b", edits["w"][1].AddGoal.ID)
}

func (s *applyDroppedGoalsSuite) TestFixRestoresEveryDroppedGoalSoTheRecordApplies() {
	fixed, _ := FixApply(s.T().Context(), s.store, []types.Declaration{s.record([]string{"notes/a.txt"})}, config.Jobs{}, nil, nil)

	s.Require().NoError(s.apply(fixed[0]))
	s.Len(fixed[0].Goals, 2)
}

func (s *applyDroppedGoalsSuite) TestDroppingGoalsWithoutReleasingAPathIsKept() {
	s.Require().NoError(s.apply(s.record([]string{"notes/a.txt", "notes/b.txt"}, "keep-a")))
}

func (s *applyDroppedGoalsSuite) TestNarrowingWithTheGoalsKeptIsAccepted() {
	s.Require().NoError(s.apply(s.record([]string{"notes/a.txt"}, "keep-a", "keep-b")))
}
