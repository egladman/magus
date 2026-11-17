package guard

import (
	"os"
	"path/filepath"
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

type StaleToolRowsSuite struct {
	suite.Suite
	env HookEnv
}

func TestStaleToolRowsSuite(t *testing.T) { suite.Run(t, new(StaleToolRowsSuite)) }

func (s *StaleToolRowsSuite) SetupTest() {
	root := s.T().TempDir()
	dir := filepath.Join(root, ".claude", "magus", "tools")
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, types.ToolHostFile), []byte("claude-code\n"), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, types.ToolVersionFile), []byte("13\n"), 0o644))
	s.env = HookEnv{FindRoot: func(string) (string, error) { return root, nil }}
}

func (s *StaleToolRowsSuite) TestRowsOfAnotherStampRefuseWithTheInstallStep() {
	req := Request{Host: "claude-code"}
	_, known, refusal := s.env.applyHostFacts(s.T().Context(), &req)

	s.False(known)
	s.Require().NotNil(refusal)
	s.Require().Error(refusal.err)
	s.Equal(types.NextStep{
		ID:   "harness-install",
		Run:  "magus agent harness install --id claude-code",
		Argv: []string{"magus", "agent", "harness", "install", "--id", "claude-code"},
		Why:  "installs the tool rows of claude-code, when the workspace loads that harness",
	}, refusal.next)
}
