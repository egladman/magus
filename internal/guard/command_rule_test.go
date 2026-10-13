package guard

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commandRuleProbe is a workspace command rule that records what it was asked and answers
// as told.
type commandRuleProbe struct {
	asked  []types.CommandRequest
	gates  []hint.Gate
	answer types.GuardVerdict
	err    error
}

func (p *commandRuleProbe) rule() workspace.CommandRule {
	return func(_ context.Context, req types.CommandRequest, facts hint.Gate) (types.GuardVerdict, error) {
		p.asked = append(p.asked, req)
		p.gates = append(p.gates, facts)
		return p.answer, p.err
	}
}

// claudeBashEnvelope is a Claude Code shell call as its hook wiring hands it to `magus shell`.
const claudeBashEnvelope = `{"session_id":"8f2c6a1e","hook_event_name":"PreToolUse","tool_name":"Bash",` +
	`"tool_input":{"command":"GH_TOKEN=x timeout 60 gh pr checks 183 --watch","description":"watch ci"}}`

// The rule is handed the parsed programs the built-ins see, the caller's label for the
// command, and who is calling, so a policy never tokenizes a shell line itself.
func TestCommandRuleSeesTheNormalizedRequest(t *testing.T) {
	ctx, _ := spawnFixture(t)
	at := hookLocation(ctx, Dependencies{})
	require.NotEmpty(t, at.dir)
	probe := &commandRuleProbe{}
	v := Judge(ctx, Dependencies{CommandRule: probe.rule()}, Request{Input: claudeBashEnvelope, Host: "claude-code"})
	assert.Equal(t, "pass", v.Decision)
	require.Len(t, probe.asked, 1)
	assert.Equal(t, types.CommandRequest{
		Host: "claude-code", Session: "8f2c6a1e",
		Command:     "GH_TOKEN=x timeout 60 gh pr checks 183 --watch",
		Description: "watch ci",
		Commands:    []types.CommandInvocation{{Program: "gh", Args: []string{"pr", "checks", "183", "--watch"}}},
		Role:        types.AgentRoleRoot,
		Thread:      types.AgentThreadMain,
		Dir:         at.dir,
		Workspace:   at.workspace,
	}, probe.asked[0])
	assert.Equal(t, FactsKey("claude-code", "8f2c6a1e"), probe.gates[0].Session())
}

