package job

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type FaultSuite struct{ suite.Suite }

func TestFault(t *testing.T) { suite.Run(t, new(FaultSuite)) }

func (s *FaultSuite) TestARecordFieldFaultsAsTheRecord() {
	fs := Declaration{ID: "worker", MaxAttempts: -1}.Faults()
	s.Require().Len(fs, 1)
	s.Equal(FaultRecord, fs[0].Kind)
	s.Equal("max_attempts", fs[0].Field)
}

func (s *FaultSuite) TestAGoalFaultsAsAGoalUnderItsIndex() {
	g := Goal{ID: "g", Kind: GoalKindSymbol, Expect: ExpectSignature, Symbols: []string{"A", "B"}}
	fs := Declaration{ID: "worker", Goals: []Goal{g}}.Faults()
	s.Require().Len(fs, 1)
	s.Equal(FaultGoal, fs[0].Kind)
	s.Equal("goals[0].symbols", fs[0].Field)
}

// The module names no code, so a deviation may permit only what the caller lists, and the
// fix names that list.
func (s *FaultSuite) TestADeviationPermitsOnlyACodeTheCallerLists() {
	d := Declaration{ID: "worker", Deviations: []Deviation{{Code: "X1", Subject: "a", Reason: "the root runs it"}}}
	s.Empty(d.Faults("X1"))
	fs := d.Faults("Y1", "Y2", "Y3")
	s.Require().Len(fs, 1)
	s.Equal("deviations[0].code", fs[0].Field)
	s.Contains(fs[0].Fix, "permit only Y1, Y2 or Y3")
}

func (s *FaultSuite) TestAFaultReadsAsItsMessageThenItsFix() {
	s.Equal("broken", Fault{Message: "broken"}.Error())
	s.Equal("broken: mend it", Fault{Message: "broken", Fix: "mend it"}.Error())
}

func (s *FaultSuite) TestOrListJoinsForASentence() {
	s.Empty(orList(nil))
	s.Equal("a", orList([]Code{"a"}))
	s.Equal("a or b", orList([]Code{"a", "b"}))
	s.Equal("a, b or c", orList([]Code{"a", "b", "c"}))
}

func (s *FaultSuite) TestARowRequiringAFeatureThisBuildLacksIsUnmet() {
	s.Nil(Job{Requires: features()}.Unmet())
	s.Equal([]string{"later"}, Job{Requires: []string{"later"}}.Unmet())
}
