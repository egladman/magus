package e2e

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// Suite is the base of a suite that drives a real installed program.
// Testify runs a suite's methods serially, so to drive programs in
// parallel give each its own suite and run it from a parallel t.Run.
type Suite struct {
	suite.Suite
	owner *testing.T
}

// SetupSuite records the test suite.Run was given. A suite that defines
// its own SetupSuite calls this one first.
func (s *Suite) SetupSuite() { s.owner = s.T() }

// Owner is the test suite.Run was given. A fixture that outlives the
// method building it registers its cleanup here.
func (s *Suite) Owner() *testing.T { return s.owner }

// RequireBinary skips the running test when b was not found, and says why.
func (s *Suite) RequireBinary(b Binary) {
	if !b.Found() {
		s.T().Skipf("%s: %s", b.Name, b.Note)
	}
}
