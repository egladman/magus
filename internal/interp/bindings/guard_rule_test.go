package bindings

import (
	"context"
	"fmt"
	"testing"

	"github.com/egladman/magus/internal/guard/steps"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// A rule's name rides its verdict to the guard; an unnamed verdict carries none.
func TestGuardVerdictCarriesTheRuleName(t *testing.T) {
	rule, err := loadCommandRule(t, `
import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    if (req.command == "named") {
        return GuardVerdict{decision = magus\GuardDecision.deny, reason = "Lead with the outcome.", rule = "pull-request-text"};
    }
    return magus\guard.deny("unnamed");
});
`)
	require.NoError(t, err)
	facts := hint.NewGate(t.TempDir(), "claude-code/s1")
	cases := map[string]verdict.Verdict{
		"named": {Decision: verdict.Deny, Reason: "Lead with the outcome.", Rule: "pull-request-text"},
		"other": {Decision: verdict.Deny, Reason: "unnamed"},
	}
	for command, want := range cases {
		got, err := rule(t.Context(), types.CommandRequest{Command: command}, facts)
		require.NoError(t, err, command)
		assert.Equal(t, want, got, command)
	}
}

// A rule may ask: the call waits on the person, by the member or the enum case.
func TestCommandRuleMayAsk(t *testing.T) {
	rule, err := loadCommandRule(t, `
import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    if (req.command == "git push") {
        return magus\guard.ask("A person approves pushes.");
    }
    if (req.command == "gh pr merge") {
        return GuardVerdict{decision = magus\GuardDecision.ask, reason = "A person merges."};
    }
    return magus\guard.defer();
});
`)
	require.NoError(t, err)
	facts := hint.NewGate(t.TempDir(), "claude-code/s1")
	cases := map[string]verdict.Verdict{
		"git push":    {Decision: verdict.Ask, Reason: "A person approves pushes."},
		"gh pr merge": {Decision: verdict.Ask, Reason: "A person merges."},
		"ls":          {Decision: verdict.Defer},
	}
	for command, want := range cases {
		got, err := rule(t.Context(), types.CommandRequest{Command: command}, facts)
		require.NoError(t, err, command)
		assert.Equal(t, want, got, command)
	}
}

// A rule's deny reaches the guard as a deny: the decision crosses as an enum case.
func TestCommandRuleDenyDenies(t *testing.T) {
	rule, err := loadCommandRule(t, `
import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    if (req.command == "git push") {
        return magus\guard.deny("Ask before pushing.");
    }
    return magus\guard.defer();
});
`)
	require.NoError(t, err)
	facts := hint.NewGate(t.TempDir(), "claude-code/s1")

	got, err := rule(t.Context(), types.CommandRequest{Command: "git push"}, facts)
	require.NoError(t, err)
	assert.Equal(t, verdict.Verdict{Decision: verdict.Deny, Reason: "Ask before pushing."}, got)

	got, err = rule(t.Context(), types.CommandRequest{Command: "ls"}, facts)
	require.NoError(t, err)
	assert.Equal(t, verdict.Verdict{Decision: verdict.Defer}, got)
}

// A rule that denies on the caller's role denies a worker and passes the root.
func TestCommandRuleDeniesOnRole(t *testing.T) {
	rule, err := loadCommandRule(t, `
import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    if (req.role == magus\AgentRole.worker) {
        return magus\guard.deny("A worker does not build.");
    }
    return magus\guard.defer();
});
`)
	require.NoError(t, err)
	facts := hint.NewGate(t.TempDir(), "claude-code/s1")

	got, err := rule(t.Context(), types.CommandRequest{Command: "go build", Role: types.AgentRoleWorker}, facts)
	require.NoError(t, err)
	assert.Equal(t, verdict.Verdict{Decision: verdict.Deny, Reason: "A worker does not build."}, got)

	got, err = rule(t.Context(), types.CommandRequest{Command: "go build", Role: types.AgentRoleRoot}, facts)
	require.NoError(t, err)
	assert.Equal(t, verdict.Verdict{Decision: verdict.Defer}, got)
}

// Comparing a boundary record's enum field to a string literal is always false, so a
// rule that does it would never fire: the checker refuses it at load.
func TestCommandRuleComparingAnEnumFieldToAStringFailsToLoad(t *testing.T) {
	_, err := loadCommandRule(t, `
import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    if (req.role == "worker") {
        return magus\guard.deny("A worker does not build.");
    }
    return magus\guard.defer();
});
`)
	require.ErrorContains(t, err, "BZZ1005")
	require.ErrorContains(t, err, "== between an enum and `str` is always false")
}

// deny's and advise's opts name the rule.
func TestVerdictRuleOpt(t *testing.T) {
	opts := vm.NewMap()
	opts.MapSet("rule", vm.StringValue("pull-request-text"))
	got, err := verdictRuleOpt("deny", opts)
	require.NoError(t, err)
	assert.Equal(t, "pull-request-text", got)

	_, err = verdictRuleOpt("advise", vm.StringValue("pull-request-text"))
	require.ErrorContains(t, err, `magus\guard.advise: opts must be a {rule: str} record`)
}

// A name that cannot report as workspace:<name> is a misdeclaration, whether it reaches the
// guard through deny's opts or a GuardVerdict the rule built itself.
func TestGuardVerdictRuleNameIsValidated(t *testing.T) {
	guardMap := vm.NewMap()
	registerVerdictMembers(context.Background(), nil, nil, guardMap)
	deny := requireDirect(t, guardMap, "deny")
	opts := func(key string, v vm.Value) vm.Value {
		m := vm.NewMap()
		m.MapSet(key, v)
		return m
	}

	err := callVoidDirect(t, deny, vm.StringValue("why"), opts("rule", vm.StringValue("Pull_Request")))
	require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
	require.ErrorContains(t, err, `guard rule name "Pull_Request" is not lowercase letters and digits joined by single hyphens, as in "pull-request"`)

	err = callVoidDirect(t, deny, vm.StringValue("why"), opts("rule", vm.StringValue("command")))
	require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))

	err = callVoidDirect(t, deny, vm.StringValue("why"), opts("rule", vm.IntValue(1)))
	require.ErrorContains(t, err, `magus\guard.deny: "rule" must be a string`)

	err = callVoidDirect(t, deny, vm.StringValue("why"), opts("name", vm.StringValue("x")))
	require.ErrorContains(t, err, `magus\guard.deny: unknown opts field "name"`)

	literal := vm.NewMap()
	literal.MapSet("decision", vm.StringValue("deny"))
	literal.MapSet("reason", vm.StringValue("why"))
	literal.MapSet("rule", vm.StringValue("NotKebab"))
	_, err = decodeGuardVerdict(`magus\guard.command`, literal)
	require.ErrorContains(t, err, string(types.GuardRuleMisdeclared))
}

