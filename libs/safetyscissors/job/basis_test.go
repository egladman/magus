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

func (s *BasisSuite) TestAStoredBasisIsSpelledAndReadBack() {
	var bad wireReads
	wire := wireBasis()

	s.Equal("Witnessed", wire.spell(BasisWitnessed))
	s.Equal(BasisReported, wire.read("Reported", &bad))
	s.Require().NoError(bad.refusal())
	wire.read("Trusted", &bad)
	s.ErrorContains(bad.refusal(), `"Trusted" is not a stored Basis`)
}
