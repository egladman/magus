package guard

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
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
		Dir:         at.dir,
		Workspace:   at.workspace,
	}, probe.asked[0])
	assert.Equal(t, FactsKey("claude-code", "8f2c6a1e"), probe.gates[0].Session())
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
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	require.NoError(t, job.Checkout{CacheDir: cacheDir, Session: "8f2c6a1e"}.Bind(row.ID))

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
		{"a line that does not parse runs nothing", `gh pr view "`, nil},
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
		assert.Equal(t, "deny", v.Decision)
		assert.Equal(t, workspaceCommandRule, v.Rule)
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
	assert.Equal(t, "advise", first.Decision)
	assert.Equal(t, string(advisoryCommandRuleFailed), first.Rule)
	assert.Contains(t, first.Context, "The working tree's magus\\guard.command rule judged nothing: the rule failed: magus\\guard.command: the rule raised: boom")
	assert.Contains(t, first.Context, "Only the built-in rules applied to this command.")

	repeat := Judge(ctx, deps, Request{Input: "ls", Host: "claude-code", Session: "s1"})
	assert.Equal(t, "advise", repeat.Decision)
	assert.NotContains(t, repeat.Context, "boom", "the repeat is one line")

	commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.NotEmpty(t, commands)
	body, err := trail.ReadBlob(cacheDir, commands[0].ResponseRef)
	require.NoError(t, err)
	var got struct {
		RuleFailures []trail.RuleFailure `json:"rule_failures"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, []trail.RuleFailure{{Side: decidedByWorktree, Error: "the rule failed: magus\\guard.command: the rule raised: boom"}}, got.RuleFailures)
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

// Resolving the approved side is time an agent can spend, so running out denies rather
// than judging without it, as it does for a spawn.
func TestApprovedCommandRuleTimeoutDenies(t *testing.T) {
	prev := approvedResolveTimeout
	approvedResolveTimeout = 50 * time.Millisecond
	t.Cleanup(func() { approvedResolveTimeout = prev })

	ctx, _ := spawnFixture(t)
	deps := Dependencies{ApprovedCommandRule: func(ctx context.Context) (workspace.CommandRule, error) {
		<-ctx.Done()
		return nil, nil
	}}
	v := Judge(ctx, deps, Request{Input: "ls", Host: "claude-code"})
	assert.Equal(t, "deny", v.Decision)
	assert.Contains(t, v.Reason, approvedRuleTimedOut(seamCommand))
}

// The checkout's state costs processes, so it is read only for a line that pushes.
func TestCommandRuleSeesCheckoutStateOnlyForAPush(t *testing.T) {
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
