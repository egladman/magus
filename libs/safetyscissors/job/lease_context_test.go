package job

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type LeaseContextSuite struct{ suite.Suite }

func TestLeaseContext(t *testing.T) { suite.Run(t, new(LeaseContextSuite)) }

func (s *LeaseContextSuite) TestAContextWithNoAnswerLeavesTheEnvironmentToAReader() {
	lease, ok := LookupLease(s.T().Context())
	s.False(ok)
	s.Empty(lease)
}

func (s *LeaseContextSuite) TestAValidLeaseIsCarried() {
	ctx := WithLease(s.T().Context(), "area/fix-parser")
	s.Equal("area/fix-parser", LeaseFromContext(ctx))
}

func (s *LeaseContextSuite) TestAnInvalidLeaseAnswersNoLease() {
	ctx := WithLease(WithLease(s.T().Context(), "outer"), "Not A Lease")
	lease, ok := LookupLease(ctx)
	s.True(ok, "an invalid id still answers, so a reader does not fall back to the environment")
	s.Empty(lease, "and it overrides the outer lease")
}
