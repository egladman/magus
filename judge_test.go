package magus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	hostagent "github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const refuseEverything = `import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.deny("Not in this workspace.");
});

magus\guard.write(fun (req: WriteRequest) > GuardVerdict {
    return magus\guard.deny("No writes here.");
});
`

// openRefusing opens a workspace whose magus\guard rules deny every command and every
// write, with a build target on the root project that counts its runs.
func openRefusing(t *testing.T) (*Magus, *atomic.Int32) {
	t.Helper()
	testkit.Isolate(t)
	runs := &atomic.Int32{}
	spellName := register.Spell(t, "zzz-judge-test-spell",
		spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) {
			runs.Add(1)
			return nil, nil
		}),
	)
	w := testkit.NewWorkspace(t)
	w.Magusfile(refuseEverything)
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(t.Context(), w.Root(), WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	return m, runs
}

// A deny is the judgment the caller asked for, reason included, never an error.
func TestJudgeCommandReturnsADenyAsAJudgment(t *testing.T) {
	m, _ := openRefusing(t)

	j, err := m.JudgeCommand(t.Context(), "s1", m.Root(), "ls")
	require.NoError(t, err)
	assert.Equal(t, string(types.GuardDeny), j.Decision)
	assert.Contains(t, j.Reason, "Not in this workspace.")
	assert.NotEmpty(t, j.Rule, "every verdict names its rule")
}

func TestJudgeWriteJudgesThePathAsAWrite(t *testing.T) {
	m, _ := openRefusing(t)

	j, err := m.JudgeWrite(t.Context(), "s1", m.Root(), "notes.txt")
	require.NoError(t, err)
	assert.Equal(t, string(types.GuardDeny), j.Decision)
	assert.Contains(t, j.Reason, "No writes here.")
}

// authorityJudgeSuite pins the SDK's door onto ADR 0045's authority. Rules
// that do not load fall back to the approved ones, read at the authority.
type authorityJudgeSuite struct {
	suite.Suite
	repo *testkit.Repo
}

func TestAuthorityJudge(t *testing.T) { suite.Run(t, new(authorityJudgeSuite)) }

func (s *authorityJudgeSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.repo = testkit.NewRepository(s.T(), map[string]string{"magusfile.buzz": refuseEverything})
}

func (s *authorityJudgeSuite) judge(ctx context.Context) types.GuardJudgment {
	m, err := Open(s.T().Context(), s.repo.Root(), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	j, err := m.JudgeCommand(ctx, "s1", m.Root(), "ls")
	s.Require().NoError(err)
	return j
}

func (s *authorityJudgeSuite) TestRulesThatDoNotLoadAreJudgedByTheApprovedOnes() {
	broken := []byte("import \"magus\";\n\nmagus\\guard.command(\n")
	m, err := Open(s.T().Context(), s.repo.Root(), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	s.Require().NoError(os.WriteFile(filepath.Join(m.Root(), "magusfile.buzz"), broken, 0o644))

	j, err := m.JudgeCommand(s.T().Context(), "s1", m.Root(), "ls")

	s.Require().NoError(err)
	s.Equal(string(types.GuardDeny), j.Decision)
	s.Contains(j.Reason, "Not in this workspace.")
}

func (s *authorityJudgeSuite) TestACommittedLooseningApprovesItselfWithNoAuthority() {
	s.repo.Commit("loosen", map[string]string{"magusfile.buzz": "import \"magus\";\n"})

	s.NotContains(s.judge(s.T().Context()).Reason, "Not in this workspace.")
}

func (s *authorityJudgeSuite) TestACommittedLooseningWaitsOnTheAuthorityTheCallerNames() {
	target := s.repo.Head()
	s.repo.Commit("loosen", map[string]string{"magusfile.buzz": "import \"magus\";\n"})

	j := s.judge(ContextWithAuthority(s.T().Context(), target))

	s.Equal(string(types.GuardDeny), j.Decision)
	s.Contains(j.Reason, "Not in this workspace.")
}

// An in-process run is judged as the `magus run` line the CLI would be, before anything runs.
func TestRunIsJudgedBeforeAnythingRuns(t *testing.T) {
	m, runs := openRefusing(t)

	err := m.Run(t.Context(), []types.Target{{Path: ".", Name: "build"}}, WithSession("s1"))
	var denied *types.GuardDeniedError
	require.ErrorAs(t, err, &denied)
	assert.Contains(t, denied.Judgment.Reason, "Not in this workspace.")
	assert.Zero(t, runs.Load(), "a denied run runs nothing")
}

// The CLI's host already judged the invocation, so the run under its mark is not judged a
// second time.
func TestRunUnderTheCLIsMarkIsNotJudgedAgain(t *testing.T) {
	m, runs := openRefusing(t)

	require.NoError(t, m.Run(guard.WithHostJudged(t.Context()), []types.Target{{Path: ".", Name: "build"}}))
	assert.Equal(t, int32(1), runs.Load())
}

func TestRunLinesAreTheCLIsCommandLines(t *testing.T) {
	lines, err := runLines([]types.Target{{Path: ".", Name: "build"}, {Path: "libs/a", Name: "test"}}, []string{"-run", "Test A"})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"magus run build . -- -run 'Test A'",
		"magus run test libs/a -- -run 'Test A'",
	}, lines)

	affected, err := affectedLine("ci", "origin/main")
	require.NoError(t, err)
	assert.Equal(t, []string{"magus affected ci --base origin/main"}, affected)

	_, err = runLines([]types.Target{{Path: ".", Name: "build"}}, []string{"a\x00b"})
	assert.Error(t, err, "a NUL byte has no command line to judge")
}

func openRefusingExample() (*exampleTB, *Magus) {
	tb, ws := newExample(map[string]string{"magusfile.buzz": refuseEverything})
	m, err := Open(context.Background(), ws.Root(), WithSpawnRule(TierCeiling))
	if err != nil {
		tb.close()
		panic(err)
	}
	tb.Cleanup(func() { _ = m.Close() })
	return tb, m
}

// ExampleMagus_JudgeCommand shows a deny as an answer: the error is nil and the judgment
// carries the decision, the rule that made it and that rule's reason.
func ExampleMagus_JudgeCommand() {
	tb, m := openRefusingExample()
	defer tb.close()

	j, err := m.JudgeCommand(context.Background(), "example", m.Root(), "ls")
	fmt.Println("error:", err)
	fmt.Println("decision:", j.Decision)
	fmt.Println("names its rule:", j.Rule != "")
	fmt.Println("gives the rule's reason:", strings.Contains(j.Reason, "Not in this workspace."))
	// Output:
	// error: <nil>
	// decision: deny
	// names its rule: true
	// gives the rule's reason: true
}

// ExampleMagus_JudgeWrite judges a path as a write and answers in the same shape.
func ExampleMagus_JudgeWrite() {
	tb, m := openRefusingExample()
	defer tb.close()

	j, err := m.JudgeWrite(context.Background(), "example", m.Root(), "notes.txt")
	fmt.Println("error:", err)
	fmt.Println("decision:", j.Decision)
	fmt.Println("names its rule:", j.Rule != "")
	fmt.Println("gives the rule's reason:", strings.Contains(j.Reason, "No writes here."))
	// Output:
	// error: <nil>
	// decision: deny
	// names its rule: true
	// gives the rule's reason: true
}

// jobReaderSuite pins WithJobReader: the lease rules judge an embedder's own
// rows, named by the lease on the call's context.
type jobReaderSuite struct {
	suite.Suite
	root string
}

func TestJobReader(t *testing.T) {
	suite.Run(t, new(jobReaderSuite))
}

// taskReader answers an embedder's tasks as job rows.
type taskReader []types.Job

func (r taskReader) List() ([]types.Job, error) { return r, nil }

func (s *jobReaderSuite) SetupTest() {
	testkit.Isolate(s.T())
	w := testkit.NewWorkspace(s.T())
	w.Magusfile(`import "magus";` + "\n")
	s.root = w.Root()
}

func (s *jobReaderSuite) judge(authority string) types.GuardJudgment {
	task := types.Job{ID: "task", State: types.StateRunning, WritePaths: []string{"a.go"}, Authority: authority}
	m, err := Open(s.T().Context(), s.root, WithJobReader(taskReader{task}), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	j, err := m.JudgeCommand(types.WithJobLease(s.T().Context(), "task"), "s1", m.Root(), "./build.sh")
	s.Require().NoError(err)
	return j
}

func (s *jobReaderSuite) TestATaskWithNoAuthorityIsRefusedWork() {
	j := s.judge("")
	s.Contains(j.Reason, "lease task records no authority")
	s.Equal(types.GuardJudgment{
		APIVersion: j.APIVersion, Decision: string(types.GuardDeny), Reason: j.Reason, Context: j.Context,
		Rule: "lease-authority", Lease: "task", LeaseFrom: j.LeaseFrom, NextSteps: j.NextSteps,
	}, j)
}

func (s *jobReaderSuite) TestATaskWithAnAuthorityIsNotRefusedForIt() {
	s.NotEqual("lease-authority", s.judge("0123abcd").Rule)
}

// A task is judged by the policy of the checkout its row names.
func (s *jobReaderSuite) TestATaskIsJudgedByItsRowsCheckout() {
	other := testkit.NewRepository(s.T(), map[string]string{"magusfile.buzz": refuseEverything})
	task := types.Job{ID: "task", State: types.StateRunning, Authority: other.Head(), CheckoutRoot: other.Root()}
	m, err := Open(s.T().Context(), s.root, WithJobReader(taskReader{task}), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })

	j, err := m.JudgeCommand(types.WithJobLease(s.T().Context(), "task"), "s1", m.Root(), "ls")

	s.Require().NoError(err)
	s.Contains(j.Reason, "Not in this workspace.")
}

func (s *jobReaderSuite) TestANilReaderFailsTheOpen() {
	_, err := Open(s.T().Context(), s.root, WithJobReader(nil), WithSpawnRule(TierCeiling))
	s.ErrorContains(err, "WithJobReader was given a nil reader")
}

// judgeStepSuite pins Judge, ADR 0046's door for any step an embedder's
// agent takes.
type judgeStepSuite struct {
	suite.Suite
	root string
}

func TestJudgeStep(t *testing.T) { suite.Run(t, new(judgeStepSuite)) }

func (s *judgeStepSuite) SetupTest() {
	testkit.Isolate(s.T())
	w := testkit.NewWorkspace(s.T())
	w.Magusfile(refuseEverything)
	s.root = w.Root()
}

func (s *judgeStepSuite) open(opts ...Option) *Magus {
	m, err := Open(s.T().Context(), s.root, append(opts, WithSpawnRule(TierCeiling))...)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	return m
}

func (s *judgeStepSuite) step(kind, name string, input map[string]any) agent.Step {
	return agent.Step{APIVersion: agent.StepAPIVersion, ID: "s-1", Kind: kind, Name: name, Input: input, Session: "s1", Dir: s.root}
}

// stepDenied is the whole judgment a step-level deny under rule returns.
func stepDenied(rule, reason string) types.GuardJudgment {
	return types.GuardJudgment{APIVersion: hostagent.GuardAPIVersion, Decision: string(types.GuardDeny), Reason: reason, Rule: rule}
}

var shellRow = agent.StepSpec{Kind: "shell", Capabilities: []string{string(types.ToolRuns)}, Command: "/command"}

var refundRow = agent.StepSpec{Kind: "refund", Capabilities: []string{string(types.ToolNetwork)}}

// refundLimit denies a refund over 100, a rule for a kind magus never ships.
func refundLimit(_ context.Context, req agent.StepRequest) (types.GuardVerdict, error) {
	if amount, _ := req.Step.Input["amount"].(float64); req.Step.Kind == "refund" && amount > 100 {
		return types.GuardVerdict{Decision: types.GuardDeny, Reason: "Refunds over 100 need a person.", Rule: "refund-limit"}, nil
	}
	return types.GuardVerdict{}, nil
}

func (s *judgeStepSuite) TestAnUndeclaredKindIsDenied() {
	j, err := s.open().Judge(s.T().Context(), s.step("refund", "card", nil))

	s.Require().NoError(err)
	s.Equal(stepDenied("unjudged-step", j.Reason), j)
	s.Contains(j.Reason, `kind "refund"`)
	s.Contains(j.Reason, "magus.WithStepRows")
}

func (s *judgeStepSuite) TestARunsRowJudgesItsCommand() {
	j, err := s.open(WithStepSpecs(shellRow)).Judge(s.T().Context(), s.step("shell", "bash", map[string]any{"command": "ls"}))

	s.Require().NoError(err)
	s.Equal(string(types.GuardDeny), j.Decision)
	s.Contains(j.Reason, "Not in this workspace.")
}

func (s *judgeStepSuite) TestAWritesRowJudgesEachPath() {
	row := agent.StepSpec{Kind: "memory", Capabilities: []string{string(types.ToolWrites)}, Paths: []string{"/files"}}

	j, err := s.open(WithStepSpecs(row)).Judge(s.T().Context(), s.step("memory", "notes", map[string]any{"files": []any{"notes.txt"}}))

	s.Require().NoError(err)
	s.Equal(string(types.GuardDeny), j.Decision)
	s.Contains(j.Reason, "No writes here.")
}

func (s *judgeStepSuite) TestAMistypedLocationIsDeniedNamingTheField() {
	j, err := s.open(WithStepSpecs(shellRow)).Judge(s.T().Context(), s.step("shell", "bash", map[string]any{"command": 4.0}))

	s.Require().NoError(err)
	s.Equal(stepDenied("step-input", j.Reason), j)
	s.Contains(j.Reason, "/command")
}

func (s *judgeStepSuite) TestAnInputOffItsSchemaIsDeniedBeforeAnyRuleRuns() {
	row := refundRow
	row.InputSchema = []byte(`{"type":"object","properties":{"amount":{"type":"number"}},"required":["amount"]}`)
	calls := 0
	counting := func(context.Context, agent.StepRequest) (types.GuardVerdict, error) {
		calls++
		return types.GuardVerdict{}, nil
	}

	j, err := s.open(WithStepSpecs(row), WithStepRule(counting)).Judge(s.T().Context(), s.step("refund", "card", map[string]any{"amount": "ten"}))

	s.Require().NoError(err)
	s.Equal(stepDenied("step-input", j.Reason), j)
	s.Contains(j.Reason, "/properties/amount")
	s.Zero(calls, "no rule reads a malformed input")
}

func (s *judgeStepSuite) TestAGoRuleJudgesAKindMagusNeverShips() {
	m := s.open(WithStepSpecs(refundRow), WithStepRule(refundLimit))

	j, err := m.Judge(s.T().Context(), s.step("refund", "card", map[string]any{"amount": 250.0}))

	s.Require().NoError(err)
	s.Equal(stepDenied("step:refund-limit", "Refunds over 100 need a person."), j)
}

func (s *judgeStepSuite) TestAGoRulesPassLeavesTheStepPassing() {
	m := s.open(WithStepSpecs(refundRow), WithStepRule(refundLimit))

	j, err := m.Judge(s.T().Context(), s.step("refund", "card", map[string]any{"amount": 20.0}))

	s.Require().NoError(err)
	s.Equal("pass", j.Decision)
}

func (s *judgeStepSuite) TestAGoRuleErrorIsAJudgmentNotMade() {
	failing := func(context.Context, agent.StepRequest) (types.GuardVerdict, error) {
		return types.GuardVerdict{}, errors.New("ledger unreachable")
	}

	_, err := s.open(WithStepSpecs(refundRow), WithStepRule(failing)).Judge(s.T().Context(), s.step("refund", "card", nil))

	s.ErrorContains(err, "ledger unreachable")
}

func (s *judgeStepSuite) TestAnInertRowPasses() {
	row := agent.StepSpec{Kind: "plan", Capabilities: []string{string(types.ToolInert)}}

	j, err := s.open(WithStepSpecs(row)).Judge(s.T().Context(), s.step("plan", "todo", map[string]any{"items": []any{"ls"}}))

	s.Require().NoError(err)
	s.Equal("pass", j.Decision)
}

func (s *judgeStepSuite) TestAWrongAPIVersionIsAnError() {
	step := s.step("plan", "todo", nil)
	step.APIVersion = "magus.step/v9"

	_, err := s.open().Judge(s.T().Context(), step)

	s.ErrorContains(err, "api_version")
}

func (s *judgeStepSuite) TestAnEmptyDirIsAnErrorWhenTheRowReadsACommand() {
	step := s.step("shell", "bash", map[string]any{"command": "ls"})
	step.Dir = ""

	_, err := s.open(WithStepSpecs(shellRow)).Judge(s.T().Context(), step)

	s.ErrorContains(err, "dir is empty")
}

func (s *judgeStepSuite) TestARowNothingJudgesFailsTheOpen() {
	_, err := Open(s.T().Context(), s.root, WithStepSpecs(refundRow), WithSpawnRule(TierCeiling))

	s.ErrorContains(err, "magus.WithStepRule")
}

func (s *judgeStepSuite) TestARepeatedRowFailsTheOpen() {
	_, err := Open(s.T().Context(), s.root, WithStepSpecs(shellRow, shellRow), WithSpawnRule(TierCeiling))

	s.ErrorContains(err, `repeats kind "shell"`)
}

func (s *judgeStepSuite) TestAPointerThatDoesNotParseFailsTheOpen() {
	row := shellRow
	row.Command = "command"

	_, err := Open(s.T().Context(), s.root, WithStepSpecs(row), WithSpawnRule(TierCeiling))

	s.ErrorContains(err, "not a JSON Pointer")
}

func (s *judgeStepSuite) TestASchemaThatDoesNotParseFailsTheOpen() {
	row := shellRow
	row.InputSchema = []byte(`{"type":`)

	_, err := Open(s.T().Context(), s.root, WithStepSpecs(row), WithSpawnRule(TierCeiling))

	s.ErrorContains(err, "input_schema")
}

func (s *judgeStepSuite) TestAKindNamedForASeamFailsTheOpen() {
	row := shellRow
	row.Kind = "command"

	_, err := Open(s.T().Context(), s.root, WithStepSpecs(row), WithSpawnRule(TierCeiling))

	s.ErrorContains(err, "reserved")
}

var delegateRow = agent.StepSpec{Kind: "delegate", Capabilities: []string{string(types.ToolSpawns)}, SpawnDescriptionField: "/title"}

// A spawns row's step reaches the workspace's magus\guard.spawn rule, as a
// host's spawn does.
func (s *judgeStepSuite) TestASpawnsRowIsJudgedByTheSpawnRules() {
	w := testkit.NewWorkspace(s.T())
	w.Magusfile(`import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > GuardVerdict {
    return magus\guard.deny("No spawns here: {req.description}.");
});
`)
	m, err := Open(s.T().Context(), w.Root(), WithStepSpecs(delegateRow), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	step := agent.Step{APIVersion: agent.StepAPIVersion, Kind: "delegate", Name: "researcher", Session: "s1", Dir: w.Root(),
		Input: map[string]any{"prompt": "Summarize the open tickets.", "title": "ticket summary"}}

	j, err := m.Judge(s.T().Context(), step)

	s.Require().NoError(err)
	s.Equal(string(types.GuardDeny), j.Decision)
	s.Contains(j.Reason, "No spawns here: ticket summary.")
}

func (s *judgeStepSuite) TestASpawnsStepWithNoPromptIsDeniedNamingIt() {
	j, err := s.open(WithStepSpecs(delegateRow)).Judge(s.T().Context(), s.step("delegate", "researcher", map[string]any{"title": "t"}))

	s.Require().NoError(err)
	s.Equal(stepDenied("step-input", j.Reason), j)
	s.Contains(j.Reason, "spawn prompt")
}

// buzzStepRuleSuite pins magus\guard.step: the workspace's rule for a kind
// feeds the judgment Judge returns.
type buzzStepRuleSuite struct {
	suite.Suite
}

func TestBuzzStepRule(t *testing.T) { suite.Run(t, new(buzzStepRuleSuite)) }

func (s *buzzStepRuleSuite) SetupTest() { testkit.Isolate(s.T()) }

// open opens a workspace whose root magusfile is src, with refundRow declared.
func (s *buzzStepRuleSuite) open(src string, opts ...Option) (*Magus, error) {
	w := testkit.NewWorkspace(s.T())
	w.Magusfile(src)
	m, err := Open(s.T().Context(), w.Root(), append(opts, WithStepSpecs(refundRow), WithSpawnRule(TierCeiling))...)
	if m != nil {
		s.T().Cleanup(func() { _ = m.Close() })
	}
	return m, err
}

func (s *buzzStepRuleSuite) refund(m *Magus, amount float64) agent.Step {
	return agent.Step{APIVersion: agent.StepAPIVersion, Kind: "refund", Name: "card", Session: "s1", Dir: m.Root(),
		Input: map[string]any{"amount": amount}}
}

const refundRule = `import "magus";
import "magus/agent";

