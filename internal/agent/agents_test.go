package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

func TestHarnessShippedAgentsAreCompleteAndHostNeutral(t *testing.T) {
	agents, err := ShippedAgents()
	require.NoError(t, err)
	require.NotEmpty(t, agents)

	for _, a := range agents {
		t.Run(a.Name, func(t *testing.T) {
			assert.NotEmpty(t, a.Description)
			assert.NotEmpty(t, a.Instructions)
			assert.True(t, strings.HasSuffix(a.Instructions, "\n"))
			for _, model := range []string{"claude", "haiku", "sonnet", "opus", "gpt", "codex"} {
				assert.NotContains(t, strings.ToLower(a.Instructions+a.Description), model, "an agent names no model")
			}
		})
	}
}

func TestHarnessScoutIsReadOnly(t *testing.T) {
	agents, err := ShippedAgents()
	require.NoError(t, err)
	require.Len(t, agents, 1)
	scout := agents[0]
	for _, command := range []string{"magus query", "magus refs", "magus describe", "magus affected --explain", "magus query output"} {
		assert.Contains(t, scout.Instructions, command)
	}
	// The body is prose checked above; the rest of the record is compared whole.
	scout.Instructions = ""
	assert.Equal(t, Agent{Name: "magus-scout", Description: agentDefs[0].Description, ReadOnly: true}, scout)
}

type scoutSuite struct {
	suite.Suite
	scout Agent
}

func TestScoutSuite(t *testing.T) { suite.Run(t, new(scoutSuite)) }

func (s *scoutSuite) SetupTest() {
	agents, err := ShippedAgents()
	s.Require().NoError(err)
	s.Require().Len(agents, 1)
	s.scout = agents[0]
}

func (s *scoutSuite) skill(form types.SkillForm) string {
	body, err := Default(6).SkillBytes("magus-multi-agent", form)
	s.Require().NoError(err)
	return string(body)
}

func scoutCommandNames() []string {
	names := make([]string, 0, len(scoutCommands))
	for _, c := range scoutCommands {
		names = append(names, c.Command)
	}
	return names
}

func (s *scoutSuite) TestAllowlistRendersFromTheOneSlice() {
	s.Require().NotEmpty(scoutCommands)
	for _, c := range scoutCommands {
		s.Contains(s.scout.Instructions, fmt.Sprintf("`%s`", c.Command))
	}
	s.NotContains(s.scout.Instructions, "{{")
}

func (s *scoutSuite) TestJobExitIsAllowed() {
	s.Contains(scoutCommandNames(), "magus job exit")
}

func (s *scoutSuite) TestEveryWorkerVerbTheSkillMandatesIsAllowed() {
	for _, verb := range []string{"magus describe", "magus job exit"} {
		s.Contains(scoutCommandNames(), verb)
	}
}

func (s *scoutSuite) TestScoutFilesItsRowLast() {
	s.Contains(s.scout.Instructions, "`magus job exit <your row> --stdin`")
	s.Contains(s.scout.Instructions, "last act")
	s.Contains(s.scout.Instructions, "the shape `magus job exec <your row>` printed")
}

func (s *scoutSuite) TestScoutMayGrepALiteralAfterTheGraphFindsNothing() {
	s.Contains(s.scout.Instructions, "git grep -n -F '<literal>'")
	s.Contains(s.scout.Instructions, "text search")
}

func (s *scoutSuite) TestGuardAdviceIsNotAPreconditionOrPermission() {
	s.Contains(s.scout.Instructions, "Guard advice is not a precondition and not a permission; only a refusal blocks.")
}

func (s *scoutSuite) TestSkillNeverSaysAProseScoutEndsNoReturnByDesign() {
	for _, form := range []types.SkillForm{types.SkillFormFull, types.SkillFormShort} {
		s.NotContains(s.skill(form), "no_return by design")
	}
	s.Contains(s.skill(types.SkillFormFull), "files a result")
}

func (s *scoutSuite) TestEconomyIsForLookupsTheGraphAnswers() {
	full := s.skill(types.SkillFormFull)
	s.Contains(full, "lookups the graph answers")
	s.NotContains(full, "and read-only evidence gathering")
	s.Contains(full, "tracing behavior through code")
}

func (s *scoutSuite) TestBriefsFileTheShapeJobExecPrinted() {
	s.Contains(s.skill(types.SkillFormFull), "the shape `magus job exec <row>` printed")
}
