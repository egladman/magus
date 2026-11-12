package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

const (
	rootStopRaw     = `{"hook_event_name":"Stop","session_id":"s1","stop_hook_active":false}`
	subagentStopRaw = `{"hook_event_name":"SubagentStop","session_id":"s1","stop_hook_active":false}`
)

type SessionAdviseSuite struct {
	suite.Suite
	at    location
	store sessions.DirectiveStore
	who   hookAttribution
}

func TestSessionAdviseSuite(t *testing.T) { suite.Run(t, new(SessionAdviseSuite)) }

func (s *SessionAdviseSuite) SetupTest() {
	s.T().Setenv("XDG_STATE_HOME", s.T().TempDir())
	s.at = location{workspace: s.T().TempDir()}
	store, err := sessions.OpenDirectiveStore(s.at.workspace)
	s.Require().NoError(err)
	s.store = store
	s.who = hookAttribution{Host: "claude-code", Session: "s1"}
}

func (s *SessionAdviseSuite) post(expect string) sessions.Directive {
	a, err := s.store.Post(context.Background(), "push what you have; we squash into one PR", expect, "eli", time.Now())
	s.Require().NoError(err)
	return a
}

func pass() types.GuardJudgment {
	return types.GuardJudgment{APIVersion: agent.GuardAPIVersion, Decision: decisionPass}
}

func (s *SessionAdviseSuite) TestARootStopIsHeldOnceToShowTheAdvisory() {
	a := s.post("")
	v := adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass())
	s.Equal(verdictOf(decisionDeny, string(denyRulePersonAdvisory)), judgedAs(v))
	s.Contains(v.Reason, a.ID)
	s.Contains(v.Reason, "not a relay from another agent")
	s.Equal(decisionPass, adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Decision)
}

func (s *SessionAdviseSuite) TestASubagentStopEventShowsNothing() {
	s.post("")
	s.Equal(decisionPass, adviseStop(context.Background(), s.at, s.who, subagentStopRaw, pass()).Decision)
	s.Equal(decisionDeny, adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Decision)
}

func (s *SessionAdviseSuite) TestAStopNamingAnAgentShowsNothing() {
	s.post("")
	child := s.who
	child.Agent = "a1"
	s.Equal(decisionPass, adviseStop(context.Background(), s.at, child, rootStopRaw, pass()).Decision)
	s.Equal(decisionDeny, adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Decision)
}

func (s *SessionAdviseSuite) TestAStopAlreadyHeldShowsItNextTime() {
	s.post("")
	held := types.GuardJudgment{Decision: decisionDeny, Reason: "background tasks still run"}
	s.Equal(held, adviseStop(context.Background(), s.at, s.who, rootStopRaw, held))
	s.Equal(string(denyRulePersonAdvisory), adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Rule)
}

func (s *SessionAdviseSuite) TestAPromptWithoutContextLeavesItForTheStop() {
	s.post("")
	s.Equal(decisionPass, adviseMessage(context.Background(), s.at, s.who, false).Decision)
	s.Equal(string(denyRulePersonAdvisory), adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Rule)
}

func (s *SessionAdviseSuite) TestAPromptWithContextShowsItInsteadOfTheStop() {
	a := s.post("")
	v := adviseMessage(context.Background(), s.at, s.who, true)
	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, a.ID)
	s.Equal(decisionPass, adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Decision)
}

// A directive reaches every session as the person's own words, so an agent
// posting or retracting one would put words in the person's mouth.
func (s *SessionAdviseSuite) deniedToAnAgent(command string) {
	s.Contains(Evaluate(strict(testDependencies()), command).Deny, "post or retract an advisory")
}

func (s *SessionAdviseSuite) TestAnAgentMayNotAdvise() {
	s.deniedToAnAgent(`magus session advise "push what you have"`)
}

func (s *SessionAdviseSuite) TestAnAgentMayNotAdviseBehindGlobalFlags() {
	s.deniedToAnAgent(`./magus -o json session advise --expect "git push" "push"`)
}

func (s *SessionAdviseSuite) TestAnAgentMayNotAdviseOnALineThatDoesNotParse() {
	s.deniedToAnAgent(`magus session advise "x" && (`)
}

func (s *SessionAdviseSuite) TestAnAgentMayNotRetract() {
	s.deniedToAnAgent(`magus session retract adv-3f9a`)
}

func (s *SessionAdviseSuite) TestAnAgentMayListAdvisories() {
	s.Empty(Evaluate(strict(testDependencies()), `magus session advisories`).Deny)
}

func (s *SessionAdviseSuite) TestAnUnreadableStoreIsNamed() {
	store, err := sessions.OpenDirectiveStore(s.at.workspace)
	s.Require().NoError(err)
	dir := store.Dir()
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "advisories.jsonl"), []byte("{\"kind\":\"shout\"}\n"), 0o644))
	v := adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass())
	s.Equal(decisionPass, v.Decision)
	s.Contains(v.Context, "advisories could not be read")
}

func (s *SessionAdviseSuite) TestAnExpectedCommandCountsAfterTheShowing() {
	s.post("git push")
	recordDirectiveAct(context.Background(), s.at, s.who, "git push origin HEAD")
	adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass())
	child := s.who
	child.Agent = "a1"
	recordDirectiveAct(context.Background(), s.at, child, "git push origin HEAD")
	all, err := s.store.Load()
	s.Require().NoError(err)
	s.Len(all[0].Acted, 1)
	s.Contains(all[0].Acted, "s1")
}