magus\guard.step("refund", fun (req: magus\agent\StepRequest) > GuardVerdict {
    if (req.step.name == "card") {
        return magus\guard.deny("Card refunds need a person.");
    }
    return magus\guard.allow();
});
`

func (s *buzzStepRuleSuite) TestABuzzRuleJudgesAKindMagusNeverShips() {
	m, err := s.open(refundRule)
	s.Require().NoError(err)

	j, err := m.Judge(s.T().Context(), s.refund(m, 250))

	s.Require().NoError(err)
	s.Equal(stepDenied("workspace:step", "Card refunds need a person."), j)
}

func (s *buzzStepRuleSuite) TestANamedVerdictReportsUnderItsName() {
	m, err := s.open(`import "magus";
import "magus/agent";

magus\guard.step("refund", fun (req: magus\agent\StepRequest) > GuardVerdict => magus\guard.deny("No refunds.", {"rule": "refund-limit"}));
`)
	s.Require().NoError(err)

	j, err := m.Judge(s.T().Context(), s.refund(m, 250))

	s.Require().NoError(err)
	s.Equal(stepDenied("workspace:refund-limit", "No refunds."), j)
}

func (s *buzzStepRuleSuite) TestTheStricterOfTheGoAndTheBuzzRuleWins() {
	m, err := s.open(`import "magus";
