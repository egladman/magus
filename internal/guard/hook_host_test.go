package guard

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

func (s *routeDenySuite) TestAHostWithNoAgentChannelGetsTheVerdictLineAlone() {
	deny := types.GuardJudgment{Decision: "deny", Reason: audienceReason}
	got := routeDeny(s.T().Context(), types.Limits{Channels: []string{"display"}}, true, deny)
	s.Equal("a text search the graph answers", got.Reason)
}

func (s *routeDenySuite) TestAHostWithAnAgentChannelKeepsTheWholeReason() {
	deny := types.GuardJudgment{Decision: "deny", Reason: audienceReason}
	got := routeDeny(s.T().Context(), types.Limits{Channels: []string{"display", "capture", "notice"}}, true, deny)
	s.Equal(audienceReason, got.Reason)
}

func (s *routeDenySuite) TestLimitsWithNoChannelsLeaveTheReasonWhole() {
	deny := types.GuardJudgment{Decision: "deny", Reason: audienceReason}
	s.Equal(audienceReason, routeDeny(s.T().Context(), types.Limits{}, true, deny).Reason)
}

func (s *routeDenySuite) TestAnAdviseIsNeverCut() {
	advise := types.GuardJudgment{Decision: "advise", Context: audienceReason}
	s.Equal(advise, routeDeny(s.T().Context(), types.Limits{Channels: []string{"display"}}, true, advise))
}

// routeDenySuite pins decision 110 per host: a deny shows the person its first line, and
// the agent gets the rest through the host's agent-only channel.
type routeDenySuite struct {
	suite.Suite
}

func TestRouteDenySuite(t *testing.T) {
	suite.Run(t, new(routeDenySuite))
}

const audienceReason = "a text search the graph answers\nnothing ran (2 commands)\ndetails: ./magus query output grd1"