// A subagent magus never saw spawned, holding no lease, shares the main session's id and
// has no Parent; only the payload's agent id tells the policy it is not the main session.
func TestCommandRuleSeesTheHostsSubagentID(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &commandRuleProbe{}
	unseen := `{"session_id":"8f2c6a1e","agent_id":"a534fcfe","hook_event_name":"PreToolUse","tool_name":"Bash",` +
		`"tool_input":{"command":"git push origin HEAD:x"}}`
	Judge(ctx, Dependencies{CommandRule: probe.rule()}, Request{Input: unseen, Host: "claude-code"})
	Judge(ctx, Dependencies{CommandRule: probe.rule()}, Request{Input: claudeBashEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 2)
	sub, main := probe.asked[0], probe.asked[1]
	assert.Equal(t, types.CommandRequest{
		Host:    "claude-code",
		Session: main.Session,
		Command: "git push origin HEAD:x",
		Commands: []types.CommandInvocation{{
			Program: "git",
			Args:    []string{"push", "origin", "HEAD:x"},
			VCS:     &types.VCSInvocation{Tool: "git", Subcommand: "push", Args: []string{"origin", "HEAD:x"}},
		}},
		Agent:       "a534fcfe",
		Role:        types.AgentRoleRoot,
		Thread:      types.AgentThreadSub,
		ThreadKnown: true,
		Checkout:    sub.Checkout, // read from the fixture's own checkout, which this test does not shape
		Dir:         sub.Dir,
		Workspace:   sub.Workspace,
	}, sub, "the subagent reports the main session's id; magus never saw it spawned, so no Parent or Lease")
	assert.Empty(t, main.Agent, "the main agent carries no subagent id")
}

// A bare command string, the form a host with no envelope sends, reaches the rule too.
func TestCommandRuleSeesABareCommand(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &commandRuleProbe{}
	Judge(ctx, Dependencies{CommandRule: probe.rule()}, Request{Input: "ls -la", Host: "opencode"})
	require.Len(t, probe.asked, 1)
	assert.Equal(t, []types.CommandInvocation{{Program: "ls", Args: []string{"-la"}}}, probe.asked[0].Commands)
}

// A worker's request carries its lease row, resolved the way every lease-scoped rule
// resolves it.
func TestCommandRuleSeesTheWorkersLease(t *testing.T) {
	row := types.Job{ID: "wave/worker", Criteria: "the guard", WritePaths: []string{"internal/guard/**"}, State: types.StateRunning, Registered: 1}
	ctx, _ := fleetFixture(t, row)
	bindCaller(t, ctx, hookAttribution{Host: "claude-code", Session: "8f2c6a1e"}, row.ID)

	probe := &commandRuleProbe{}
	Judge(ctx, Dependencies{CommandRule: probe.rule()}, Request{Input: claudeBashEnvelope, Host: "claude-code"})
	require.Len(t, probe.asked, 1)
	assert.Equal(t, types.AgentRoleWorker, probe.asked[0].Role)
	require.NotNil(t, probe.asked[0].Lease)
	assert.Equal(t, row.ID, probe.asked[0].Lease.ID)
	assert.Equal(t, row.WritePaths, probe.asked[0].Lease.WritePaths)
}

func TestCommandInvocations(t *testing.T) {
	cases := []struct {
		name, line string
		want       []types.CommandInvocation
	}{
		{"wrappers peel to the program", `env -u X nohup sh -c 'gh run watch 1'`,
			[]types.CommandInvocation{{Program: "gh", Args: []string{"run", "watch", "1"}}}},
		{"a path names its base program and its file", `/opt/bin/gh pr list`,
			[]types.CommandInvocation{{Program: "gh", Args: []string{"pr", "list"}, Path: "/opt/bin/gh"}}},
		{"a relative path resolves against the line's directory", `timeout 60 ./magus run lint .`,
			[]types.CommandInvocation{{Program: "magus", Args: []string{"run", "lint", "."}, Path: "/work/tree/magus"}}},
		{"a quoted path is still literal", `"../other/magus" ls`,
			[]types.CommandInvocation{{Program: "magus", Args: []string{"ls"}, Path: "/work/other/magus"}}},
		{"a program found on PATH has no file", `magus ls`,
			[]types.CommandInvocation{{Program: "magus", Args: []string{"ls"}}}},
		{"a variable leaves the file unknown", `$T/magus --root $T ls`,
			[]types.CommandInvocation{{Program: "magus", Args: []string{"--root", "", "ls"}}}},
		{"a cd earlier on the line leaves a relative file unknown", `cd sub && ./magus ls && /abs/magus ls`,
			[]types.CommandInvocation{
				{Program: "cd", Args: []string{"sub"}},
				{Program: "magus", Args: []string{"ls"}},
				{Program: "magus", Args: []string{"ls"}, Path: "/abs/magus"},
			}},
		{"a program reparsed from a -c payload has no file", `sh -c './magus ls'`,
			[]types.CommandInvocation{{Program: "magus", Args: []string{"ls"}}}},
		{"a while loop marks its condition and body", `while true; do gh run list; sleep 5; done; echo done`,
			[]types.CommandInvocation{
				{Program: "true", Args: []string{}, Repeats: true},
				{Program: "gh", Args: []string{"run", "list"}, Repeats: true},
				{Program: "sleep", Args: []string{"5"}, Repeats: true},
				{Program: "echo", Args: []string{"done"}},
			}},
		{"an until loop repeats too", `until gh pr checks 1; do sleep 30; done`,
			[]types.CommandInvocation{
				{Program: "gh", Args: []string{"pr", "checks", "1"}, Repeats: true},
				{Program: "sleep", Args: []string{"30"}, Repeats: true},
			}},
		{"a for loop walks a list and repeats nothing", `for i in 1 2; do gh pr view $i; done`,
			[]types.CommandInvocation{{Program: "gh", Args: []string{"pr", "view", ""}}}},
		{"a for loop that sleeps between passes polls", `for i in 1 2 3; do gh pr view 7 --json state; sleep 60; done`,
			[]types.CommandInvocation{
				{Program: "gh", Args: []string{"pr", "view", "7", "--json", "state"}, Repeats: true},
				{Program: "sleep", Args: []string{"60"}, Repeats: true},
			}},
		{"a line that does not parse runs nothing", `gh pr view "`, nil},
		{"git's global options before its subcommand are taken out", `git -C .. -c user.name=x --no-pager commit -q -m "trim a fragment"`,
			[]types.CommandInvocation{{
				Program: "git", Args: []string{"-C", "..", "-c", "user.name=x", "--no-pager", "commit", "-q", "-m", "trim a fragment"},
				VCS: &types.VCSInvocation{Tool: "git", Subcommand: "commit", Args: []string{"-q", "-m", "trim a fragment"}},
			}}},
		{"git reads its help flag as the help command", `git --help commit`,
			[]types.CommandInvocation{{
				Program: "git", Args: []string{"--help", "commit"},
				VCS: &types.VCSInvocation{Tool: "git", Subcommand: "help", Args: []string{"commit"}},
			}}},
		{"hg's global options are taken out wherever they sit", `hg -R ../repo --config ui.username=x commit -m "trim a fragment" -y`,
			[]types.CommandInvocation{{
				Program: "hg", Args: []string{"-R", "../repo", "--config", "ui.username=x", "commit", "-m", "trim a fragment", "-y"},
				VCS: &types.VCSInvocation{Tool: "hg", Subcommand: "commit", Args: []string{"-m", "trim a fragment"}},
			}}},
		{"sl reads its own global options", `sl --configfile x.rc ci -m "trim a fragment"`,
			[]types.CommandInvocation{{
				Program: "sl", Args: []string{"--configfile", "x.rc", "ci", "-m", "trim a fragment"},
				VCS: &types.VCSInvocation{Tool: "sl", Subcommand: "ci", Args: []string{"-m", "trim a fragment"}},
			}}},
		{"jj's global options are taken out after the subcommand too", `jj --no-pager describe -r @- -m "trim a fragment" --color=never`,
			[]types.CommandInvocation{{
				Program: "jj", Args: []string{"--no-pager", "describe", "-r", "@-", "-m", "trim a fragment", "--color=never"},
				VCS: &types.VCSInvocation{Tool: "jj", Subcommand: "describe", Args: []string{"-r", "@-", "-m", "trim a fragment"}},
			}}},
		{"a substituted message renders empty in the parsed arguments too", `jj commit -m "$(cat msg)"`,
			[]types.CommandInvocation{
				{
					Program: "jj", Args: []string{"commit", "-m", ""},
					VCS: &types.VCSInvocation{Tool: "jj", Subcommand: "commit", Args: []string{"-m", ""}},
				},
				{Program: "cat", Args: []string{"msg"}},
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, commandInvocations(tc.line, DialectBash, "/work/tree"))
		})
	}
}

