package verdict

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type DecisionSuite struct{ suite.Suite }

func TestDecision(t *testing.T) { suite.Run(t, new(DecisionSuite)) }

// UnmarshalText is the one door from a name to a decision, so it is where a misspelling
// stops, and an empty name stays unset.
func (s *DecisionSuite) TestUnmarshalTextAcceptsEachDecisionAndUnset() {
	for _, name := range []string{"", "allow", "advise", "deny"} {
		var d Decision
		s.Require().NoError(d.UnmarshalText([]byte(name)), name)
		s.Equal(Decision(name), d)
	}
}

func (s *DecisionSuite) TestUnmarshalTextRefusesAnUndeclaredDecision() {
	d := Deny
	s.Require().EqualError(d.UnmarshalText([]byte("block")), `unknown guard decision "block" (want one of [allow advise deny])`)
	s.Equal(Deny, d, "a refused name leaves the decision as it was")
}

func (s *DecisionSuite) TestStringRendersUnsetByName() {
	s.Equal("unset", Decision("").String())
	s.Equal("deny", Deny.String())
}

func (s *DecisionSuite) TestMarshalTextWritesTheName() {
	got, err := Advise.MarshalText()
	s.Require().NoError(err)
	s.Equal("advise", string(got))
}

// A decision outside the declared set never reads as allow: it outranks advise and ties
// with deny, so a verdict nothing can interpret still blocks.
func (s *DecisionSuite) TestStricterTreatsAnUnknownDecisionAsDeny() {
	unknown := Verdict{Decision: "block", Reason: "typo"}
	s.Equal(unknown, Stricter(Verdict{}, unknown))
	s.Equal(unknown, Stricter(unknown, Verdict{Decision: Advise, Reason: "x"}))
}

func (s *DecisionSuite) TestStricterKeepsDenyOverAllowAndAdvise() {
	deny := Verdict{Decision: Deny, Reason: "unnamed model"}
	advise := Verdict{Decision: Advise, Reason: "name a model"}
	s.Equal(deny, Stricter(Verdict{}, deny))
	s.Equal(deny, Stricter(deny, advise))
	s.Equal(deny, Stricter(advise, deny))
}

func (s *DecisionSuite) TestStricterKeepsAdviseOverAllow() {
	advise := Verdict{Decision: Advise, Reason: "name a model"}
	s.Equal(advise, Stricter(advise, Verdict{}))
	s.Equal(Verdict{Decision: Allow}, Stricter(Verdict{Decision: Allow}, Verdict{}), "an explicit allow ties with the zero allow")
}

// Two rules that agree still both explain themselves, and the same reason twice is said
// once.
func (s *DecisionSuite) TestStricterKeepsBothReasonsOnATie() {
	adviseA := Verdict{Decision: Advise, Reason: "name a model"}
	adviseB := Verdict{Decision: Advise, Reason: "add Done when"}
	s.Equal(Verdict{Decision: Advise, Reason: "name a model\n\nadd Done when"}, Stricter(adviseA, adviseB))
	s.Equal(adviseA, Stricter(adviseA, adviseA))
	s.Equal(Verdict{Decision: Deny, Reason: "unnamed model\n\nno completion goal"},
		Stricter(Verdict{Decision: Deny, Reason: "unnamed model"}, Verdict{Decision: Deny, Reason: "no completion goal"}))
}

// A merge reports under one rule: the stricter verdict's, or on a tie the first that
// names one, while both reasons still reach the reader.
func (s *DecisionSuite) TestStricterKeepsOneRule() {
	named := Verdict{Decision: Deny, Reason: "no outcome line", Rule: "pull-request-text"}
	other := Verdict{Decision: Deny, Reason: "no test plan", Rule: "pull-request-tests"}
	advise := Verdict{Decision: Advise, Reason: "name a model", Rule: "spawn-model"}
	unnamed := Verdict{Decision: Deny, Reason: "unnamed model"}

	s.Equal(named, Stricter(advise, named))
	s.Equal(named, Stricter(named, advise))
	s.Equal(Verdict{Decision: Deny, Reason: "no outcome line\n\nno test plan", Rule: "pull-request-text"}, Stricter(named, other))
	s.Equal(Verdict{Decision: Deny, Reason: "unnamed model\n\nno outcome line", Rule: "pull-request-text"}, Stricter(unnamed, named))
}

func (s *DecisionSuite) TestValidateRuleNameAcceptsKebabCase() {
	for _, name := range []string{"pull-request-text", "no-curl", "rule-2", "rule2", "h1-heading"} {
		s.Require().NoError(ValidateRuleName(name), name)
	}
}

// The refusal suggests the spelling magus's Normalize would give the name.
func (s *DecisionSuite) TestValidateRuleNameSuggestsTheKebabSpelling() {
	s.Require().EqualError(ValidateRuleName("pullRequestText"), `guard rule name "pullRequestText" is not lowercase letters and digits joined by single hyphens, as in "pull-request-text"`)
	s.Require().EqualError(ValidateRuleName("pull_request"), `guard rule name "pull_request" is not lowercase letters and digits joined by single hyphens, as in "pull-request"`)
	s.Require().EqualError(ValidateRuleName("HTTPServer2"), `guard rule name "HTTPServer2" is not lowercase letters and digits joined by single hyphens, as in "http-server-2"`)
}

func (s *DecisionSuite) TestValidateRuleNameRefusesEmptyAndSeamNames() {
	s.Require().EqualError(ValidateRuleName(""), `guard rule name is empty`)
	s.Require().EqualError(ValidateRuleName("command"), `guard rule name "command" is reserved: an unnamed command rule reports under it`)
	s.Require().EqualError(ValidateRuleName("step"), `guard rule name "step" is reserved: an unnamed step rule reports under it`)
}
