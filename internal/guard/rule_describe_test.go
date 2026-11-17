package guard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type RuleDescribeSuite struct {
	suite.Suite
}

func TestRuleDescribe(t *testing.T) { suite.Run(t, new(RuleDescribeSuite)) }

func (s *RuleDescribeSuite) TestTheWorkspaceSettingOverridesTheCompiledDecision() {
	s.Equal("deny", appliedDecision(types.RuleDoc{Decision: "advise", Workspace: "deny,once"}))
	s.Equal("advise", appliedDecision(types.RuleDoc{Decision: "advise"}))
}

func (s *RuleDescribeSuite) TestTheTableTalliesByAppliedDecision() {
	var b strings.Builder
	s.Require().NoError(WriteRuleTable(&b, []types.RuleDoc{
		{Name: "a", Decision: "deny", Catches: "x"},
		{Name: "b", Decision: "advise", Workspace: "off", Catches: "y"},
	}))
	s.Contains(b.String(), "DEFAULT")
	s.Contains(b.String(), "1 refuse, 0 explain, 1 off.")
}

func (s *RuleDescribeSuite) TestTheDetailSaysAWorkspaceTurnedTheRuleOff() {
	var b strings.Builder
	WriteRuleDetail(&b, types.RuleDoc{Name: "a", Decision: "deny", Workspace: "off", Catches: "x"})
	s.Contains(b.String(), "a (off; default deny)")
	s.Contains(b.String(), "turns the rule off")
}

func (s *RuleDescribeSuite) TestRuleNamesListsTheCatalog() {
	s.Len(RuleNames(), len(Rules()))
}
