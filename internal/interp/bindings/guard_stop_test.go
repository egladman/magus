package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type StopRuleBindingSuite struct {
	suite.Suite
	reg  *workspace.WorkspaceRegistry
	ctx  context.Context
	sess *buzz.Session
}

func TestStopRuleBindingSuite(t *testing.T) { suite.Run(t, new(StopRuleBindingSuite)) }

func (s *StopRuleBindingSuite) SetupTest() {
	s.reg = workspace.NewWorkspaceRegistry()
	s.ctx = workspace.ContextWithRegistry(s.T().Context(), s.reg)
	s.sess = buzz.NewSession(s.ctx, buzz.WithEmbedded())
	registerAllBuzz(s.ctx, s.sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
}

func (s *StopRuleBindingSuite) TestTheRuleSeesTheRequestAfterTheLoadCloses() {
	s.Require().NoError(s.sess.Exec(s.ctx, `
import "magus";

magus\guard.stop(fun (req: StopRequest) > GuardVerdict {
    return magus\guard.deny("{req.event}|{req.stopHookActive}|{req.transcript}|{req.workspace}|{req.role}");
});
`))
	_ = s.sess.Close()
	rule := s.reg.StopRule()
	s.Require().NotNil(rule)

	got, err := rule(s.T().Context(), types.StopRequest{
		Event: "Stop", StopHookActive: true, Transcript: "/t.jsonl", Workspace: "/repo", Role: types.AgentRoleRoot,
	}, hint.NewGate(s.T().TempDir(), "s"))
	s.Require().NoError(err)
	s.Equal(types.GuardVerdict{Decision: types.GuardDeny, Reason: "Stop|true|/t.jsonl|/repo|root"}, got)
}

func (s *StopRuleBindingSuite) TestRegisteringTwiceIsMisdeclared() {
	err := s.sess.Exec(s.ctx, `
import "magus";

magus\guard.stop(fun (req: StopRequest) > GuardVerdict => magus\guard.allow());
magus\guard.stop(fun (req: StopRequest) > GuardVerdict => magus\guard.allow());
`)
	s.Require().Error(err)
	s.Contains(err.Error(), "MGS1045")
}
