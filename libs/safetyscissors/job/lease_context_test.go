package job

import (
	"context"
	"errors"
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
	ctx, err := WithLease(s.T().Context(), "area/fix-parser")
	s.Require().NoError(err)
	s.Equal("area/fix-parser", LeaseFromContext(ctx))
}

func (s *LeaseContextSuite) TestAnInvalidLeaseIsRefused() {
	outer, err := WithLease(s.T().Context(), "outer")
	s.Require().NoError(err)

	ctx, err := WithLease(outer, "Not A Lease")

	refused, ok := errors.AsType[*InvalidLeaseError](err)
	s.Require().True(ok, err)
	s.Equal("Not A Lease", refused.Lease)
	s.Equal("outer", LeaseFromContext(ctx), "a refused lease leaves ctx as it was")
}

func (s *LeaseContextSuite) TestNoLeaseOverridesAnOuterOne() {
	outer, err := WithLease(s.T().Context(), "outer")
	s.Require().NoError(err)

	for _, ctx := range []context.Context{WithoutLease(outer), s.leased(outer, "")} {
		lease, ok := LookupLease(ctx)
		s.True(ok, "no lease still answers, so a reader does not fall back to the environment")
		s.Empty(lease)
	}
}

func (s *LeaseContextSuite) leased(ctx context.Context, lease string) context.Context {
	ctx, err := WithLease(ctx, lease)
	s.Require().NoError(err)
	return ctx
}