// Strengthen only: a built-in deny stands and the rule is not even asked, a workspace deny
// replaces a pass, and a workspace advice fills a pass.
func TestCommandRuleStrengthensOnly(t *testing.T) {
	t.Run("a built-in deny stands", func(t *testing.T) {
		ctx, _ := spawnFixture(t)
		probe := &commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAllow}}
		v := Judge(ctx, Dependencies{CommandRule: probe.rule()}, Request{Input: "git stash", Host: "claude-code"})
		assert.Equal(t, "deny", v.Decision)
		assert.NotEqual(t, workspaceCommandRule, v.Rule)
		assert.Empty(t, probe.asked)
	})
	t.Run("a workspace deny blocks", func(t *testing.T) {
		ctx, cacheDir := spawnFixture(t)
		probe := &commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "Ask for the board once."}}
		v := Judge(ctx, Dependencies{CommandRule: probe.rule()}, Request{Input: "gh run watch 1", Host: "claude-code"})
		assert.Equal(t, verdictWithRule("deny", workspaceCommandRule), unworded(v))
		assert.Contains(t, v.Reason, "Ask for the board once.")

		commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
		require.Len(t, commands, 1)
		assert.Equal(t, decidedByWorktree, commands[0].DecidedBy)
	})
	t.Run("a workspace advice fills a pass", func(t *testing.T) {
		ctx, _ := spawnFixture(t)
		probe := &commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAdvise, Reason: "Batch-poll instead."}}
		v := Judge(ctx, Dependencies{CommandRule: probe.rule()}, Request{Input: "gh pr checks 1", Host: "claude-code"})
		assert.Equal(t, Verdict{SchemaVersion: v.SchemaVersion, Decision: "advise", Context: "Batch-poll instead.", Rule: workspaceCommandRule}, v)
	})
	t.Run("an allow changes nothing", func(t *testing.T) {
		ctx, _ := spawnFixture(t)
		probe := &commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAllow}}
		v := Judge(ctx, Dependencies{CommandRule: probe.rule()}, Request{Input: "ls", Host: "claude-code"})
		assert.Equal(t, "pass", v.Decision)
		require.Len(t, probe.asked, 1)
	})
}

