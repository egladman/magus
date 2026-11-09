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

type PersonAdvisorySuite struct {
	suite.Suite
	at    location
	store sessions.AdvisoryStore
	who   hookAttribution
}

func TestPersonAdvisorySuite(t *testing.T) { suite.Run(t, new(PersonAdvisorySuite)) }

func (s *PersonAdvisorySuite) SetupTest() {
	s.T().Setenv("XDG_STATE_HOME", s.T().TempDir())
	s.at = location{workspace: s.T().TempDir()}
	store, err := advisoryStore(s.at)
	s.Require().NoError(err)
	s.store = store
	s.who = hookAttribution{Host: "claude-code", Session: "s1"}
}

func (s *PersonAdvisorySuite) post(expect string) sessions.Advisory {
	a, err := s.store.Post(context.Background(), "push what you have; we squash into one PR", expect, "eli", time.Now())
	s.Require().NoError(err)
	return a
}

func pass() types.GuardJudgment {
	return types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionPass}
}

func (s *PersonAdvisorySuite) TestARootStopIsHeldOnceToShowTheAdvisory() {
	a := s.post("")
	v := adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass())
	s.Equal(decisionDeny, v.Decision)
	s.Equal(RulePersonAdvisory, v.Rule)
	s.Contains(v.Reason, a.ID)
	s.Contains(v.Reason, "not a relay from another agent")
	s.Equal(decisionPass, adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Decision)
}

func (s *PersonAdvisorySuite) TestASubagentStopShowsNothing() {
	s.post("")
	s.Equal(decisionPass, adviseStop(context.Background(), s.at, s.who, subagentStopRaw, pass()).Decision)
	child := s.who
	child.Agent = "a1"
	s.Equal(decisionPass, adviseStop(context.Background(), s.at, child, rootStopRaw, pass()).Decision)
	s.Equal(decisionDeny, adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Decision)
}

func (s *PersonAdvisorySuite) TestAStopAlreadyHeldShowsItNextTime() {
	s.post("")
	held := types.GuardJudgment{Decision: decisionDeny, Reason: "background tasks still run"}
	s.Equal(held, adviseStop(context.Background(), s.at, s.who, rootStopRaw, held))
	s.Equal(RulePersonAdvisory, adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Rule)
}

func (s *PersonAdvisorySuite) TestAPromptShowsItOnlyWhereContextRenders() {
	a := s.post("")
	s.Equal(decisionPass, adviseMessage(context.Background(), s.at, s.who, false).Decision)
	v := adviseMessage(context.Background(), s.at, s.who, true)
	s.Equal(decisionAdvise, v.Decision)
	s.Contains(v.Context, a.ID)
	s.Equal(decisionPass, adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass()).Decision)
}

func (s *PersonAdvisorySuite) TestAnUnreadableStoreIsNamed() {
	dir, err := sessions.AdvisoryDir(s.at.workspace)
	s.Require().NoError(err)
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "advisories.jsonl"), []byte("{\"kind\":\"shout\"}\n"), 0o644))
	v := adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass())
	s.Equal(decisionPass, v.Decision)
	s.Contains(v.Context, "advisories could not be read")
}

func (s *PersonAdvisorySuite) TestAnExpectedCommandCountsAfterTheShowing() {
	s.post("git push")
	recordAdvisoryAct(context.Background(), s.at, s.who, "git push origin HEAD")
	adviseStop(context.Background(), s.at, s.who, rootStopRaw, pass())
	child := s.who
	child.Agent = "a1"
	recordAdvisoryAct(context.Background(), s.at, child, "git push origin HEAD")
	all, err := s.store.Load()
	s.Require().NoError(err)
	s.Len(all[0].Acted, 1)
	s.Contains(all[0].Acted, "s1")
}
