package guard

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

// RootHeldRowSuite pins what a parentless integrate row's holder may do.
type RootHeldRowSuite struct {
	suite.Suite
}

func TestRootHeldRowSuite(t *testing.T) { suite.Run(t, new(RootHeldRowSuite)) }

func (s *RootHeldRowSuite) holder(parent string, kids ...types.Job) holder {
	row := types.Job{ID: "integrate", Parent: parent, Integrates: []string{"done-a"}, State: types.StateRunning}
	st := leaseStanding{readable: true, declared: true, state: row.State, row: row, rows: append([]types.Job{row}, kids...)}
	return holder{id: row.ID, standing: func() leaseStanding { return st }, storeLease: func() string { return row.ID }}
}

func (s *RootHeldRowSuite) TestARootHoldingARowForksASibling() {
	got := childForkRebind(childFork{id: "next-row", readOnly: true}, s.holder(""), "declare a job", "fork it")
	s.False(got.refuses(), got.problem)
}

func (s *RootHeldRowSuite) TestARootEndsItsRowWithLiveChildren() {
	kid := types.Job{ID: "kid", Parent: "integrate", State: types.StateRunning}
	cmds, ok := ParseCommands("magus job exit integrate --stdin")
	s.Require().True(ok)
	got := leaseRebind(cmds[0], "", s.holder("", kid))
	s.False(got.refuses(), got.problem)
}

func (s *RootHeldRowSuite) TestAWorkerStillCannotForkOutsideItsTree() {
	got := childForkRebind(childFork{id: "next-row", readOnly: true}, s.holder("orchestrator"), "declare a job", "fork it")
	s.True(got.refuses())
}
