package bindings

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/guard/steps"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// FunctionRuleLoadSuite pins which load stores a function rule and keeps its session: the
// parse-mode load alone. A run-mode load only checks the call.
type FunctionRuleLoadSuite struct {
	suite.Suite
	reg   *workspace.WorkspaceRegistry
	group *steps.Group
}

func TestFunctionRuleLoadSuite(t *testing.T) {
	suite.Run(t, new(FunctionRuleLoadSuite))
}

func (s *FunctionRuleLoadSuite) SetupTest() {
	s.reg, s.group = workspace.NewWorkspaceRegistry(), &steps.Group{}
	s.T().Cleanup(func() { _ = s.reg.Close() })
}

func (s *FunctionRuleLoadSuite) context() context.Context {
	return steps.WithGroup(workspace.ContextWithRegistry(s.T().Context(), s.reg), s.group)
}

// load runs src in a session of the given mode over the suite's registry and closes it, as
// a load does before the guard calls what it registered.
func (s *FunctionRuleLoadSuite) load(parseMode bool, src string) error {
	ctx := s.context()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	s.Require().NoError(registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, parseMode))
	return sess.Exec(ctx, src)
}

// leakSlack covers the objects a first load builds once for the process, such as interned
// enum cases; ten kept sessions would exceed it many times over.
const leakSlack = 50

func commandRuleSource(reason string) string {
	return `
import "magus";
magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.deny("` + reason + `");
});
`
}

func (s *FunctionRuleLoadSuite) commandVerdict() types.GuardVerdict {
	rule := s.reg.CommandRule()
	s.Require().NotNil(rule, "the registry holds no command rule")
	got, err := rule(s.T().Context(), types.CommandRequest{Command: "ls"}, hint.NewGate(s.T().TempDir(), "claude-code/s1"))
	s.Require().NoError(err)
	return got
}

func (s *FunctionRuleLoadSuite) TestParseModeLoadRegistersAndKeepsItsSession() {
	before := vm.ReadHeapStats().Objects
	s.Require().NoError(s.load(true, commandRuleSource("first")))
	s.Equal("first", s.commandVerdict().Reason)
	s.Greater(vm.ReadHeapStats().Objects, before, "the closed load must still hold what the rule runs on")
	s.Require().NoError(s.reg.Close())
	s.LessOrEqual(vm.ReadHeapStats().Objects, before+leakSlack, "closing the registry must release the kept session")
}

func (s *FunctionRuleLoadSuite) TestRunModeLoadStoresNoRule() {
	s.Require().NoError(s.load(true, commandRuleSource("first")))
	s.Require().NoError(s.load(false, commandRuleSource("second")))
	s.Equal("first", s.commandVerdict().Reason)
}

func (s *FunctionRuleLoadSuite) TestRunModeLoadKeepsNothing() {
	s.Require().NoError(s.load(true, commandRuleSource("first")))
	before := vm.ReadHeapStats().Objects
	for range 10 {
		s.Require().NoError(s.load(false, commandRuleSource("again")))
	}
	s.LessOrEqual(vm.ReadHeapStats().Objects, before+leakSlack, "a closed run-mode load left objects behind")
}

func (s *FunctionRuleLoadSuite) TestRunModeLoadRefusesASecondRegistration() {
	s.Require().NoError(s.load(true, commandRuleSource("first")))
	err := s.load(false, commandRuleSource("first")+commandRuleSource("second"))
	s.Require().ErrorContains(err, string(types.GuardRuleMisdeclared))
	s.ErrorContains(err, "already registered in this magusfile")
}

func (s *FunctionRuleLoadSuite) TestRunModeLoadRefusesARuleTheWorkspaceLoadDidNotRegister() {
	err := s.load(false, commandRuleSource("late"))
	s.Require().ErrorContains(err, string(types.GuardRuleMisdeclared))
	s.ErrorContains(err, "registered while a target runs and not when the workspace loaded")
	s.Nil(s.reg.CommandRule())
}

const stepRuleSource = `
import "magus";
import "magus/agent";
magus\guard.step("ci", fun (req: StepRequest) > GuardVerdict {
    return magus\guard.allow();
});
`

func (s *FunctionRuleLoadSuite) TestStepRuleFollowsTheSameRule() {
	err := s.load(false, stepRuleSource)
	s.Require().ErrorContains(err, string(types.GuardRuleMisdeclared))
	s.ErrorContains(err, "registered while a target runs and not when the workspace loaded")
	s.Nil(s.group.Rule("ci"))

	s.Require().NoError(s.load(true, stepRuleSource))
	stored := s.group.Rule("ci")
	s.Require().NotNil(stored)
	before := vm.ReadHeapStats().Objects
	s.Require().NoError(s.load(false, stepRuleSource))
	s.LessOrEqual(vm.ReadHeapStats().Objects, before+leakSlack, "a closed run-mode load left objects behind")
	s.Require().NotNil(s.group.Rule("ci"))
}

const modelTierMatrixSource = `
import "magus";
magus\modelTier(fun (req: ModelTierRequest) > any { return null; });
`

func (s *FunctionRuleLoadSuite) TestModelTierMatrixFollowsTheSameRule() {
	err := s.load(false, modelTierMatrixSource)
	s.Require().ErrorContains(err, string(types.ModelTierDeclarationUnusable))
	s.ErrorContains(err, "registered while a target runs and not when the workspace loaded")
	s.Nil(s.reg.ModelTierMatrix())

	s.Require().NoError(s.load(true, modelTierMatrixSource))
	before := vm.ReadHeapStats().Objects
	s.Require().NoError(s.load(false, modelTierMatrixSource))
	s.LessOrEqual(vm.ReadHeapStats().Objects, before+leakSlack, "a closed run-mode load left objects behind")
	s.NotNil(s.reg.ModelTierMatrix())
}

func (s *FunctionRuleLoadSuite) TestScriptModeRefusesStopAndStep() {
	ctx := s.context()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	RegisterMagusNamespace(ctx, sess)
	for _, src := range []string{
		`import "magus"; magus\guard.stop(fun (req: int) > int { return req; });`,
		`import "magus"; magus\guard.step("ci", fun (req: int) > int { return req; });`,
	} {
		err := sess.Exec(ctx, src)
		s.Require().ErrorContains(err, string(types.MagusfileOnlyMember), src)
	}
	s.Nil(s.reg.StopRule())
	s.Nil(s.group.Rule("ci"))
}

func (s *FunctionRuleLoadSuite) TestClosedRootPoolWorkerReleasesItsSession() {
	dir := s.T().TempDir()
	writeMagusfile(s.T(), dir, commandRuleSource("first")+"export fun build(ctx: magus\\Context, args: [str]) > void {}\n")
	ctx := s.context()
	srcs, err := interp.FindAll(dir)
	s.Require().NoError(err)
	_, err = interp.Parse(ctx, srcs[0])
	s.Require().NoError(err)
	before := vm.ReadHeapStats().Objects
	for range 10 {
		worker, err := interp.NewBuzzWorkerFunc(srcs[0])(ctx)
		s.Require().NoError(err)
		s.Require().NoError(worker.Session.Close())
	}
	s.LessOrEqual(vm.ReadHeapStats().Objects, before+leakSlack, "a closed pool worker left objects behind")
}