import "magus/agent";

magus\guard.step("refund", fun (req: magus\agent\StepRequest) > GuardVerdict => magus\guard.advise("Log the refund."));
`, WithStepRule(refundLimit))
	s.Require().NoError(err)

	j, err := m.Judge(s.T().Context(), s.refund(m, 250))

	s.Require().NoError(err)
	s.Equal(stepDenied("step:refund-limit", "Refunds over 100 need a person."), j)
}

func (s *buzzStepRuleSuite) TestAnUncommittedLooseningWaitsOnApproval() {
	repo := testkit.NewRepository(s.T(), map[string]string{"magusfile.buzz": refundRule})
	m, err := Open(s.T().Context(), repo.Root(), WithStepSpecs(refundRow), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	loosened := "import \"magus\";\nimport \"magus/agent\";\n\nmagus\\guard.step(\"refund\", fun (req: magus\\agent\\StepRequest) > GuardVerdict => magus\\guard.allow());\n"
	s.Require().NoError(os.WriteFile(filepath.Join(m.Root(), "magusfile.buzz"), []byte(loosened), 0o644))

	j, err := m.Judge(s.T().Context(), s.refund(m, 250))

	s.Require().NoError(err)
	s.Equal(stepDenied("workspace:step", "Card refunds need a person."), j)
}

func (s *buzzStepRuleSuite) TestARuleForAnotherKindLeavesTheRowUnjudgedAtOpen() {
	_, err := s.open(`import "magus";