// A broken rule judges nothing and says so once per session, the stance the spawn rule
// takes; the command still runs.
func TestCommandRuleFailureFailsOpen(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	probe := &commandRuleProbe{err: errors.New("magus\\guard.command: the rule raised: boom")}
	deps := Dependencies{CommandRule: probe.rule()}

	first := Judge(ctx, deps, Request{Input: "ls", Host: "claude-code", Session: "s1"})
	assert.Equal(t, verdictWithRule("advise", string(advisoryCommandRuleFailed)), unworded(first))
	assert.Contains(t, first.Context, "The working tree's magus\\guard.command rule judged nothing: the rule failed: magus\\guard.command: the rule raised: boom")
	assert.Contains(t, first.Context, "Only the built-in rules applied to this command.")

	repeat := Judge(ctx, deps, Request{Input: "ls", Host: "claude-code", Session: "s1"})
	assert.Equal(t, "advise", repeat.Decision)
	assert.NotContains(t, repeat.Context, "boom", "the repeat is one line")
	again := Judge(ctx, deps, Request{Input: "ls", Host: "claude-code", Session: "s1"})
	assert.Equal(t, "pass", again.Decision, "the one line is said once to a caller, not on every command")

	commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.NotEmpty(t, commands)
	assert.Equal(t, []trail.RuleFailure{{Side: decidedByWorktree, Error: "the rule failed: magus\\guard.command: the rule raised: boom"}},
		readCommandBlob(t, cacheDir, commands[0]).RuleFailures)
}

// commandBlob is the part of an agent_command response blob these tests read.
type commandBlob struct {
	RuleFailures []trail.RuleFailure `json:"rule_failures"`
}

func readCommandBlob(t *testing.T, cacheDir string, e trail.Event) commandBlob {
	t.Helper()
	body, err := trail.ReadBlob(cacheDir, e.ResponseRef)
	require.NoError(t, err)
	var got commandBlob
	require.NoError(t, json.Unmarshal(body, &got))
	return got
}

// The approved rule and the working-tree rule both run and the stricter answer stands, so
// an agent's edit to the policy can tighten it but not loosen it.
func TestCommandRulesKeepTheStricterSide(t *testing.T) {
	deny := types.GuardVerdict{Decision: types.GuardDeny, Reason: "no watching"}
	allow := types.GuardVerdict{Decision: types.GuardAllow}
	cases := []struct {
		name           string
		live, approved *types.GuardVerdict
		want           string
		decidedBy      string
	}{
		{"a tightening in the working tree applies at once", &deny, &allow, "deny", decidedByWorktree},
		{"a loosening in the working tree waits for approval", &allow, &deny, "deny", decidedByApproved},
		{"a rule deleted from the working tree still answers from the approved side", nil, &deny, "deny", decidedByApproved},
		{"no approved side runs the working tree alone", &allow, nil, "pass", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			deps := Dependencies{}
			if tc.live != nil {
				deps.CommandRule = (&commandRuleProbe{answer: *tc.live}).rule()
			}
			if tc.approved != nil {
				approved := (&commandRuleProbe{answer: *tc.approved}).rule()
				deps.ApprovedCommandRule = func(context.Context) (workspace.CommandRule, error) { return approved, nil }
			}
			v := Judge(ctx, deps, Request{Input: "gh run watch 1", Host: "claude-code"})
			assert.Equal(t, tc.want, v.Decision)

			commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
			require.Len(t, commands, 1)
			assert.Equal(t, tc.decidedBy, commands[0].DecidedBy)
		})
	}
}

