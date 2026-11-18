package job

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type BasisSuite struct{ suite.Suite }

func TestBasisSuite(t *testing.T) { suite.Run(t, new(BasisSuite)) }

func (s *BasisSuite) TestTheBasesAreClosedAndStrongestFirst() {
	s.Equal([]string{"witnessed", "attested", "reported"}, BasisWitnessed.Values())
	s.True(BasisAttested.Valid())
	s.False(Basis("trusted").Valid())
	s.Equal("unset", Basis("").String())
}

func (s *BasisSuite) TestAStoredBasisIsItsGoValue() {
	var bad wireReads
	wire := wireBasis()

	s.Equal(BasisReported, wire.read("reported", &bad))
	s.Require().NoError(bad.refusal())
	wire.read("Reported", &bad)
	s.ErrorContains(bad.refusal(), `"Reported" is not a stored Basis`)
}
