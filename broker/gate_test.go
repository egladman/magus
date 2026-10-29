package broker

import (
	"errors"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
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
	v, err := c.Claim(s.T().Context(), claim)
	s.Require().NoError(err)
	s.Require().True(v.Granted, "%+v", v)
	return v.ID
}

func (s *AttributionSuite) TestWithLeaseOutranksBaggage() {
	s.T().Setenv(trail.EnvBaggage, "magus.lease=from-baggage")
	s.Equal("from-baggage", NewClient(s.addr).hello.Lease)
	s.Equal("from-marker", NewClient(s.addr, WithLease("from-marker")).hello.Lease)
}

func (s *AttributionSuite) TestTheOldestGateWaitsForAnotherInvocationsSeat() {
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

func (s *AttributionSuite) TestAYoungerGateIsRefusedAtOnce() {
	older := s.client(WithGate(true))
	s.grant(older, types.MachineClaim{Project: ".", Target: "lint", Slots: 2, PID: 3})
	younger := s.client(WithGate(true))

	v, err := younger.Claim(s.T().Context(), types.MachineClaim{Project: "docs", Target: "lint", Slots: 1, PID: 4})
	s.Require().NoError(err)
	s.Equal([]bool{false, true, false}, []bool{v.Granted, v.Fits, v.OwnRun},
		"only the oldest gate waits, so two gates never wait on each other")
}

func (s *AttributionSuite) TestAWaitingGateKeepsAFeatureCheckOutOfItsRoom() {
	feature := s.client()
	first := s.grant(feature, types.MachineClaim{Project: "libs/x", Target: "go-test", Slots: 1, PID: 1})
	second := s.grant(feature, types.MachineClaim{Project: "libs/x", Target: "lint", Slots: 1, PID: 1})
	gate := s.client(WithGate(true))
	member := claimAsync(s.T(), s.T().Context(), s.addr, gate, types.MachineClaim{Project: ".", Target: "go-test", Slots: 2, PID: 3})

	feature.Release(s.T().Context(), first)
	requirePending(s.T(), member)
	late := s.client()
	v, err := late.Claim(s.T().Context(), types.MachineClaim{Project: "libs/y", Target: "go-test", Slots: 1, PID: 5})
	s.Require().NoError(err)
	s.False(v.Granted, "the freed slot is the gate's, so the feature check is refused")

	feature.Release(s.T().Context(), second)
	s.True(requireAnswer(s.T(), member).v.Granted)
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

func (s *AttributionSuite) TestTheGateGivesUpAtItsBoundNamingIt() {
	bound := gateWaitBound
	gateWaitBound = 100 * time.Millisecond
	s.T().Cleanup(func() { gateWaitBound = bound })
	feature := s.client()
	s.grant(feature, types.MachineClaim{Project: "libs/x", Target: "go-test", Slots: 2, PID: 1})
	gate := s.client(WithGate(true))

	got := requireAnswer(s.T(), claimAsync(s.T(), s.T().Context(), s.addr, gate, types.MachineClaim{Project: ".", Target: "go-test", Slots: 1, PID: 3}))
	var exit types.ExitError
	s.Require().True(errors.As(got.err, &exit), "%v", got.err)
	s.Equal(cache.ExitCodeMachineBusy, exit.Code)
	s.Contains(got.err.Error(), "the most a gate waits")
	s.Contains(got.err.Error(), "go-test")
}