// A working tree that fails to load cannot switch the approved command rule off.
func TestBrokenWorkingTreeStillRunsTheApprovedCommandRule(t *testing.T) {
	ctx, _ := spawnFixture(t)
	approved := (&commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardDeny, Reason: "no watching"}}).rule()
	deps := Dependencies{
		LoadFailure:         errors.New("magusfile.buzz:3:1: expected expression"),
		ApprovedCommandRule: func(context.Context) (workspace.CommandRule, error) { return approved, nil },
	}
	v := Judge(ctx, deps, Request{Input: "gh run watch 1", Host: "claude-code"})
	assert.Equal(t, "deny", v.Decision)
	assert.Contains(t, v.Reason, "no watching")
}

// Resolving the approved side is time an agent can spend, so running out of it denies a
// command that changes state rather than judging it without that side, and lets one that
// only reads through with a note naming the phase and its budget.
func TestApprovedCommandRuleTimeoutDeniesAChangeAndAdvisesARead(t *testing.T) {
	slow := Dependencies{ApprovedCommandRule: func(ctx context.Context) (workspace.CommandRule, error) {
		<-ctx.Done()
		return nil, nil
	}}
	judge := func(t *testing.T, command string) (Verdict, []trail.RuleFailure) {
		ctx, cacheDir := spawnFixture(t)
		ctx, cancel := context.WithTimeout(ctx, judgeReserve+600*time.Millisecond)
		defer cancel()
		v := Judge(ctx, slow, Request{Input: command, Host: "claude-code", Session: "s1"})
		commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
		require.Len(t, commands, 1)
		return v, readCommandBlob(t, cacheDir, commands[0]).RuleFailures
	}

	v, failures := judge(t, "touch notes.txt")
	assert.Equal(t, verdictWithRule("deny", workspaceCommandRule), unworded(v))
	assert.Regexp(t, `magus ran out of time judging this command: resolving the approved magus\\guard\.command rule did not finish within \d+ms`, v.Reason)
	require.Len(t, failures, 1)
	assert.Regexp(t, `^resolving the rule did not finish within \d+ms$`, failures[0].Error)

	v, _ = judge(t, "ls")
	assert.Equal(t, verdictWithRule("advise", string(advisoryCommandRuleFailed)), unworded(v))
	assert.Regexp(t, `The approved magus\\guard\.command rule judged nothing: resolving the rule did not finish within \d+ms`, v.Context)
}

// A rule that runs past its share of the deadline is named with the budget it had.
func TestSlowWorkingTreeCommandRuleIsNamedWithItsBudget(t *testing.T) {
	ctx, _ := spawnFixture(t)
	ctx, cancel := context.WithTimeout(ctx, judgeReserve+600*time.Millisecond)
	defer cancel()
	deps := Dependencies{CommandRule: func(ctx context.Context, _ types.CommandRequest, _ hint.Gate) (types.GuardVerdict, error) {
		<-ctx.Done()
		return types.GuardVerdict{}, ctx.Err()
	}}

	v := Judge(ctx, deps, Request{Input: "touch notes.txt", Host: "claude-code", Session: "s1"})
	assert.Equal(t, "deny", v.Decision)
	assert.Regexp(t, `the working tree's magus\\guard\.command rule did not answer within \d+ms`, v.Reason)
}

// The checkout's state costs processes, so it is read only for a line that pushes or
// commits. A push wins over a commit on the same line, since the push rules read it.
func TestCommandRuleSeesCheckoutStateOnlyForAPushOrACommit(t *testing.T) {
	ctx, _ := spawnFixture(t)
	var read []string
	state := &types.CheckoutState{Branch: "main", Base: "origin/main"}
	deps := Dependencies{CheckoutState: func(_ context.Context, dir string) *types.CheckoutState { read = append(read, dir); return state }}
	probe := &commandRuleProbe{}
	deps.CommandRule = probe.rule()
	at := hookLocation(ctx, Dependencies{})

	for _, line := range []string{
		`git commit -m "trim a fragment"`,
		`jj -R ../j describe -m "trim a fragment"`,
		`hg ci -m "trim a fragment"`,
		`git -C a commit -m x && git -C b push origin HEAD:topic`,
		`git log -m`,
	} {
		Judge(ctx, deps, Request{Input: line, Host: "claude-code"})
	}
	require.Len(t, probe.asked, 5)
	for i := range 4 {
		assert.Equal(t, state, probe.asked[i].Checkout, probe.asked[i].Command)
	}
	assert.Nil(t, probe.asked[4].Checkout, "git log records nothing")
	assert.Equal(t, []string{at.dir, filepath.Join(at.dir, "../j"), at.dir, filepath.Join(at.dir, "b")}, read)
}