import "magus/agent";

magus\guard.step("egress", fun (req: magus\agent\StepRequest) > GuardVerdict => magus\guard.allow());
`)

	s.ErrorContains(err, `magus\guard.step`)
	s.ErrorContains(err, `kind "refund"`)
}

func (s *buzzStepRuleSuite) TestARuleThatRaisesDeniesARowOnlyItJudges() {
	m, err := s.open(`import "magus";
import "magus/agent";

magus\guard.step("refund", fun (req: magus\agent\StepRequest) > GuardVerdict {
    if (req.step.name == "card") { throw "ledger unreachable"; }
    return magus\guard.allow();
});
`)
	s.Require().NoError(err)

	j, err := m.Judge(s.T().Context(), s.refund(m, 250))

	s.Require().NoError(err)
	s.Equal(stepDenied("unjudged-step", j.Reason), j)
	s.Contains(j.Reason, "ledger unreachable")
}

// stepRemedySuite pins that a step's built-in deny serves the remedy the
// same command line gets from the hook.
type stepRemedySuite struct {
	suite.Suite
}

func TestStepRemedy(t *testing.T) { suite.Run(t, new(stepRemedySuite)) }

func (s *stepRemedySuite) TestADenyServesItsRemedyCommand() {
	testkit.Isolate(s.T())
	w := testkit.NewWorkspace(s.T())
	w.Magusfile(`import "magus";` + "\n")
	m, err := Open(s.T().Context(), w.Root(), WithStepSpecs(shellRow), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	step := agent.Step{APIVersion: agent.StepAPIVersion, Kind: "shell", Name: "bash", Session: "s1", Dir: w.Root(),
		Input: map[string]any{"command": "magus ls | head"}}

	j, err := m.Judge(s.T().Context(), step)

	s.Require().NoError(err)
	s.Equal(string(types.GuardDeny), j.Decision)
	s.NotEmpty(j.NextSteps, "the deny serves a remedy command")
}
