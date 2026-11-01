package job

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type treeOrderSuite struct {
	suite.Suite
}

func TestTreeOrder(t *testing.T) { suite.Run(t, new(treeOrderSuite)) }

func (s *treeOrderSuite) TestChildrenNestUnderTheirParent() {
	plan := types.Job{ID: "plan"}
	core := types.Job{ID: "plan/core", Parent: "plan"}
	other := types.Job{ID: "other"}
	deep := types.Job{ID: "plan/core/deep", Parent: "plan/core"}

	s.Equal([]treeLine{
		{Job: plan, Depth: 0},
		{Job: core, Depth: 1},
		{Job: deep, Depth: 2},
		{Job: other, Depth: 0},
	}, TreeOrder([]types.Job{plan, core, other, deep}))
}

func (s *treeOrderSuite) TestAClearedParentAndACycleKeepTheirRowsAtDepthZero() {
	rows := []types.Job{
		{ID: "orphan", Parent: "cleared-parent"},
		{ID: "a", Parent: "b"},
		{ID: "b", Parent: "a"},
	}

	got := TreeOrder(rows)

	s.Require().Len(got, len(rows))
	for _, line := range got {
		s.Zero(line.Depth, line.Job.ID)
	}
}
