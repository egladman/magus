package job

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

type CheckLineSuite struct{ suite.Suite }

func TestCheckLine(t *testing.T) { suite.Run(t, new(CheckLineSuite)) }

// The rendered line is what the guard and the console still read, so it has to survive a
// round trip through the record the store keeps.
func (s *CheckLineSuite) TestATargetCheckRoundTripsThroughItsLine() {
	want := types.LeaseCheck{Target: "go::go-test", Project: ".", Args: []string{"-run", "Ledger"}}
	s.Equal("magus run go::go-test . -- -run Ledger", CheckCommand(want))
	got, err := ParseCheckCommand(CheckCommand(want))
	s.Require().NoError(err)
	s.Equal(want, got)
}

func (s *CheckLineSuite) TestAnEmptyProjectRendersAsTheRoot() {
	s.Equal("magus run lint . --no-default-charms", CheckCommand(types.LeaseCheck{Target: "lint", NoDefaultCharms: true}))
}

func (s *CheckLineSuite) TestAScriptCheckRendersAsARecordedRun() {
	want := types.LeaseCheck{Script: "probes/key.buzz", Args: []string{"--deep"}}
	s.Equal("magus buzz --record probes/key.buzz -- --deep", CheckCommand(want))
	got, err := ParseCheckCommand(CheckCommand(want))
	s.Require().NoError(err)
	s.Equal(want, got)
	s.Equal("magus buzz -t probes/key.buzz", CheckCommand(types.LeaseCheck{Script: "probes/key.buzz", Test: true}))
}

func (s *CheckLineSuite) TestASetOfRunsIsNotOneCheck() {
	_, err := ParseCheckCommand("magus affected ci --no-default-charms")
	var refused *checkCommandError
	s.Require().ErrorAs(err, &refused, "a set of runs is not one run, so no ref can be bound to it")
	s.False(refused.Script)
	s.Contains(err.Error(), "is not one")
	_, err = ParseCheckCommand("magus buzz --record probe.buzz extra")
	s.Require().ErrorAs(err, &refused)
	s.True(refused.Script)
}

func (s *CheckLineSuite) TestARecordedCommandQualifiesItsTargetWithTheSpell() {
	s.Equal("magus run go::go-test cmd/magus -- -run X",
		recordedCommand(job.CapturedRun{Spell: "go", Target: "go-test", Project: "cmd/magus", ExtraArgs: []string{"-run", "X"}}))
}

func (s *CheckLineSuite) TestAStoreWriteRendersValidationFromCheck() {
	s.Empty(validationLine(job.Job{}))
	s.Equal("magus run test .", validationLine(job.Job{Check: rawOf(types.LeaseCheck{Target: "test"})}))
}