// A push's checkout is the one its -C names.
func TestCommandRuleSeesThePushedCheckout(t *testing.T) {
	ctx, _ := spawnFixture(t)
	var read []string
	state := &types.CheckoutState{RemoteBranches: []string{"origin/main"}}
	deps := Dependencies{CheckoutState: func(_ context.Context, dir string) *types.CheckoutState { read = append(read, dir); return state }}
	probe := &commandRuleProbe{}
	deps.CommandRule = probe.rule()

	Judge(ctx, deps, Request{Input: "git status", Host: "claude-code"})
	Judge(ctx, deps, Request{Input: "git -C ../other push -q origin HEAD:topic", Host: "claude-code"})
	require.Len(t, probe.asked, 2)
	assert.Nil(t, probe.asked[0].Checkout)
	assert.Equal(t, state, probe.asked[1].Checkout)
	at := hookLocation(ctx, Dependencies{})
	assert.Equal(t, []string{filepath.Join(at.dir, "../other")}, read, "read once, in the checkout -C names")
}

// A command magus itself served stays served: the rule's advice stands down, as every
// advisory does on one.
func TestCommandRuleAdviceStandsDownOnAServedCommand(t *testing.T) {
	ctx, _ := spawnFixture(t)
	probe := &commandRuleProbe{answer: types.GuardVerdict{Decision: types.GuardAdvise, Reason: "Batch-poll instead."}}
	v, rec := gradeWorkspaceCommand(ctx, Dependencies{CommandRule: probe.rule()},
		Verdict{Decision: "pass"}, commandRuleInput{command: "gh pr checks 1", preauth: "next"}, hookAttribution{}, hookLocation(ctx, Dependencies{}))
	assert.Equal(t, "pass", v.Decision)
	assert.Empty(t, rec.decidedBy)

	probe.answer = types.GuardVerdict{Decision: types.GuardDeny, Reason: "no"}
	v, _ = gradeWorkspaceCommand(ctx, Dependencies{CommandRule: probe.rule()},
		Verdict{Decision: "pass"}, commandRuleInput{command: "gh pr checks 1", preauth: "next"}, hookAttribution{}, hookLocation(ctx, Dependencies{}))
	assert.Equal(t, "deny", v.Decision, "its deny still holds")
}

// slowCompiles is a telemetry provider for one load that counts its Buzz compile phases and
// sleeps slow on the first one past free: a load that compiles more than free phases stands
// in for a cold compile of the approved policy.
type slowCompiles struct {
	observability.Provider
	free   int32
	slow   time.Duration
	phases atomic.Int32
}

func (*slowCompiles) Enabled() bool { return true }

func (p *slowCompiles) RecordBuzzCompile(context.Context, float64, string, string) {
	if p.phases.Add(1) == p.free+1 {
		time.Sleep(p.slow)
	}
}

func (*slowCompiles) RecordBuzzImport(context.Context, float64, string, string)      {}
func (*slowCompiles) RecordBuzzExec(context.Context, float64, string, string)        {}
func (*slowCompiles) RecordBuzzJITRun(context.Context)                               {}
func (*slowCompiles) RecordBuzzHostCall(context.Context, observability.BuzzHostCall) {}
func (*slowCompiles) RecordBuzzVMFault(context.Context, string)                      {}

