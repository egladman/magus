package broker

import (
	"testing"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// AttributionSuite pins who a run's claims answer to: the lease its checkout binds, and
// whether it is a gate, which outranks every other run for seats (decisions 101 and 102
// of the 2026-10-27 build-budget report).
type AttributionSuite struct {
	suite.Suite
	addr string
}

func TestAttributionSuite(t *testing.T) { suite.Run(t, new(AttributionSuite)) }

func (s *AttributionSuite) SetupTest() {
	s.addr = testAddr(s.T())
	serve(s.T(), s.addr, WithCapacity(0, 2))
}

func (s *AttributionSuite) client(opts ...ClientOption) *Client {
	c, err := Dial(s.T().Context(), s.addr, opts...)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = c.Close() })
	return c
}

func (s *AttributionSuite) grant(c *Client, claim types.MachineClaim) string {
	v, err := c.Claim(s.T().Context(), claim, nil)
	s.Require().NoError(err)
	s.Require().True(v.Granted, "%+v", v)
	return v.ID
}

func (s *AttributionSuite) TestWithLeaseOutranksBaggage() {
	s.T().Setenv(trail.EnvBaggage, "magus.lease=from-baggage")
	s.Equal("from-baggage", NewClient(s.addr).hello.Lease)
	s.Equal("from-marker", NewClient(s.addr, WithLease("from-marker")).hello.Lease)
}

func (s *AttributionSuite) TestAGateWaitsForAnotherInvocationsSeat() {
	feature := s.client()
	held := s.grant(feature, types.MachineClaim{Project: "libs/x", Target: "go-test", Slots: 2, PID: 1})
	gate := s.client(WithGate(true))

	member := claimAsync(s.T(), s.T().Context(), s.addr, gate, types.MachineClaim{Project: ".", Target: "go-test", Slots: 1, PID: 3})
	requirePending(s.T(), member)

	feature.Release(s.T().Context(), held)
	got := requireAnswer(s.T(), member)
	s.Require().NoError(got.err)
	s.True(got.v.Granted, "the release seats the gate's member")
}

func (s *AttributionSuite) TestAYoungerGateWaitsBehindTheOlder() {
	older := s.client(WithGate(true))
	held := s.grant(older, types.MachineClaim{Project: ".", Target: "lint", Slots: 2, PID: 3})
	younger := s.client(WithGate(true))

	waiting := claimAsync(s.T(), s.T().Context(), s.addr, younger, types.MachineClaim{Project: "docs", Target: "lint", Slots: 1, PID: 4})
	requirePending(s.T(), waiting)

	older.Release(s.T().Context(), held)
	s.True(requireAnswer(s.T(), waiting).v.Granted, "the older gate's release seats the younger")
}

func (s *AttributionSuite) TestTwoGatesOnDifferentRootsBothFinish() {
	first, second := s.client(WithGate(true)), s.client(WithGate(true))
	a := s.grant(first, types.MachineClaim{Project: ".", Target: "ci", Slots: 1, PID: 3, Invocation: "3:a"})
	b := s.grant(second, types.MachineClaim{Project: ".", Target: "ci", Slots: 1, PID: 4, Invocation: "4:b"})
	firstMember := claimAsync(s.T(), s.T().Context(), s.addr, first, types.MachineClaim{Project: ".", Target: "go-test", Slots: 1, PID: 3, Invocation: "3:a2", Ancestors: []string{"3:a"}})
	secondMember := claimAsync(s.T(), s.T().Context(), s.addr, second, types.MachineClaim{Project: ".", Target: "go-test", Slots: 1, PID: 4, Invocation: "4:b2", Ancestors: []string{"4:b"}})

	for _, got := range []claimed{requireAnswer(s.T(), firstMember), requireAnswer(s.T(), secondMember)} {
		s.Require().NoError(got.err)
		s.True(got.v.Granted, "each stalled root seats one child, so neither waits on the other")
	}
	first.Release(s.T().Context(), a)
	second.Release(s.T().Context(), b)
}

func (s *AttributionSuite) TestAWaitingGateKeepsAFeatureCheckOutOfItsRoom() {
	feature := s.client()
	first := s.grant(feature, types.MachineClaim{Project: "libs/x", Target: "go-test", Slots: 1, PID: 1})
	second := s.grant(feature, types.MachineClaim{Project: "libs/x", Target: "lint", Slots: 1, PID: 1})
	gate := s.client(WithGate(true))
	member := claimAsync(s.T(), s.T().Context(), s.addr, gate, types.MachineClaim{Project: ".", Target: "go-test", Slots: 2, PID: 3})

	feature.Release(s.T().Context(), first)
	requirePending(s.T(), member)
	late := claimAsync(s.T(), s.T().Context(), s.addr, s.client(), types.MachineClaim{Project: "libs/y", Target: "go-test", Slots: 1, PID: 5})
	requirePending(s.T(), late)

	feature.Release(s.T().Context(), second)
	s.True(requireAnswer(s.T(), member).v.Granted, "the freed slots are the gate's")
	requirePending(s.T(), late)
}

func (s *AttributionSuite) TestAMemberOfTheGatesRunWaitsWithIt() {
	gate := s.client(WithGate(true))
	s.grant(gate, types.MachineClaim{Project: ".", Target: "ci", Slots: 1, PID: 3, Invocation: "inv-gate"})
	feature := s.client()
	held := s.grant(feature, types.MachineClaim{Project: "libs/x", Target: "go-test", Slots: 1, PID: 1})
	nested := s.client()
	s.grant(nested, types.MachineClaim{Project: "docs", Target: "format", Slots: 1, PID: 6, Invocation: "inv-nested", Ancestors: []string{"inv-gate"}})

	member := claimAsync(s.T(), s.T().Context(), s.addr, nested, types.MachineClaim{
		Project: "docs", Target: "lint", Slots: 1, PID: 7, Invocation: "inv-nested-2", Ancestors: []string{"inv-gate"},
	})
	requirePending(s.T(), member)

	feature.Release(s.T().Context(), held)
	s.True(requireAnswer(s.T(), member).v.Granted, "a magus the gate runs is the gate's")
}