// TestRunDirRetainsNothingWhenTheMagusfileRegistersARule pins that a rule is stored by
// the parse-mode load alone: the registry already holds the workspace's rule, so each
// run-mode evaluation of the same magusfile must free what it allocates.
func TestRunDirRetainsNothingWhenTheMagusfileRegistersARule(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `import "magus";
magus\guard.command(fun (req: magus\CommandRequest) > magus\GuardVerdict { return magus\guard.defer(); });
export fun build(ctx: magus\Context, args: [str]) > void {}
`)
	reg := workspace.NewWorkspaceRegistry()
	defer func() { _ = reg.Close() }()
	ctx := workspace.ContextWithRegistry(context.Background(), reg)
	srcs, err := interp.FindAll(dir)
	require.NoError(t, err)
	_, err = interp.Parse(ctx, srcs[0])
	require.NoError(t, err)
	require.NotNil(t, reg.CommandRule(), "the parse-mode load must store the rule")
	evaluate := func(n int) {
		for range n {
			_, err := interp.RunDir(ctx, dir, "build", nil)
			require.NoError(t, err)
		}
	}
	evaluate(10)
	objects := vm.ReadHeapStats().Objects
	evaluate(100)
	if grew := vm.ReadHeapStats().Objects - objects; grew > 100 {
		t.Fatalf("100 evaluations grew the Buzz heap by %d objects; a run-mode load must keep nothing", grew)
	}
}

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

const commandRuleTemplate = `
import "magus";
magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.deny("%s");
});
`

func commandRuleSource(reason string) string {
	return fmt.Sprintf(commandRuleTemplate, reason)
}

func (s *FunctionRuleLoadSuite) commandVerdict() verdict.Verdict {
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
    return magus\guard.defer();
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