// A cold compile of the approved policy that runs past its share of the hook's deadline
// costs that one hook: the compile it paid for is kept, so the next hook compiles nothing
// and the approved rule answers.
func TestColdApprovedCompileLapsesOnlyOnce(t *testing.T) {
	testkit.Isolate(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := testkit.NewRepo(t, map[string]string{
		"magus.yaml":     "{}\n",
		"magusfile.buzz": "import \"magus\";\n\nmagus\\guard.command(fun (req: CommandRequest) > GuardVerdict {\n    return magus\\guard.deny(\"committed\");\n});\n",
	})
	repo.Magusfile("import \"magus\";\n")
	rules, err := magus.LoadGuardRules(t.Context(), repo.Root(), types.VCSOptions{})
	require.NoError(t, err)
	require.Nil(t, rules.CommandRule(), "the working tree drops the rule, so the approved one is pending")

	const share = time.Second
	resolve := func(ctx context.Context) (ruleCall, error) {
		rule, err := rules.ApprovedCommandRule(ctx)
		if rule == nil {
			return nil, err
		}
		return func(ctx context.Context) (types.GuardVerdict, error) {
			return rule(ctx, types.CommandRequest{Command: "touch notes.txt"}, hint.NewGate(t.TempDir(), "s"))
		}, err
	}
	hook := func(provider *slowCompiles) rulesAnswer {
		// The resolve holds two shares of the deadline to the approved rule's one.
		ctx, cancel := context.WithTimeout(observability.WithProvider(t.Context(), provider), judgeReserve+share*3/2)
		defer cancel()
		return askWorkspaceRules(ctx, seamCommand, Dependencies{}, resolve, nil)
	}

	// What a load costs with the policy's compile kept, against one that compiles it. The
	// rest is what the module it imports declares, compiled on every load.
	cold, warm := &slowCompiles{}, &slowCompiles{}
	require.Nil(t, hook(cold).lapse)
	require.Nil(t, hook(warm).lapse)
	free := warm.phases.Load()
	require.Less(t, free, cold.phases.Load(), "the second load does not compile the policy")

	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	first := hook(&slowCompiles{free: free, slow: 2 * share})
	require.NotNil(t, first.lapse, "the cold compile runs past the hook's deadline")
	assert.Equal(t, rulesAnswer{
		answer:   types.GuardVerdict{Decision: types.GuardDeny, Reason: "committed"},
		by:       decidedByApproved,
		answered: []string{decidedByApproved},
	}, hook(&slowCompiles{free: free, slow: 2 * share}), "the next hook answers")
}

// The approved side runs what the committed revision holds however the working tree edits
// the policy: an edit is not found under the approved chunks' keys, and the chunks a
// working-tree load compiled, moved over the approved ones under the same names, do not
// verify there.
func TestApprovedCommandRuleIgnoresAWorkingTreeEditOfItsSources(t *testing.T) {
	testkit.Isolate(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const rule = "import \"magus\";\n\nexport fun rule(req: CommandRequest) > GuardVerdict {\n    return magus\\guard.%s;\n}\n"
	repo := testkit.NewRepo(t, map[string]string{
		"magus.yaml":      "{}\n",
		"magusfile.buzz":  "import \"magus\";\nimport \"./lib/policy\" as policy;\n\nmagus\\guard.command(policy\\rule);\n",
		"lib/policy.buzz": fmt.Sprintf(rule, `deny("committed")`),
	})
	root := repo.Root()
	worktreeStore := filepath.Join(cache, "magus", "buzz-bytecode")
	approvedStore := filepath.Join(cache, "magus", "buzz-bytecode-approved")
	decide := func(rule workspace.CommandRule) types.GuardDecision {
		t.Helper()
		require.NotNil(t, rule)
		got, err := rule(t.Context(), types.CommandRequest{Command: "ls"}, hint.NewGate(t.TempDir(), "s"))
		require.NoError(t, err)
		return got.Decision
	}
	judge := func(edit string) []types.GuardDecision {
		t.Helper()
		repo.Write("lib/policy.buzz", fmt.Sprintf(rule, edit))
		rules, err := magus.LoadGuardRules(t.Context(), root, types.VCSOptions{})
		require.NoError(t, err)
		approved, err := rules.ApprovedCommandRule(t.Context())
		require.NoError(t, err)
		return []types.GuardDecision{decide(rules.CommandRule()), decide(approved)}
	}

	assert.Equal(t, []types.GuardDecision{types.GuardAllow, types.GuardDeny}, judge("allow()"))

	moved := 0
	require.NoError(t, filepath.WalkDir(worktreeStore, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(worktreeStore, path)
		require.NoError(t, err)
		dst := filepath.Join(approvedStore, rel)
		if _, err := os.Stat(dst); err == nil {
			moved++
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o700))
		return os.WriteFile(dst, data, 0o600)
	}))
	require.Positive(t, moved, "the unedited magusfile's working-tree chunk has the approved chunk's name")

	assert.Equal(t, []types.GuardDecision{types.GuardAdvise, types.GuardDeny}, judge(`advise("edited again")`))
}
