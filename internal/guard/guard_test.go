package guard

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	// Blank-imported so its init installs the spell registry's ensure hook, exactly as
	// cmd/magus/packs_interp.go does for the real binary: without it,
	// project.DefaultSpellRegistry().All() in testDependencies below runs against a registry
	// nothing ever populated, and every raw-tool test would match against an empty
	// catalog. See internal/interp/bindings/spell.go's init.
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/interp"
	_ "github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// verdictWithRule is the verdict a rule reaches before any wording or lease attribution:
// pair it with unworded so a test compares the whole struct and still checks the prose by
// substring.
func verdictWithRule(decision, rule string) types.GuardJudgment {
	return types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decision, Rule: rule}
}

// unworded is v without the fields a rule test leaves to other assertions: the reason and
// context prose, which the test checks by substring, and the lease attribution, which follows
// the environment the test ran in.
func unworded(v types.GuardJudgment) types.GuardJudgment {
	v.Reason, v.Context, v.Lease, v.LeaseFrom = "", "", "", ""
	return v
}

// judgedAs is v with only its decision and rule kept, for a test whose other assertions own
// the wording, the lease and the remedy: compare it with a types.GuardJudgment of both.
func judgedAs(v types.GuardJudgment) types.GuardJudgment {
	return types.GuardJudgment{Decision: v.Decision, Rule: v.Rule}
}

// WorkspaceInspect and WorkspaceCacheDir are the root package's Inspect and ResolveCacheDir.
// This package cannot import the root package, which imports it, so the external test
// package sets them (see workspace_rules_test.go) before any test runs.
var (
	WorkspaceInspect  func(ctx context.Context, root string) (types.WorkspaceRepository, error)
	WorkspaceCacheDir func(root string) (string, error)
)

// testDependencies resolves the workspace the way the CLI's hookDeps does, so a rule graded
// here reads the same tree the hook would.
func testDependencies() Dependencies {
	return Dependencies{
		Inspect: func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
			if root == "" {
				found, err := workspace.FindRoot("")
				if err != nil {
					return nil, err
				}
				root = found
			}
			return WorkspaceInspect(ctx, root)
		},
		CacheDir: func(root string) (string, error) {
			if root == "" {
				found, err := workspace.FindRoot("")
				if err != nil {
					return "", err
				}
				root = found
			}
			return WorkspaceCacheDir(root)
		},
		Spells: project.DefaultSpellRegistry().All,
	}
}

// TestDecodeHookEnvelope_CommandStillWinsOverPrompt guards the ordering that makes the spawn
// branch purely additive: a payload carrying both is judged as the command it carries, so no
// envelope the guard already evaluated can start skipping the guard because a prompt appeared
// beside it.
func TestDecodeHookEnvelope_CommandStillWinsOverPrompt(t *testing.T) {
	req, ok := decodeTestEnvelope(t, `{"tool_input":{"command":"git stash","prompt":"delegate this"}}`)
	require.True(t, ok)
	assert.Equal(t, "git stash", req.Value)
	assert.False(t, req.IsSpawn)

	req, ok = decodeTestEnvelope(t, `{"tool_input":{"file_path":"MAGUS.md","prompt":"delegate this"}}`)
	require.True(t, ok)
	assert.Equal(t, "MAGUS.md", req.Value)
	assert.True(t, req.IsPath)
	assert.False(t, req.IsSpawn)
}

// TestDecodeHookEnvelope pins reading a host's hook payload directly. Without it, wiring
// the guard means `jq -r .tool_input.command | magus shell`: an extra dependency on the
// critical path of every tool call, in the one place that must not fail.
func TestDecodeHookEnvelope(t *testing.T) {
	cmdPayload := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash",` +
		`"tool_input":{"command":"magus run ci | tail"}}`
	req, ok := decodeTestEnvelope(t, cmdPayload)
	require.True(t, ok)
	assert.Equal(t, hookRequest{Value: "magus run ci | tail", Who: hookAttribution{Session: "s1", Event: "PreToolUse"}}, req)

	// A file_path payload is a WRITE, so the envelope decides the --path question too.
	writePayload := `{"hook_event_name":"PreToolUse","tool_input":{"file_path":"MAGUS.md"}}`
	req, ok = decodeTestEnvelope(t, writePayload)
	require.True(t, ok)
	assert.Equal(t, "MAGUS.md", req.Value)
	assert.True(t, req.IsPath, "a file_path payload must be judged as a path, not as a command")

	// Anything that is not a usable envelope is left alone for the bare-command form.
	for _, raw := range []string{
		"magus run ci | tail",           // a plain command
		`{"tool_input":{}}`,             // an envelope with nothing to judge
		`{not json`,                     // malformed
		`{"tool_input":{"command":""}}`, // explicitly empty
	} {
		_, ok := decodeTestEnvelope(t, raw)
		assert.False(t, ok, "must fall through to the literal form: %q", raw)
	}

	// TestHookCmd_EnvelopeWithNothingToJudge (cmd/magus/hook_test.go) exercises the same
	// payload end to end through hookCmd; this pins the decoder's own half of it, which that
	// test cannot reach directly since decodeHookEnvelope is unexported to this package.
	const nothingToJudge = `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"TodoWrite",` +
		`"tool_input":{"todos":[{"content":"never use sed -i here"}]}}`
	req, ok = decodeTestEnvelope(t, nothingToJudge)
	require.True(t, ok, "a payload naming a host hook event is an envelope")
	assert.True(t, req.NothingToJudge)
	assert.Empty(t, req.Value)

	// The same JSON with no envelope marker on it is still judged as the text it is: only a
	// payload that identifies itself as a host hook may claim the exemption.
	_, ok = decodeTestEnvelope(t, `{"tool_input":{"todos":[{"content":"never use sed -i here"}]}}`)
	assert.False(t, ok)
}

// TestDecodeHookEnvelopeReadsEveryWritePathSpelling: the path decoder used to see
// `file_path` alone, so a host tool naming its target anything else fell through to
// NothingToJudge and every write rule went unrun.
func TestDecodeHookEnvelopeReadsEveryWritePathSpelling(t *testing.T) {
	for name, payload := range map[string]string{
		"the documented spelling": `{"tool_name":"Write","tool_input":{"file_path":"MAGUS.md"}}`,
		"a notebook":              `{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"MAGUS.md"}}`,
		"any other _path key":     `{"tool_name":"Other","tool_input":{"target_path":"MAGUS.md"}}`,
	} {
		req, ok := decodeTestEnvelope(t, payload)
		require.True(t, ok, name)
		assert.True(t, req.IsPath, name)
		assert.Equal(t, "MAGUS.md", req.Value, name)
	}

	// A field arriving with an unexpected type still reaches the MCP arm. Typed, it
	// failed the unmarshal outright and the raw JSON was judged as a shell line, which is
	// the one outcome the default arm of the decoder exists to prevent.
	req, ok := decodeTestEnvelope(t, `{"tool_name":"mcp__magus__buzz","tool_input":{"path":123}}`)
	require.True(t, ok)
	assert.Equal(t, "magus buzz 123", req.Value)
}

// TestGuardGradesTwoSessionsInOneCheckoutSeparately is the enforcement half of the
// per-session binding: each session's own record decides which write paths its writes are graded
// against, so two workers sharing a checkout are each denied outside their own paths
// rather than both running ungraded.
func TestGuardGradesTwoSessionsInOneCheckoutSeparately(t *testing.T) {
	one := types.Job{
		ID: "wave/one", Criteria: "the store", WritePaths: []string{"internal/job/**"},
		State: types.StateRunning, Checkpoint: "rev", ReportedBase: "rev", BaseVerdict: types.BaseMatch, Registered: 1,
	}
	two := types.Job{
		ID: "wave/two", Criteria: "the guard", WritePaths: []string{"internal/guard/**"},
		State: types.StateRunning, Checkpoint: "rev", ReportedBase: "rev", BaseVerdict: types.BaseMatch, Registered: 1,
	}
	ctx, _ := fleetFixture(t, one, two)

	bindCaller(t, ctx, hookAttribution{Host: "test-host", Session: "session-one"}, one.ID)
	bindCaller(t, ctx, hookAttribution{Host: "test-host", Session: "session-two"}, two.ID)

	first := Judge(ctx, Dependencies{}, Request{
		Input: "internal/guard/spawn.go", IsPath: true, Session: "session-one", Host: "test-host",
	})
	assert.Equal(t, one.ID, first.Lease, "session one is graded under its own row")

	second := Judge(ctx, Dependencies{}, Request{
		Input: "internal/guard/spawn.go", IsPath: true, Session: "session-two", Host: "test-host",
	})
	assert.Equal(t, two.ID, second.Lease, "session two is graded under its own row, in the same checkout")
	assert.NotEqual(t, first.Lease, second.Lease)
}

// TestHostUnnamedRefusesWithTheCodeAndTheRemedy pins the whole verdict: a deny, never an
// ask or a pass, carrying MGS3024 and the command that fixes it, and naming no form so
// the sh and Buzz forms of one template reply byte for byte alike.
func TestHostUnnamedRefusesWithTheCodeAndTheRemedy(t *testing.T) {
	t.Parallel()
	got := hostUnnamed()
	assert.Equal(t, types.GuardJudgment{SchemaVersion: agent.GuardSchemaVersion, Decision: decisionDeny, Reason: got.Reason}, got)
	assert.Contains(t, got.Reason, string(types.HookHostUnnamed))
	assert.Contains(t, got.Reason, "--agent-name")
	assert.Contains(t, got.Reason, "magus describe harness")
	assert.NotContains(t, got.Reason, "buzz")
}

// TestJudgeRefusesInstalledGlueThatNamesNoHost is the refusal reached through Judge, before
// any rule or dependency is consulted: zero Dependencies would fail a rule's lookup, so a
// verdict equal to hostUnnamed's proves nothing past the check ran.
func TestJudgeRefusesInstalledGlueThatNamesNoHost(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"", "  "} {
		got := Judge(context.Background(), Dependencies{}, Request{Input: "ls", Form: "sh", Host: host})
		assert.Equal(t, hostUnnamed(), got, "host %q", host)
	}
}

// TestJudgeLeavesCallersWithoutAFormAlone keeps the refusal to installed glue. A person
// running `magus shell` names no form and no host, and is judged as before.
func TestJudgeLeavesCallersWithoutAFormAlone(t *testing.T) {
	t.Parallel()
	got := Judge(context.Background(), testDependencies(), Request{Input: ""})
	assert.Equal(t, decisionPass, got.Decision, "an empty input from a person passes")
	assert.NotContains(t, got.Reason, string(types.HookHostUnnamed))
}

// A command typed at a terminal is the CLI's: it records no session and is not an agent's,
// while its terminal window still keys what the guard remembers for it.
func TestATerminalCallIsRecordedAsTheCLIWithNoSession(t *testing.T) {
	dir := t.TempDir()
	ctx := trail.ContextWithEntryPoint(WithLocation(t.Context(), dir, "/repo", ""), types.EntryPointCLI)

	Judge(ctx, Dependencies{}, Request{Input: "ls", Host: "test-host", Window: "tty:w1"})

	events := trailEvents(t, dir, trail.KindAgentCommand)
	require.Len(t, events, 1)
	assert.Equal(t, types.EntryPointCLI, events[0].EntryPoint)
	assert.Empty(t, events[0].Session, "a terminal window is not a host session")
	assert.NotContains(t, events[0].Label(), "agent", "a terminal call is never labeled an agent")

	assert.Equal(t, "test-host/tty:w1", hookAttribution{Host: "test-host", Window: "tty:w1"}.factsKey())
	assert.Equal(t, "test-host/s1", hookAttribution{Host: "test-host", Session: "s1", Window: "tty:w1"}.factsKey(),
		"a host session wins over the window")
	assert.Empty(t, hookAttribution{Host: "test-host"}.factsKey(), "neither leaves the gate its anonymous window")
}

// policyRule matches a rule's line in hack/policy/guard.buzz's header: `//   name (`.
var policyRule = regexp.MustCompile(`(?m)^//   ([a-z][a-z-]+) \(`)

// policyCase is one real hook input and the verdict this repository's policy owes it.
type policyCase struct {
	rule     string
	name     string
	input    map[string]any
	decision string
	// reason is a fragment the verdict's reason must carry, "" to check none.
	reason string
	// worker is the spawn title the caller was started under, "" for a person.
	worker string
	// checkout is what the guard reads about a pushing checkout, nil for any other line.
	checkout *types.CheckoutState
	// jobs are the rows the job store holds when the input arrives. A running row with no
	// checkout root is taken in the case's own checkout.
	jobs []types.Job
	// corruptStore replaces the job store with bytes it cannot decode.
	corruptStore bool
	// lease is the job the caller acts under, "" for the orchestrator or a person.
	lease string
	// binary places an executable ./magus in the workspace, as every checkout a session runs in
	// has; the capture rule answers differently when there is none.
	binary bool
}

// placeBinary puts an executable file named magus at the workspace root.
func placeBinary(t *testing.T, workspace string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "magus"), []byte("#!/bin/sh\n"), 0o755))
}

func agentSpawn(toolInput map[string]any) map[string]any {
	return map[string]any{"tool_name": "Agent", "tool_input": toolInput}
}

func bash(command string) map[string]any {
	return map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}}
}

// The Buzz tests in hack/policy/*.buzz pin each rule against argvs they build by hand.
// This pins the other half: the policy the root magusfile actually registers, reached
// through the Go guard from the shell line or tool call a host sends, so a parse the Go
// side changes, or a request field it stops filling, fails here and not in a session.
// Every rule the header lists needs a case.
func TestWorkspacePolicyJudgesRealHookInputs(t *testing.T) {
	root, err := workspace.FindRoot("")
	require.NoError(t, err)
	m, err := LoadWorkspaceRules(t.Context(), root, types.VCSOptions{})
	require.NoError(t, err, "the policy loads the way the hook loads it")
	require.NotNil(t, m.CommandRule(), "the root magusfile registers a command rule")
	require.NotNil(t, m.SpawnRule(), "and a spawn rule")
	require.NotNil(t, m.WriteRule(), "and a write rule")

	cases := []policyCase{
		{rule: "ci-watch", name: "gh pr checks --watch", input: bash("gh pr checks 183 --watch"), decision: decisionDeny, reason: "GREEN CHANGES NOTHING"},
		{rule: "ci-watch", name: "a while loop over gh pr checks", input: bash("while true; do gh pr checks 183; sleep 30; done"), decision: decisionDeny, reason: "GREEN CHANGES NOTHING"},
		{rule: "ci-watch", name: "a for loop that sleeps over gh pr checks", input: bash("for i in $(seq 1 20); do gh pr checks 183; sleep 30; done"), decision: decisionDeny, reason: "GREEN CHANGES NOTHING"},
		{rule: "ci-watch", name: "gh run watch behind env and timeout", input: bash("GH_TOKEN=x timeout 600 gh run watch 34069443069"), decision: decisionDeny, reason: "GREEN CHANGES NOTHING"},
		{rule: "ci-batch-poll", name: "one pull request's checks", input: bash("gh pr checks 183"), decision: decisionDeny, reason: "hack/ci/show-failures.buzz"},
		{rule: "ci-batch-poll", name: "all pull requests' checks", input: bash("gh pr list --state open --json number,mergeable,statusCheckRollup"), decision: decisionDeny, reason: "hack/ci/show-failures.buzz"},
		{rule: "ci-log-read", name: "raw job logs", input: bash("gh run view 34069443069 --log-failed"), decision: decisionAdvise, reason: "hack/ci/show-failures.buzz"},
		{rule: "ci-log-read", name: "job logs API", input: bash("gh api repos/egladman/magus/actions/jobs/123/logs"), decision: decisionAdvise, reason: "without target artifacts"},
		{rule: "ci-watch", name: "repeated job logs API", input: bash("while true; do gh api repos/egladman/magus/actions/jobs/123/logs; sleep 30; done"), decision: decisionDeny, reason: "GREEN CHANGES NOTHING"},
		{rule: "queue-poll", name: "the queue's runs", input: bash("gh run list --workflow queue.yaml --limit 5"), decision: decisionAdvise, reason: "magus queue ls"},
		{rule: "admin-merge", name: "gh pr merge --admin", input: bash("gh pr merge 12 --squash --admin"), decision: decisionDeny, reason: "--auto --squash"},
		{rule: "auto-merge", name: "gh pr merge --auto", input: bash("gh pr merge 12 --squash --auto"), decision: decisionDeny, reason: "a person enables auto-merge"},
		{rule: "auto-merge", name: "the enablePullRequestAutoMerge mutation through gh api", input: bash(`gh api graphql -f query='mutation { enablePullRequestAutoMerge(input: {pullRequestId: "PR_x"}) { clientMutationId } }'`),
			decision: decisionDeny, reason: "a person enables auto-merge"},
		{rule: "auto-merge", name: "gh pr merge --disable-auto", input: bash("gh pr merge 12 --disable-auto"), decision: decisionPass},
		{rule: "pr-watch", name: "a for loop that sleeps over a pull request's state", input: bash("for i in 1 2 3; do gh pr view 300 --json state,autoMergeRequest; sleep 60; done"), decision: decisionDeny, reason: "CI monitor"},
		{rule: "pr-watch", name: "a while loop over the pulls endpoint", input: bash(`while true; do gh api repos/egladman/magus/pulls/300 --jq .merged; sleep 60; done`), decision: decisionDeny, reason: "CI monitor"},
		{rule: "pr-watch", name: "one read of a pull request's state", input: bash("gh pr view 300 --json state,autoMergeRequest"), decision: decisionPass},
		{rule: "worker-runs-gate", name: "a review worker runs the gate", input: bash("./magus affected ci --no-default-charms"), worker: "root/review footprint", decision: decisionDeny, reason: "integrate"},
		{rule: "worker-runs-gate", name: "the checkout's integrator runs the gate", input: bash("./magus affected ci --no-default-charms"), worker: "root/integrate footprint",
			jobs: []types.Job{{ID: "footprint", State: types.StateRunning, WritePaths: []string{"docs/**"}, Check: &types.LeaseCheck{Target: "go-test", Project: "."}}}, lease: "footprint", decision: decisionPass},
		{rule: "worker-runs-gate", name: "an integrate title with no lease taken here", input: bash("./magus affected ci --no-default-charms"), worker: "root/integrate footprint", decision: decisionDeny, reason: "integrate"},
		{rule: "worker-builds-magus", name: "the checkout's integrator builds magus", input: bash("./magus run go-build ."), worker: "root/integrate footprint",
			jobs: []types.Job{{ID: "footprint", State: types.StateRunning, WritePaths: []string{"docs/**"}, Check: &types.LeaseCheck{Target: "go-test", Project: "."}}}, lease: "footprint", decision: decisionPass},
		{rule: "detached-push-unqualified", name: "a detached push to a new branch", input: bash("git push origin HEAD:guard-pr-polling"),
			checkout: &types.CheckoutState{RemoteBranches: []string{"origin/main"}}, decision: decisionDeny, reason: "HEAD:refs/heads/guard-pr-polling"},
		{rule: "push-authority", name: "a subagent opens a pull request", input: bash(`gh pr create --title "fix: pin the key" --body "Pins it."`),
			worker: "root/feat footprint", decision: decisionDeny, reason: "only the main session pushes or opens a pull request"},
		{rule: "branch-name", name: "the main session pushes an uppercase, underscored name", input: bash("git push origin HEAD:Pin_Key"),
			checkout: &types.CheckoutState{Branch: "pin-key", Base: "origin/main", RemoteBranches: []string{"origin/main"}}, decision: decisionDeny, reason: "`Pin_Key` is not a branch name to publish"},
		{rule: "spawn-without-job-row", name: "a spawn titled for no job", input: agentSpawn(map[string]any{
			"description": "audit the store", "prompt": "Audit it.", "model": "sonnet",
		}), decision: decisionDeny, reason: "magus job fork <job> --model sonnet"},
		{rule: "spawn-without-job-row", name: "a spawn naming a row the store lacks", input: agentSpawn(map[string]any{
			"description": "root/review footprint", "prompt": "Review it.", "model": "sonnet",
		}), decision: decisionDeny, reason: "magus job fork footprint --parent root --model sonnet --read-only"},
		{rule: "spawn-without-job-row", name: "a spawn naming an ended row", input: agentSpawn(map[string]any{
			"description": "root/review footprint", "prompt": "Review it.", "model": "sonnet",
		}), jobs: []types.Job{{ID: "footprint", State: types.StatePass}}, decision: decisionDeny, reason: "has ended (pass)"},
		{rule: "spawn-without-job-row", name: "a spawn naming a live nested row", input: agentSpawn(map[string]any{
			"description": "root/review footprint", "prompt": "Review it.", "model": "sonnet",
		}), jobs: []types.Job{{ID: "root/footprint", Parent: "root", State: types.StateDeclared, ReadOnly: true}}, decision: decisionPass},
		{rule: "spawn-without-job-row", name: "a spawn while the store cannot be read", input: agentSpawn(map[string]any{
			"description": "root/review footprint", "prompt": "Review it.", "model": "sonnet",
		}), corruptStore: true, decision: decisionAdvise, reason: "went unchecked"},
		{rule: "change-role-spawn-not-isolated", name: "a feat worker sharing the checkout", input: agentSpawn(map[string]any{
			"description": "root/feat footprint", "prompt": "Build it.", "model": "sonnet",
		}), jobs: []types.Job{{ID: "footprint", State: types.StateDeclared}}, decision: decisionDeny, reason: `isolation: "worktree"`},
		{rule: "change-role-spawn-not-isolated", name: "a feat worker in its own worktree", input: agentSpawn(map[string]any{
			"description": "root/feat footprint", "prompt": "Build it.", "model": "sonnet", "isolation": "worktree",
		}), jobs: []types.Job{{ID: "footprint", State: types.StateDeclared}}, decision: decisionPass},
		{rule: "worker-builds-magus", name: "a worker rebuilds the binary", input: bash("./magus run go-build ."),
			lease: "footprint", jobs: []types.Job{{ID: "footprint", State: types.StateRunning}}, decision: decisionDeny, reason: "a worker does not build magus"},
		{rule: "worker-builds-magus", name: "a worker bootstraps the binary", input: bash("GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache ."),
			lease: "footprint", jobs: []types.Job{{ID: "footprint", State: types.StateRunning}}, decision: decisionDeny, reason: "ask the main session to place ./magus"},
		{rule: "worker-builds-magus", name: "the orchestrator rebuilds the binary", input: bash("./magus run go-build ."), decision: decisionPass},
		{rule: "brief-builds-magus", name: "a brief tells its worker to build", input: agentSpawn(map[string]any{
			"description": "root/feat footprint", "prompt": "Implement it, then run `./magus run go-build .` first.", "model": "sonnet", "isolation": "worktree",
		}), jobs: []types.Job{{ID: "footprint", State: types.StateDeclared}}, decision: decisionDeny, reason: "tells the worker to build magus"},
		{rule: "brief-builds-magus", name: "a brief that names the build only to forbid it", input: agentSpawn(map[string]any{
			"description": "root/feat footprint", "prompt": "Never run `./magus run go-build .`; use the binary already placed.", "model": "sonnet", "isolation": "worktree",
		}), jobs: []types.Job{{ID: "footprint", State: types.StateDeclared}}, decision: decisionPass},
		{rule: "worker-bootstrap", name: "a subagent runs the bootstrap", input: bash("go run -trimpath ./cmd/magus run go-build --no-cache ."),
			worker: "root/feat footprint", decision: decisionDeny, reason: "a worker does not build magus"},
		{rule: "host-capture", name: "a cat of a run log", input: bash("cat .magus/logs/0123abcd.log"), binary: true, decision: decisionDeny, reason: "magus query output"},
		{rule: "host-capture", name: "a cat of a run log where the workspace has no ./magus", input: bash("cat .magus/logs/0123abcd.log"), decision: decisionPass},
		{rule: "host-terminals", name: "a mkdir of a terminals directory", input: bash("mkdir -p terminals"), decision: decisionDeny, reason: "a directory named terminals is a host session folder, not a run"},
		{rule: "sealed-read", name: "a cat of a sealed proofread set", input: bash("cat ~/.cache/proofread/calibration/sealed/round5/candidates.jsonl"),
			decision: decisionDeny, reason: "stays sealed until its round is scored"},
		{rule: "sealed-read", name: "a read of the open labels beside it", input: bash("cat ~/.cache/proofread/calibration/labels/round4/counts.json"), decision: decisionPass},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.rule] = true
		t.Run(fmt.Sprintf("%s/%s", tc.rule, tc.name), func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			if tc.binary {
				placeBinary(t, hookLocation(ctx, Dependencies{}).workspace)
			}
			store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: hookLocation(ctx, Dependencies{}).workspace})
			for _, row := range tc.jobs {
				if row.State == types.StateRunning && row.CheckoutRoot == "" {
					row.CheckoutRoot = hookLocation(ctx, Dependencies{}).workspace
				}
				row.Validated = 1
				_, err := store.Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
				require.NoError(t, err)
			}
			if tc.corruptStore {
				p, err := store.Path()
				require.NoError(t, err)
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
				require.NoError(t, os.WriteFile(p, []byte("{not a job store"), 0o644))
			}
			input := map[string]any{"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse"}
			for k, v := range tc.input {
				input[k] = v
			}
			if tc.worker != "" {
				input["agent_id"] = "a1"
				writeSpawnedAgent(hint.NewGate(cacheDir, FactsKey("claude-code", "8f2c6a1e")), "a1", spawnedAgent{Description: tc.worker})
			}
			// busy-wait off, so the policy's own loop rules (ci-watch, pr-watch) decide and say why.
			deps := Dependencies{
				CommandRule: m.CommandRule(), SpawnRule: m.SpawnRule(), WriteRule: m.WriteRule(),
				Builtins: map[string]builtin.Setting{string(denyRuleBusyWait): {Decision: builtin.Off}},
			}
			if tc.checkout != nil {
				deps.CheckoutState = func(context.Context, string) *types.CheckoutState { return tc.checkout }
			}
			v := Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, input), Lease: tc.lease})
			assert.Equal(t, tc.decision, v.Decision, v.Reason)
			if tc.decision != decisionPass {
				assert.Contains(t, []string{workspaceCommandRule, workspaceSpawnRule}, v.Rule, "the policy decided, not a built-in")
			}
			if tc.reason != "" {
				assert.Contains(t, v.Reason+v.Context, tc.reason)
			}
		})
	}

	// shellAt judges shell lines in a checkout on branch, whose base ref is origin/main, and
	// returns the cache dir they are judged against. The workspace is an empty temporary
	// dir unless one is named.
	shellAt := func(t *testing.T, branch, workspace string) (func(command string) types.GuardJudgment, string) {
		t.Helper()
		ctx, cacheDir := spawnFixture(t)
		if workspace != "" {
			ctx = WithLocation(ctx, cacheDir, workspace, workspace)
		}
		deps := Dependencies{
			CommandRule: m.CommandRule(),
			CheckoutState: func(context.Context, string) *types.CheckoutState {
				return &types.CheckoutState{Branch: branch, Base: "origin/main"}
			},
		}
		return func(command string) types.GuardJudgment {
			return Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
				"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Bash",
				"tool_input": map[string]any{"command": command},
			})})
		}, cacheDir
	}
	shellIn := func(t *testing.T, branch, workspace string) func(command string) types.GuardJudgment {
		t.Helper()
		run, _ := shellAt(t, branch, workspace)
		return run
	}

	t.Run("host-capture/a task capture outside the workspace names its own ref", func(t *testing.T) {
		wsRoot := testkit.NewWorkspace(t)
		wsRoot.WriteExecutable("magus", "#!/bin/sh\n")
		ws := wsRoot.Root()
		hostTemp := t.TempDir()
		tasks := filepath.Join(hostTemp, "claude-501", "project", "session", "tasks")
		require.NoError(t, os.MkdirAll(tasks, 0o755))
		capture := filepath.Join(tasks, "bi3m8xyic.output")
		require.NoError(t, os.WriteFile(capture, []byte("[fail] magus run (ran, 3s)\n  output: out1afa17d04e90\n"), 0o644))

		v := shellIn(t, "trim-key", ws)("cat " + capture)
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceCommandRule), unworded(v), "the policy decided, not a built-in: %s", v.Reason)
		assert.Contains(t, v.Reason, "`magus query output out1afa17d04e90`")

		// A backgrounded command that is not a magus run leaves a capture with no ref, and
		// reading it is the only way to see its output.
		plain := filepath.Join(tasks, "b7plain00.output")
		require.NoError(t, os.WriteFile(plain, []byte("go version go1.26 darwin/arm64\n"), 0o644))
		v = shellIn(t, "plain-capture", ws)("cat " + plain)
		assert.NotEqual(t, decisionDeny, v.Decision, v.Reason)
	})

	covered["commit-subject"] = true
	t.Run("commit-subject", func(t *testing.T) {
		run := shellIn(t, "trim-key", "")
		for _, line := range []string{
			`git -c user.name=x commit -q -m "URL-parse the port; keep the key"`,
			`hg -R . --config ui.username=x commit -m "trim a fragment" -y`,
			`sl --cwd . ci -m "trim a fragment"`,
			`jj --no-pager commit -m "trim a fragment"`,
			`jj describe -r @- --message="trim a fragment"`,
			`rg "git commit -m" docs`,
		} {
			v := run(line)
			assert.NotEqual(t, decisionDeny, v.Decision, fmt.Sprintf("%s: %s", line, v.Reason))
		}
		for _, line := range []string{
			`git -C . commit -m "Fix the port."`,
			`hg -q commit -m "Fix the port."`,
			`sl commit -m "Fix the port."`,
			`jj --color=never commit -m "Fix the port."`,
			`jj -R . describe -m "Fix the port."`,
		} {
			v := run(line)
			assert.Equal(t, verdictWithRule(decisionDeny, workspaceCommandRule), unworded(v), line)
			assert.Contains(t, v.Reason, "a capitalized first word", line)
			assert.Contains(t, v.Reason, "Write one subject line, lowercase and imperative", line)
			assert.NotContains(t, v.Reason, "Skill(", line)
		}

		v := run("git add a.go && git commit -m \"$(cat <<'EOF'\nFix the port.\n\nCo-Authored-By: a <b@c>\nEOF\n)\"")
		assert.Equal(t, decisionDeny, v.Decision, v.Reason)
		for _, want := range []string{"a capitalized first word", "an attribution", "a line break"} {
			assert.Contains(t, v.Reason, want)
		}
		assert.Contains(t, run(`git commit -m "fix(cache): keep the key"`).Reason, "a colon", "off the base branch a prefix is denied")
	})

	t.Run("commit-subject on the base branch", func(t *testing.T) {
		run := shellIn(t, "main", "")
		for _, line := range []string{
			`git commit -m "fix(cache): keep the key stable across runs"`,
			`jj describe -m "feat: add a commit-msg hook"`,
		} {
			v := run(line)
			assert.NotEqual(t, decisionDeny, v.Decision, fmt.Sprintf("%s: %s", line, v.Reason))
		}
		v := run(`git commit -m "trim a fragment"`)
		assert.Equal(t, decisionDeny, v.Decision, v.Reason)
		assert.Contains(t, v.Reason, "A commit made on main lands as written")
		assert.Contains(t, v.Reason, `"trim a fragment": no `+"`<type>: `"+` prefix`)
	})

	covered["pull-request-text"] = true
	t.Run("pull-request-text", func(t *testing.T) {
		// The rule runs proofread, which `magus run proofread-build libs/proofread` links into
		// the workspace's libs/proofread/gen, so this one holds that module, built output
		// and all; the root test target needs proofread-build. It is not the checkout itself, whose
		// branch diff would add advice about the areas it touches to every verdict.
		proofreadMod := filepath.Join(root, "libs", "proofread")
		linkJudge := func(ws string) {
			require.NoError(t, os.Mkdir(filepath.Join(ws, "libs"), 0o755))
			require.NoError(t, os.Symlink(proofreadMod, filepath.Join(ws, "libs", "proofread")))
		}

		// The rule reads the branch's diff and denies one it cannot read, so the workspace is
		// a repository whose branch adds a shipped file and the changelog fragment its fix
		// title owes. The judge's link is untracked and stays out of the diff.
		branchRepo := func() (string, string) {
			repo := testkit.NewRepo(t, map[string]string{"README.md": "# key\n"})
			repo.Git("update-ref", "refs/remotes/origin/main", "HEAD")
			repo.Git("switch", "-q", "-c", "trim-key")
			head := repo.Commit("sort the key", map[string]string{
				"internal/cache/key.go":          "package cache\n",
				"changes/unreleased/trim-key.md": "### Fixed\n\n- **Warm builds hit the cache again.** The key sorts its inputs before hashing.\n",
			})
			return repo.Root(), head
		}
		ws, head := branchRepo()
		linkJudge(ws)
		// gh edit reads the pull request's head and base from gh, which a test has no login for.
		bin := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(bin, "gh"),
			[]byte(fmt.Sprintf(`#!/bin/sh
printf '{"headRefOid":"%%s","baseRefName":"main"}' %s
`, head)), 0o755))
		t.Setenv("PATH", fmt.Sprintf("%s%s%s", bin, string(os.PathListSeparator), os.Getenv("PATH")))
		run, cacheDir := shellAt(t, "trim-key", ws)
		const describe = `gh pr create --title "fix(cache): pin the key" --body "Warm builds miss the cache because the key hashes its inputs in map order; sorting them pins one key per build."`
		v := run(describe)
		assert.Contains(t, []string{decisionPass, decisionAdvise}, v.Decision, fmt.Sprintf("judged, not failed open: %s%s", v.Reason, v.Context))

		// A workspace that is no repository has no diff to read: the description is denied
		// with the fetch that would make it readable, never judged on facts nobody read.
		bare := t.TempDir()
		linkJudge(bare)
		unread, _ := shellAt(t, "trim-key", bare)
		v = unread(describe)
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceShellPrefix+"pull-request-text"), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "the diff of this pull request cannot be read")
		assert.Contains(t, v.Reason, "run `git fetch origin main`, then retry")

		// A cited ref is looked up in the store of the checkout being judged.
		store := cache.NewOutputStore(cacheDir)
		passed, err := store.Persist(t.Context(), strings.Repeat("a1", 32), []byte("ok\n"),
			cache.OutputDescriptor{Project: ".", Target: "go-test"})
		require.NoError(t, err)
		failed, err := store.Persist(t.Context(), strings.Repeat("b2", 32), []byte("FAIL\n"),
			cache.OutputDescriptor{Project: "libs/conventions", Target: "go-test", Failed: true})
		require.NoError(t, err)
		citing := func(ref string) string {
			return fmt.Sprintf("gh pr edit 9 --title \"fix(cache): pin the key\" --body \"Warm builds hit the cache again because the key now sorts its inputs; TestKey passed (%s).\"", ref)
		}
		v = run(citing(passed.Ref))
		assert.Equal(t, decisionPass, v.Decision, fmt.Sprintf("a passing run this checkout holds: %s%s", v.Reason, v.Context))
		v = run(citing(failed.Ref))
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceShellPrefix+"pull-request-text"), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, fmt.Sprintf("`%s` cites a failed run (libs/conventions:go-test)", failed.Ref))
		v = run(citing("outc3c3c3c3c3c3"))
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceShellPrefix+"pull-request-text"), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "`outc3c3c3c3c3c3` cites a run this checkout holds no record of")

		// attribution ships off; this repository's decisions table turns it on.
		v = run("gh pr create --title \"Pin the key\" --body \"$(cat <<'EOF'\nClaude pinned the key.\nEOF\n)\"")
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceShellPrefix+"pull-request-text"), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, `pr-title: "Pin the key": no `+"`<type>: `"+` prefix`)
		assert.Contains(t, v.Reason, "Drop 'Claude': describe the change, not who or what produced it. [attribution]")
		assert.NotContains(t, v.Reason, "Skill(")

		v = run(`gh pr edit 412 --title "fix(cache): pin the key" --body "Workers miss the skill rules because the guard reads a stale copy; it reads .claude/skills/x/SKILL.md instead."`)
		assert.Equal(t, decisionPass, v.Decision, fmt.Sprintf("a path spelling a tool's name credits no one: %s%s", v.Reason, v.Context))

		v = run(`gh pr comment 412 --body "Good catch: the map is written from two goroutines, so it takes a lock now."`)
		assert.NotEqual(t, decisionDeny, v.Decision, fmt.Sprintf("a reply is judged as a reply: %s%s", v.Reason, v.Context))
		v = run(`gh pr comment 412 --body "Let me delve into why the map is shared."`)
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceShellPrefix+"pull-request-text"), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "[buzzword]")

		// The same sources with no gen/ dir: a judge never built denies, and never compiles.
		unbuilt, _ := branchRepo()
		mod := filepath.Join(unbuilt, "libs", "proofread")
		require.NoError(t, os.MkdirAll(mod, 0o755))
		entries, err := os.ReadDir(proofreadMod)
		require.NoError(t, err)
		for _, e := range entries {
			if e.Name() != "gen" {
				require.NoError(t, os.Symlink(filepath.Join(proofreadMod, e.Name()), filepath.Join(mod, e.Name())))
			}
		}
		v = shellIn(t, "trim-key", unbuilt)(describe)
		assert.Equal(t, decisionDeny, v.Decision, v.Reason)
		assert.Contains(t, v.Reason, "libs/proofread/gen/proofread is not built; run `magus run proofread-build libs/proofread`")

		// The built judge beside a source it was not linked from is as stale as none.
		require.NoError(t, os.Symlink(filepath.Join(proofreadMod, "gen"), filepath.Join(mod, "gen")))
		require.NoError(t, os.WriteFile(filepath.Join(mod, "zz.go"), []byte("package proofread\n"), 0o644))
		v = shellIn(t, "trim-key", unbuilt)(describe)
		assert.Equal(t, decisionDeny, v.Decision, v.Reason)
		assert.Contains(t, v.Reason, "was linked from other sources than the tree holds; run `magus run proofread-build libs/proofread`")
	})

	covered["code-comments"] = true
	t.Run("code-comments", func(t *testing.T) {
		ctx, ws, _ := writeFixture(t)
		write := func(name, content string) types.GuardJudgment {
			return Judge(ctx, Dependencies{WriteRule: m.WriteRule()}, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
				"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Write",
				"tool_input": map[string]any{"file_path": filepath.Join(ws, name), "content": content},
			})})
		}
		const rule = "keep only the comments that say what the code cannot"
		assert.NotContains(t, write("notes.md", "# A heading\n").Context, rule, "prose is not code")
		v := write("a.go", "package a\n\n// keyOf is stable across runs.\nfunc keyOf() {}\n")
		assert.Equal(t, decisionAdvise, v.Decision, v.Reason)
		assert.Contains(t, v.Reason+v.Context, rule)
		assert.NotContains(t, v.Reason+v.Context, "Skill(")
		assert.NotContains(t, write("b.go", "package a\n\n// again\n").Context, rule, "once per session")
	})

	covered["host-cursor-rules"] = true
	t.Run("host-cursor-rules", func(t *testing.T) {
		ctx, ws, _ := writeFixture(t)
		write := func(name string) types.GuardJudgment {
			return Judge(ctx, Dependencies{WriteRule: m.WriteRule()}, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
				"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Write",
				"tool_input": map[string]any{"file_path": filepath.Join(ws, name), "content": "{}\n"},
			})})
		}
		v := write(".cursor/rules/x.mdc")
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceWriteRule), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "docs/guides/integrations/agents/cursor.md")
		assert.NotEqual(t, decisionDeny, write(".cursor/hooks.json").Decision)
	})

	// The binary rules read the trees a line names, so each case builds its own.
	judgeIn := func(t *testing.T, ws, command string) types.GuardJudgment {
		t.Helper()
		testkit.Isolate(t)
		ctx := WithLocation(t.Context(), t.TempDir(), ws, ws)
		deps := Dependencies{CommandRule: m.CommandRule(), SpawnRule: m.SpawnRule(), WriteRule: m.WriteRule()}
		return Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
			"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Bash",
			"tool_input": map[string]any{"command": command},
		})})
	}
	checkout := func(t *testing.T, withBinary bool) string {
		t.Helper()
		w := testkit.NewWorkspace(t)
		w.Write("go.mod", "module github.com/egladman/magus\n\ngo 1.25\n")
		if withBinary {
			w.WriteExecutable("magus", "binary")
		}
		return w.Root()
	}

	covered["foreign-binary"] = true
	t.Run("foreign-binary", func(t *testing.T) {
		built, here := checkout(t, true), checkout(t, false)
		v := judgeIn(t, here, filepath.Join(built, "magus")+" run lint .")
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceCommandRule), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "another checkout of magus")
	})

	covered["go-into-checkout"] = true
	t.Run("go-into-checkout", func(t *testing.T) {
		other := checkout(t, true)
		v := judgeIn(t, checkout(t, false), fmt.Sprintf("go -C %s test ./...", other))
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceCommandRule), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "runs a toolchain command in another checkout")

		bare := checkout(t, false)
		assert.Equal(t, decisionPass, judgeIn(t, checkout(t, false), fmt.Sprintf("GOEXPERIMENT=jsonv2 go -C %s run -trimpath ./cmd/magus run go-build --no-cache .", bare)).Decision,
			"the bootstrap into a checkout with no binary passes")
		assert.Equal(t, decisionDeny, judgeIn(t, checkout(t, false), fmt.Sprintf("go -C %s build -o magus ./cmd/magus", bare)).Decision,
			"a bare link is not the bootstrap")
	})

	// The binary answering this hook is the test binary, so a workspace whose ./magus is it
	// stands in for a checkout's own build.
	covered["stale-binary"] = true
	t.Run("stale-binary", func(t *testing.T) {
		exe, err := os.Executable()
		require.NoError(t, err)
		ws := checkout(t, false)
		require.NoError(t, os.Symlink(exe, filepath.Join(ws, "magus")))
		v := judgeIn(t, ws, "ls")
		assert.Equal(t, verdictWithRule(decisionAdvise, workspaceCommandRule), unworded(v), v.Reason)
		assert.Contains(t, v.Reason+v.Context, "./magus run go-build .")
	})

	// The test binary carries no go-build stamp, so the stale-binary rule judges it stale,
	// and a state-writing verb it would run is denied where a read-only one is advised.
	covered["stale-write"] = true
	t.Run("stale-write", func(t *testing.T) {
		exe, err := os.Executable()
		require.NoError(t, err)
		ws := checkout(t, false)
		require.NoError(t, os.Symlink(exe, filepath.Join(ws, "magus")))
		v := judgeIn(t, ws, "./magus init --vcs git")
		assert.Equal(t, verdictWithRule(decisionDeny, workspaceCommandRule), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "no go-build stamp")
		assert.Equal(t, decisionAdvise, judgeIn(t, ws, "./magus init --dry-run").Decision, "a dry run writes nothing")
	})

	source, err := os.ReadFile(filepath.Join(root, "hack", "policy", "guard.buzz"))
	require.NoError(t, err)
	listed := policyRule.FindAllStringSubmatch(string(source), -1)
	require.NotEmpty(t, listed, "the header still lists its rules as `//   name (`")
	for _, rule := range listed {
		assert.True(t, covered[rule[1]], "%s is in the policy's header with no real-input case here", rule[1])
	}
}

// TestWriteOutsideTheWorkspaceIsAdvisedNothing: a scratch file or a user-level config is
// not this workspace's to advise on, and the audit caught advisories on exactly those.
// The same write inside the root still is.
func TestWriteOutsideTheWorkspaceIsAdvisedNothing(t *testing.T) {
	testkit.Isolate(t)
	root, elsewhere := t.TempDir(), t.TempDir()
	ctx := WithLocation(t.Context(), t.TempDir(), root, root)
	write := func(path string) types.GuardJudgment {
		return Judge(ctx, Dependencies{}, Request{Input: path, IsPath: true, Session: "s1", Host: "test-host"})
	}

	assert.Equal(t, decisionPass, write(elsewhere+"/CLAUDE.md").Decision)
	inside := write(root + "/CLAUDE.md")
	assert.Equal(t, verdictWithRule(decisionAdvise, string(advisoryInstruction)), unworded(inside))
}

// driftWorkspace is focusFixture loaded as the hook's workspace. Only the reader half and
// ClassifyFiles are reached on the write path.
type driftWorkspace struct {
	focusFixture
	types.TargetExpander
	types.AffectedComputer
	types.Inspector
}

func (driftWorkspace) ClassifyFiles(context.Context, []string) ([]types.FileEntry, error) {
	return nil, nil
}

// verdictRefLine opens the line a stored deny cites its grd ref on.
const verdictRefLine = "\nfull verdict: "

// verdictRef matches the grd ref a deny cites.
var verdictRef = regexp.MustCompile(verdictRefPrefix + `[0-9a-f]+`)

// storedVerdict is the full verdict a deny's reason cites, read from cacheDir: where the
// rationale and the rule's page live.
func storedVerdict(t *testing.T, cacheDir, reason string) string {
	t.Helper()
	ref := verdictRef.FindString(reason)
	require.NotEmpty(t, ref, "the deny cites its stored verdict: %q", reason)
	stored, err := trail.ReadBlob(cacheDir, ref)
	require.NoError(t, err)
	return string(stored)
}

// adviceRefLine opens the line a stored advisory cites its grd ref on.
const adviceRefLine = "\nfull advice: "

// withoutVerdictRef is v as a dry run renders it: the call stores its verdict or advice
// and cites the ref, and a dry run stores nothing, so it has no ref to cite.
func withoutVerdictRef(v types.GuardJudgment) types.GuardJudgment {
	v.Reason = withoutRefLine(v.Reason, verdictRefLine)
	v.Context = withoutRefLine(v.Context, adviceRefLine)
	return v
}

// withoutRefLine drops the line opening with line from text.
func withoutRefLine(text, line string) string {
	head, rest, ok := strings.Cut(text, line)
	if !ok {
		return text
	}
	if _, tail, more := strings.Cut(rest, "\n"); more {
		head += "\n" + tail
	}
	return head
}

// treeState is every path under dirs with its bytes and modification time.
func treeState(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	state := map[string]string{}
	for _, dir := range dirs {
		require.NoError(t, filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			state[path] = info.ModTime().String()
			if !d.IsDir() {
				body, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				state[path] += " " + string(body)
			}
			return nil
		}))
	}
	return state
}

// A dry run reaches the verdict the call would and leaves the cache dir and the state dir
// as it found them, where the call itself writes to one of them. The second round reads
// what the first call wrote: a held advisory stays held, a recorded drift stays quiet.
// Only a repeated deny is worded differently.
func TestDryRunJudgesAsTheCallAndWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) (context.Context, Dependencies, Request)
		want  types.GuardJudgment
	}{
		{
			name: "a write drifting into a second project",
			setup: func(t *testing.T) (context.Context, Dependencies, Request) {
				testkit.Isolate(t)
				cacheDir := t.TempDir()
				who := hookAttribution{Session: "s1"}
				scopeDrift{markers: hint.NewGate(cacheDir, who.factsKey()), project: "libs/ui"}.record()
				deps := Dependencies{
					Inspect: func(context.Context, string) (types.WorkspaceRepository, error) {
						return driftWorkspace{focusFixture: newFocusFixture()}, nil
					},
					Policy: func() PolicyState { return PolicyState{Digest: "d1", ShellRules: 1} },
				}
				return WithLocation(t.Context(), cacheDir, "/ws", "/ws"), deps, Request{Input: "/ws/app/main.go", IsPath: true, Session: who.Session}
			},
			want: types.GuardJudgment{Decision: decisionAdvise, Rule: string(advisoryScopeDrift)},
		},
		{
			name: "a once-per-session notice",
			setup: func(t *testing.T) (context.Context, Dependencies, Request) {
				ctx, _ := fleetFixture(t, types.Job{ID: "finished", State: types.StatePass})
				return ctx, Dependencies{}, Request{Input: "ls", Lease: "finished", Session: "s1"}
			},
			want: types.GuardJudgment{Decision: decisionAdvise, Rule: string(advisoryLeaseTerminal), Lease: "finished", LeaseFrom: types.LeaseSourceFlag},
		},
		{
			name: "a denied command",
			setup: func(t *testing.T) (context.Context, Dependencies, Request) {
				testkit.Isolate(t)
				return WithLocation(t.Context(), t.TempDir(), "/ws", "/ws"), strict(Dependencies{}), Request{Input: "magus run ci | tail", Session: "s1"}
			},
			want: types.GuardJudgment{Decision: decisionDeny},
		},
		{
			name: "an unregistered worker's first write",
			setup: func(t *testing.T) (context.Context, Dependencies, Request) {
				ctx, root := fleetFixture(t, types.Job{ID: "worker", State: types.StateRunning, WritePaths: []string{"app/**"}})
				bindCaller(t, ctx, hookAttribution{}, "worker")
				// An edit of a file already there, so no new-directory or new-file rule speaks.
				require.NoError(t, os.MkdirAll(filepath.Join(root, "app"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(root, "app", "main.go"), []byte("package main\n"), 0o644))
				commitCheckout(t, root)
				return ctx, Dependencies{}, Request{Input: filepath.Join(root, "app", "main.go"), IsPath: true}
			},
			want: types.GuardJudgment{Decision: decisionPass, Lease: "worker", LeaseFrom: types.LeaseSourceMarker},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, deps, req := tc.setup(t)
			// Its own, so another process's copy in the shared one is not read as this one's.
			t.Setenv("TMPDIR", t.TempDir())
			dirs := []string{hookLocation(ctx, deps).cacheDir, os.Getenv("XDG_STATE_HOME")}
			checked := req
			checked.DryRun = true
			for round := range 2 {
				before := treeState(t, dirs...)
				preview := Judge(ctx, deps, checked)
				assert.Equal(t, before, treeState(t, dirs...), "round %d: the dry run wrote", round)
				leftover, err := filepath.Glob(filepath.Join(os.TempDir(), "magus-guard-dry-run-*"))
				require.NoError(t, err)
				assert.Empty(t, leftover, "round %d: the dry run left its copy behind", round)

				real := Judge(ctx, deps, req)
				if round == 0 {
					assert.NotContains(t, preview.Reason, verdictRefLine, "a dry run stores no verdict, so it cites none")
					assert.NotContains(t, preview.Context, adviceRefLine, "nor any advice")
					assert.Equal(t, withoutVerdictRef(real), preview)
					assert.NotEqual(t, before, treeState(t, dirs...), "the call itself records something")
					got := types.GuardJudgment{Decision: real.Decision, Rule: real.Rule, Lease: real.Lease, LeaseFrom: real.LeaseFrom}
					if tc.want.Rule == "" {
						got.Rule = ""
					}
					assert.Equal(t, tc.want, got)
					continue
				}
				if real.Decision == decisionDeny {
					real.Reason, preview.Reason = "", ""
				}
				assert.Equal(t, withoutVerdictRef(real), preview, "round %d", round)
			}
		})
	}
}

// A dry run past the hook's deadline still reads its copy, so only release removes it.
func TestSessionCopyHoldsItsGatesUntilRelease(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cacheDir := t.TempDir()
	const kind = hint.MarkerKind("told")
	require.False(t, hint.NewGate(cacheDir, "s1").MarkFired(kind))

	dir, release := sessionCopy(cacheDir, "s1")
	require.DirExists(t, dir)
	assert.True(t, hint.NewGate(dir, "s1").AlreadyFired(kind), "the copy carries the session's marker")
	release()
	assert.NoDirExists(t, dir)
	assert.True(t, hint.NewGate(cacheDir, "s1").AlreadyFired(kind), "the session's own marker stays")
}

// A hook process exits without waiting for a dry run that ran out of time, and that run's
// gates may write its copy until then; the next copy removes it, and keeps a live process's.
func TestSessionCopyRemovesTheCopyOfAnExitedProcess(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	const exited = 1 << 30
	orphan := filepath.Join(tmp, fmt.Sprintf("%s%d-1", dryRunCopyPrefix, exited))
	require.False(t, hint.NewGate(orphan, "s1").MarkFired("told"), "a late write recreates the copy")
	require.DirExists(t, orphan)
	live := filepath.Join(tmp, fmt.Sprintf("%s%d-1", dryRunCopyPrefix, os.Getpid()))
	unnamed := filepath.Join(tmp, dryRunCopyPrefix+"2837461923")
	for _, dir := range []string{live, unnamed} {
		require.NoError(t, os.Mkdir(dir, 0o755))
	}

	dir, release := sessionCopy(t.TempDir(), "s1")
	assert.NoDirExists(t, orphan)
	assert.DirExists(t, live)
	assert.DirExists(t, unnamed, "a copy that names no process is never guessed at")
	release()
	assert.NoDirExists(t, dir)
}

// Judging a spawn records it, so a dry run refuses one rather than record it.
func TestDryRunRefusesASpawn(t *testing.T) {
	testkit.Isolate(t)
	cacheDir := t.TempDir()
	ctx := WithLocation(t.Context(), cacheDir, "/ws", "/ws")
	v := Judge(ctx, Dependencies{}, Request{Input: claudeSpawnEnvelope, Host: "claude-code", DryRun: true})
	assert.Equal(t, decisionDeny, v.Decision)
	entries, err := os.ReadDir(cacheDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// TestJudgeServesADenyRemedyItThenPreauthorizes is the plan's proof run end to end: the
// redirect is refused with its --tee line as next, and running that line passes,
// cleared by the remedy's own id.
func TestJudgeServesADenyRemedyItThenPreauthorizes(t *testing.T) {
	ctx, _ := fleetFixture(t)
	cacheDir := ctx.Value(locationKey{}).(location).cacheDir
	deps := strict(testDependencies())

	v := Judge(ctx, deps, Request{Input: "./magus ls jobs -o json > f"})
	want := types.NextStep{
		ID: "deny-output-redirect", Run: "magus ls jobs -o json --tee f",
		Argv: []string{"magus", "ls", "jobs", "-o", "json", "--tee", "f"},
		Why:  "--tee writes the structured record to the file and still prints it.",
	}
	ref := verdictRef.FindString(v.Reason)
	require.NotEmpty(t, ref, "the first firing cites its stored verdict")
	require.Equal(t, types.GuardJudgment{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      decisionDeny,
		Reason:        fmt.Sprintf("`ls jobs >f` keeps console text, which is not a format anything should parse.\nnext:\n  magus ls jobs -o json --tee f%smagus query output %s", verdictRefLine, ref),
		Rule:          string(denyRuleOutputRedirect),
		Lease:         v.Lease, // follows the environment the test ran in
		LeaseFrom:     v.LeaseFrom,
		Next:          []types.NextStep{want},
	}, v, "one line plus the next and the verdict ref")

	assert.Equal(t, "deny-output-redirect", servedNextPreauthorizes(hint.NewGate(cacheDir, ""), want.Run))
	assert.NotEqual(t, decisionDeny, Judge(ctx, deps, Request{Input: want.Run}).Decision, "the served line passes")
}

// TestJudgeDropsARemedyTheRoleMayNotRun pins the other half: a worker refused a piped gate
// is not handed the gate unpiped, because a served next stands the lease rules down.
func TestJudgeDropsARemedyTheRoleMayNotRun(t *testing.T) {
	worker := narrowLease()
	ctx, _ := fleetFixture(t, worker)
	deps := strict(testDependencies())
	const piped = "magus affected ci | tail -5"

	// The worker first: its firing is the one worded in full.
	leased := Judge(ctx, deps, Request{Input: piped, Lease: worker.ID})
	require.Equal(t, types.GuardJudgment{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      decisionDeny,
		Reason:        leased.Reason, // the prose is checked by substring below
		Rule:          string(denyRuleOutputPipe),
		Lease:         worker.ID,
		LeaseFrom:     leased.LeaseFrom, // which channel answered is not this test's concern
	}, leased, "the worker may not run the gate, so it is not served one")
	assert.Contains(t, leased.Reason, "`-s` stays quiet until something fails", "the prose names the lever instead")
	assert.NotContains(t, leased.Reason, "next:")

	unbound := Judge(ctx, deps, Request{Input: piped})
	require.Len(t, unbound.Next, 1)
	assert.Equal(t, "magus affected ci -s", unbound.Next[0].Run)
}

// TestEveryJudgedDecisionNamesItsRule reads Judge itself: a statement setting a verdict to
// deny, advise or ask must sit where a verdict.Rule assignment covers it, in its own block
// or one around it. Recorded verdicts once carried no rule name by the thousand, and
// the recent ones all came from a site that set the decision and not the name.
func TestEveryJudgedDecisionNamesItsRule(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "guard.go", nil, 0)
	require.NoError(t, err)
	var judge *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "Judge" {
			judge = fn
		}
	}
	require.NotNil(t, judge, "Judge moved out of guard.go and this gate stopped reading it")

	verdictField := func(e ast.Expr, field string) bool {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != field {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == "verdict"
	}
	assignsRule := func(s ast.Stmt) bool {
		as, ok := s.(*ast.AssignStmt)
		return ok && slices.ContainsFunc(as.Lhs, func(e ast.Expr) bool { return verdictField(e, "Rule") })
	}
	decides := func(s ast.Stmt) bool {
		as, ok := s.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return false
		}
		for i, e := range as.Lhs {
			id, ok := as.Rhs[i].(*ast.Ident)
			if verdictField(e, "Decision") && ok && strings.HasPrefix(id.Name, "decision") && id.Name != "decisionPass" {
				return true
			}
		}
		return false
	}
	checked := 0
	var walk func(list []ast.Stmt, named bool)
	walk = func(list []ast.Stmt, named bool) {
		named = named || slices.ContainsFunc(list, assignsRule)
		for _, s := range list {
			if decides(s) {
				checked++
				assert.True(t, named, "%s sets a decision no verdict.Rule assignment names", fset.Position(s.Pos()))
			}
			ast.Inspect(s, func(n ast.Node) bool {
				switch b := n.(type) {
				case *ast.BlockStmt:
					walk(b.List, named)
					return false
				case *ast.CaseClause:
					walk(b.Body, named)
					return false
				}
				return true
			})
		}
	}
	walk(judge.Body.List, false)
	assert.Greater(t, checked, 10, "found too few decisions; the walk stopped reaching Judge's arms")
}

// The notices about the acting lease itself honor every decision a workspace may set:
// advise says so once a session, off says nothing, and deny refuses every call rather
// than only the first.
func TestJudgeLeaseNoticesHonorEveryDecision(t *testing.T) {
	for _, notice := range []struct {
		rule  hint.MarkerKind
		lease string
	}{
		{advisoryLeaseTerminal, "finished"},
		{advisoryLeaseInvalid, "lease b!"},
	} {
		for _, tt := range []struct {
			decision     builtin.Decision
			first, again string
		}{
			{decision: builtin.Advise, first: decisionAdvise, again: decisionPass},
			{decision: builtin.Off, first: decisionPass, again: decisionPass},
			{decision: builtin.Deny, first: decisionDeny, again: decisionDeny},
		} {
			t.Run(fmt.Sprintf("%s/%s", string(notice.rule), tt.decision), func(t *testing.T) {
				ctx, _ := fleetFixture(t, types.Job{ID: "finished", State: types.StatePass})
				deps := withSetting(string(notice.rule), tt.decision)
				judge := func() types.GuardJudgment {
					v := Judge(ctx, deps, Request{Input: "ls", Lease: notice.lease, Session: "s1"})
					return types.GuardJudgment{Decision: v.Decision, Rule: v.Rule}
				}
				want := func(decision string) types.GuardJudgment {
					if decision == decisionPass {
						return types.GuardJudgment{Decision: decisionPass}
					}
					return types.GuardJudgment{Decision: decision, Rule: string(notice.rule)}
				}

				assert.Equal(t, want(tt.first), judge())
				assert.Equal(t, want(tt.again), judge())
			})
		}
	}
}

// The shell advisories that spoke with no name, each now naming a catalogued rule.
func TestShellAdvisoriesNameACataloguedRule(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]denyRuleName{
		"./magus run lint . && echo done": advisoryEchoOnSuccess,
		"time ./magus run test . -s":      advisoryTimedMagus,
		"npm update":                      advisoryDependencyUpdate,
		"pnpm install":                    advisoryDependencyInstall,
	} {
		v := Evaluate(advising(testDependencies()), command)
		require.NotEmpty(t, v.Context, command)
		assert.Equal(t, string(want), v.advisoryName(), command)
		_, ok := Rule(v.advisoryName())
		assert.True(t, ok, "%q names %q, which the catalog does not list", command, v.advisoryName())
	}
}

// BenchmarkWithJobStoreRows is one guard call's read of the job store: a store the size a
// working session leaves behind, in a real repository so the store's placement resolves as
// it does for a hook.
func BenchmarkWithJobStoreRows(b *testing.B) {
	testkit.Isolate(b)
	root, err := workspace.FindRoot("")
	require.NoError(b, err)
	cacheDir := b.TempDir()
	at := location{cacheDir: cacheDir, workspace: root, dir: root}
	store := job.NewStore(at.jobs())
	states := []types.JobState{types.StatePass, types.StatePass, types.StateFail, types.StateNoReturn, types.StateDeclared}
	for i := range 300 {
		row := types.Job{
			ID: fmt.Sprintf("root-%d/job-%d", i%20, i), Parent: fmt.Sprintf("root-%d", i%20),
			State: states[i%len(states)], WritePaths: []string{"internal/guard/guard.go"},
		}
		_, err := store.Update(b.Context(), row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(b, err)
	}
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		withJobStoreRows(ctx, at)
	}
}

// Every call reads the store as it is: a row written between two calls is in the second.
func TestJobStoreRowsAreReadFreshEachCall(t *testing.T) {
	testkit.Isolate(t)
	root, cacheDir := t.TempDir(), t.TempDir()
	at := location{cacheDir: cacheDir, workspace: root, dir: root}
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root})
	put := func(row types.Job) {
		t.Helper()
		_, err := store.Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	rowsOf := func() []string {
		t.Helper()
		snap, ok := job.SnapshotFromContext(withJobStoreRows(t.Context(), at))
		require.True(t, ok)
		require.NoError(t, snap.Err)
		var ids []string
		for _, r := range snap.Rows {
			ids = append(ids, r.ID)
		}
		return ids
	}

	put(types.Job{ID: "a", State: types.StateDeclared, WritePaths: []string{"x.go"}, Model: "sonnet"})
	assert.Equal(t, []string{"a"}, rowsOf())
	put(types.Job{ID: "b", State: types.StateDeclared})
	assert.Equal(t, []string{"a", "b"}, rowsOf())
}

// recoverableRules are the compiled rules a caller recovers from by running the command the
// refusal names. They deny by default (ADR 0013), and a workspace may set one to advise.
var recoverableRules = []string{
	"architecture-unbriefed", "brief-command", "brief-plain-commands", "buzz-unbriefed", "chained-run",
	"exit-status-echo", "filter-without-input", "grep-reader", "interpreter-rewrite", "interpreter-script", "magus-timeout",
	"monitor-duplicate", "monitor-pile", "output-pipe", "output-redirect", "raw-tool", "read-navigation",
	"read-paging", "search-translation", "sed-in-place", "sibling-checkout",
	"spawn-unbriefed", "spawn-waiter", "stage-all", "symbol-search", "throwaway-copy", "unjudged-tool",
	"unknown-env",
}

// commandAdvisories are the advisories whose text names a command, which deny by default.
var commandAdvisories = []string{
	"capture-filter", "check-timeout", "checkpoint-state", "dependency-install", "dependency-update",
	"gate-repeat", "generated-write", "graph-pipe", "installed-skill", "lease-invalid",
	"model-tier-context-cap", "spawn-nested-waiter",
}

// strict returns deps with every rule's setting spelled out, so a test pins the denial
// whatever deps carried before.
func strict(deps Dependencies) Dependencies {
	deps.Builtins = make(map[string]builtin.Setting)
	for name, d := range builtin.Defaults() {
		deps.Builtins[name] = builtin.Setting{Decision: d}
	}
	return deps
}

// advising returns deps with each named rule set to advise, the opt-out a workspace
// declares by name in magus\guard.builtins. With no names it lowers every rule ADR 0013
// made deny.
func advising(deps Dependencies, names ...string) Dependencies {
	if len(names) == 0 {
		names = slices.Concat(recoverableRules, commandAdvisories)
	}
	builtins := maps.Clone(deps.Builtins)
	if builtins == nil {
		builtins = make(map[string]builtin.Setting, len(names))
	}
	for _, name := range names {
		builtins[name] = builtin.Setting{Decision: builtin.Advise}
	}
	deps.Builtins = builtins
	return deps
}

func TestRecoverableRulesDenyUnlessSetToAdvise(t *testing.T) {
	t.Parallel()
	defaults := builtin.Defaults()

	for _, name := range slices.Concat(recoverableRules, commandAdvisories) {
		require.Contains(t, defaults, name, "a rule the table does not carry")
		assert.Equal(t, builtin.Deny, defaults[name], name)
	}
	assert.Len(t, strict(Dependencies{}).Builtins, len(defaults))

	assert.Equal(t, denyRuleSedInPlace, Evaluate(Dependencies{}, sedInPlace).Rule.Name, "the default denies")
	assert.Empty(t, Evaluate(advising(Dependencies{}), sedInPlace).Deny, "advise is the opt-out")
}

// sedInPlace is a recoverable deny on its own line, and nothing else.
const sedInPlace = `sed -i 's/a/b/' f.go`

func withSetting(rule string, d builtin.Decision) Dependencies {
	return Dependencies{Builtins: map[string]builtin.Setting{rule: {Decision: d}}}
}

func TestGradeDeny(t *testing.T) {
	t.Parallel()
	deny := ShellVerdict{Deny: "use the editor", Rule: denyRule{Name: denyRuleSedInPlace}}

	for _, tt := range []struct {
		decision builtin.Decision
		want     ShellVerdict
	}{
		{builtin.Deny, deny},
		{builtin.Advise, ShellVerdict{Context: "use the editor", Kind: hint.MarkerKind(denyRuleSedInPlace), demoted: true}},
		{builtin.Off, ShellVerdict{}},
	} {
		got := withSetting(string(denyRuleSedInPlace), tt.decision).grade(deny)
		assert.Equal(t, tt.want, got, tt.decision)
		assert.Equal(t, got, withSetting(string(denyRuleSedInPlace), tt.decision).grade(got), "grading is idempotent: %s", tt.decision)
	}
}

func TestGradeAdvisory(t *testing.T) {
	t.Parallel()
	advice := ShellVerdict{Context: "read the map", Kind: advisorySourceRead, Brief: "map"}

	for _, tt := range []struct {
		decision builtin.Decision
		want     ShellVerdict
	}{
		{builtin.Advise, advice},
		{builtin.Deny, ShellVerdict{Deny: "read the map", Rule: denyRule{Name: denyRuleName(advisorySourceRead)}}},
		{builtin.Off, ShellVerdict{}},
	} {
		assert.Equal(t, tt.want, withSetting(string(advisorySourceRead), tt.decision).grade(advice), tt.decision)
	}
}

func TestGradeDefaultsAndUnknownRules(t *testing.T) {
	t.Parallel()
	var deps Dependencies

	sed := ShellVerdict{Deny: "x", Rule: denyRule{Name: denyRuleSedInPlace}}
	assert.Equal(t, sed, deps.grade(sed), "nil Builtins grades by the defaults")
	assert.True(t, advising(deps, string(denyRuleSedInPlace)).grade(sed).demoted, "advise demotes")
	whole := ShellVerdict{Deny: "x", Rule: denyRule{Name: denyRuleWholeTree}}
	assert.Equal(t, whole, deps.grade(whole))
	uncatalogued := ShellVerdict{Deny: "x", Rule: denyRule{Name: "not-a-rule"}}
	assert.Equal(t, uncatalogued, deps.grade(uncatalogued), "a rule the table does not carry keeps its decision")
}

func TestEvaluateGradesEachSite(t *testing.T) {
	t.Parallel()

	advised := Evaluate(withSetting(string(denyRuleSedInPlace), builtin.Advise), sedInPlace)
	assert.Empty(t, advised.Deny)
	assert.True(t, advised.demoted)
	assert.Equal(t, hint.MarkerKind(denyRuleSedInPlace), advised.Kind)

	assert.Equal(t, denyRuleSedInPlace, Evaluate(Dependencies{}, sedInPlace).Rule.Name)
	assert.Equal(t, ShellVerdict{}, Evaluate(withSetting(string(denyRuleSedInPlace), builtin.Off), sedInPlace))
}

func TestDemotedDenyDoesNotHideALaterDeny(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		sedInPlace + " && git reset --hard",
		"git add -A && git reset --hard",
		"git add -A && git stash",
	} {
		v := Evaluate(advising(Dependencies{}), command)
		assert.Equal(t, denyRuleWholeTree, v.Rule.Name, command)
		assert.NotEmpty(t, v.Deny, command)
	}
}

func TestDemotedDenyOutranksAdvisories(t *testing.T) {
	t.Parallel()

	v := Evaluate(advising(Dependencies{}), sedInPlace+" && git commit -m x")
	assert.True(t, v.demoted, "the held advice outranks the commit advisory")
	assert.Equal(t, hint.MarkerKind(denyRuleSedInPlace), v.Kind)

	off := Evaluate(withSetting(string(denyRuleSedInPlace), builtin.Off), sedInPlace+" && git commit -m x")
	assert.Equal(t, advisoryStageClassify, off.Kind, "an off rule leaves the line to the advisory")
}

func TestDemotedDenyKeepsThePushGate(t *testing.T) {
	t.Parallel()

	v := Evaluate(advising(Dependencies{}), sedInPlace+" && git push")
	assert.Equal(t, advisoryPushGate, v.Rule.Name, "Judge upgrades the push gate, so no advice may hide it")
}

func TestStrongerRanks(t *testing.T) {
	t.Parallel()
	deny := ShellVerdict{Deny: "d", Rule: denyRule{Name: denyRuleWholeTree}}
	demoted := ShellVerdict{Context: "h", Kind: "sed-in-place", demoted: true}
	push := ShellVerdict{Context: "p", Rule: denyRule{Name: advisoryPushGate}}
	advisory := ShellVerdict{Context: "a", Kind: advisorySourceRead}

	for _, tt := range []struct {
		name       string
		a, b, want ShellVerdict
	}{
		{"deny over demoted", demoted, deny, deny},
		{"deny kept over demoted", deny, demoted, deny},
		{"demoted over advisory", advisory, demoted, demoted},
		{"demoted kept over advisory", demoted, advisory, demoted},
		{"advisory over silence", ShellVerdict{}, advisory, advisory},
		{"push gate kept over demoted", push, demoted, push},
		{"first demoted kept", demoted, ShellVerdict{Context: "h2", Kind: "raw-tool", demoted: true}, demoted},
	} {
		assert.Equal(t, tt.want, stronger(tt.a, tt.b), tt.name)
	}
}

func TestRankGraded(t *testing.T) {
	t.Parallel()
	advisory := ShellVerdict{Context: "a", Kind: advisorySourceRead}
	deny := ShellVerdict{Deny: "d", Rule: denyRule{Name: denyRuleWholeTree}}

	lowered := withSetting(string(denyRuleSiblingCheckout), builtin.Advise)
	got := lowered.rankGraded(advisory, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout)
	assert.True(t, got.demoted, "a demoted sibling checkout outranks an advisory")
	assert.Equal(t, deny, lowered.rankGraded(deny, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout))

	assert.Equal(t, denyRuleSiblingCheckout, Dependencies{}.rankGraded(advisory, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout).Rule.Name)
	assert.Equal(t, advisory, withSetting(string(denyRuleSiblingCheckout), builtin.Off).rankGraded(advisory, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout))
}

// The committed load is skipped only when no pending path can change what the root load
// registers, so each shape that can is pinned here.
func TestPendingTouchesLoad(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n"), 0o644))
	sources := []interp.SourceFile{
		{Path: filepath.Join(root, "magusfile.buzz")},
		{Path: filepath.Join(root, "hack", "policy", "guard.buzz")},
	}
	sep := string(filepath.Separator)
	cases := []struct {
		name    string
		pending []string
		want    bool
	}{
		{"a file the load read", []string{filepath.Join(root, "hack", "policy", "guard.buzz")}, true},
		{"the root magusfile", []string{filepath.Join(root, "magusfile.buzz")}, true},
		{"the config that shapes the load", []string{filepath.Join(root, "magus.yaml")}, true},
		{"a new magusfiles source", []string{filepath.Join(root, "magusfiles", "a.buzz")}, true},
		{"an untracked directory holding a source", []string{filepath.Join(root, "hack") + sep}, true},
		{"an untracked magusfiles directory", []string{filepath.Join(root, "magusfiles") + sep}, true},
		{"a spell no load read", []string{filepath.Join(root, "spells", "go", "spell.buzz")}, false},
		{"an untracked directory elsewhere", []string{filepath.Join(root, "scratch") + sep}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pendingTouchesLoad(root, newPendingSet(tc.pending), sources))
		})
	}
}

// A second Pending of an unchanged scope trusts the recorded answer, which is
// how a hook avoids git status. The recorded file is rewritten with a sentinel
// path under the same fingerprint; only a call that did not ask git can return
// it. An edit, and a commit of that edit, both change the fingerprint, so the
// sentinel is dropped and git answers again.
func TestPendingReusesAStatusWhenNothingChanged(t *testing.T) {
	t.Setenv("MAGUS_CACHE_DIR", "")
	repo := testkit.NewRepo(t, map[string]string{"policy.buzz": "fun judge() > void {}\n"})
	root := repo.Root()
	git := func(args ...string) { repo.Git(args...) }
	file := repo.Path("policy.buzz")

	res, err := vcs.Resolve(t.Context(), root, "", types.VCSOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.VCS)
	h := headPolicy{
		workspace: root,
		driver:    res.VCS,
		repoRoot: func(ctx context.Context) (string, error) {
			return res.VCS.Root(ctx, root)
		},
		batch:    &objectBatchState{},
		cacheDir: WorkspaceCacheDir,
	}
	scope := []string{file}

	got, err := h.Pending(t.Context(), scope)
	require.NoError(t, err)
	assert.Empty(t, got)

	cache, ok := h.pendingCacheFile(scope)
	require.True(t, ok)
	body, err := os.ReadFile(cache)
	require.NoError(t, err)
	fp, _, ok := strings.Cut(string(body), "\n")
	require.True(t, ok)
	sentinel := filepath.Join(root, "sentinel.buzz")
	require.NoError(t, os.WriteFile(cache, []byte(fmt.Sprintf(`%s
%s
`, fp, sentinel)), 0o644))

	got, err = h.Pending(t.Context(), scope)
	require.NoError(t, err)
	assert.Equal(t, []string{sentinel}, got, "an unchanged tree must reuse the recorded pending list")

	require.NoError(t, os.WriteFile(file, []byte("fun judge() > void { changed; }\n"), 0o644))
	got, err = h.Pending(t.Context(), scope)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, canonicalPath(file), canonicalPath(got[0]), "an edit must ask git again")

	git("add", "policy.buzz")
	git("commit", "-q", "-m", "edit")
	got, err = h.Pending(t.Context(), scope)
	require.NoError(t, err)
	assert.Empty(t, got, "a commit of that edit is clean, which HEAD has to say because the file bytes did not change again")
}

// gitPolicy is a committed repository holding files, and the approval authority over
// it. git runs a command in the repository.
func gitPolicy(t *testing.T, files map[string]string) (root string, h headPolicy, git func(...string)) {
	t.Helper()
	repo := testkit.NewRepo(t, files)
	root = repo.Root()
	git = func(args ...string) { repo.Git(args...) }
	res, err := vcs.Resolve(t.Context(), root, "", types.VCSOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.VCS)
	return root, headPolicy{
		workspace: root,
		driver:    res.VCS,
		repoRoot:  func(ctx context.Context) (string, error) { return res.VCS.Root(ctx, root) },
		batch:     &objectBatchState{},
		cacheDir:  WorkspaceCacheDir,
	}, git
}

// racingDriver runs before and after around each status, standing in for a
// writer that races the hook.
type racingDriver struct {
	types.VCSDriver
	before, after func()
}

func (d racingDriver) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	d.before()
	out, err := d.VCSDriver.DirtyFiles(ctx, dir, paths)
	d.after()
	return out, err
}

func (d racingDriver) CheckoutID(dir string) (string, bool) {
	return d.VCSDriver.(vcs.CheckoutIDReader).CheckoutID(dir)
}

// A status answer is recorded only for the state it saw. Otherwise a file
// swapped to its committed bytes while status runs, or a checkout moved after
// it, files a clean answer under the key of a loosened tree, and the next hook
// reads that tree as approved.
func TestPendingRecordsOnlyTheStateStatusSaw(t *testing.T) {
	t.Setenv("MAGUS_CACHE_DIR", "")
	loosen := func(t *testing.T, file string) {
		require.NoError(t, os.WriteFile(file, []byte("loosened\n"), 0o644))
	}
	cases := []struct {
		name string
		// setup leaves the file loosened; before and after race the status.
		setup         func(t *testing.T, file string, git func(...string))
		before, after func(t *testing.T, file string, git func(...string))
		// restore puts the loosened bytes back once the racing call returns.
		restore bool
	}{
		{
			name:  "a file swapped to its committed bytes while status runs",
			setup: func(t *testing.T, file string, _ func(...string)) { loosen(t, file) },
			before: func(t *testing.T, file string, _ func(...string)) {
				require.NoError(t, os.WriteFile(file, []byte("strict\n"), 0o644))
			},
			after:   func(*testing.T, string, func(...string)) {},
			restore: true,
		},
		{
			name: "the checkout moved after status",
			setup: func(t *testing.T, file string, git func(...string)) {
				loosen(t, file)
				git("commit", "-q", "-am", "loosen")
			},
			before: func(*testing.T, string, func(...string)) {},
			after:  func(_ *testing.T, _ string, git func(...string)) { git("reset", "-q", "--soft", "HEAD~1") },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, h, git := gitPolicy(t, map[string]string{"policy.buzz": "strict\n"})
			file := filepath.Join(root, "policy.buzz")
			tc.setup(t, file, git)
			scope := []string{file}
			racing := h
			racing.driver = racingDriver{
				VCSDriver: h.driver,
				before:    func() { tc.before(t, file, git) },
				after:     func() { tc.after(t, file, git) },
			}
			_, err := racing.Pending(t.Context(), scope)
			require.NoError(t, err)
			if tc.restore {
				loosen(t, file)
			}

			got, err := h.Pending(t.Context(), scope)
			require.NoError(t, err)
			require.Len(t, got, 1, "the loosened file differs from HEAD")
			assert.Equal(t, canonicalPath(file), canonicalPath(got[0]))
		})
	}
}

// Every scope keeps its own record, so the scopes a hook asks about in turn
// do not evict each other, and the record lives in the cache dir magus.yaml
// names.
func TestPendingRecordsEachScopeInTheConfiguredCacheDir(t *testing.T) {
	t.Setenv("MAGUS_CACHE_DIR", "")
	root, h, _ := gitPolicy(t, map[string]string{
		"magus.yaml": "cache:\n  dir: elsewhere\n",
		"a.buzz":     "a\n",
		"b.buzz":     "b\n",
	})
	scopeA := []string{filepath.Join(root, "a.buzz")}
	scopeB := []string{filepath.Join(root, "b.buzz")}
	for _, scope := range [][]string{scopeA, scopeB} {
		_, err := h.Pending(t.Context(), scope)
		require.NoError(t, err)
	}
	a, ok := h.pendingCacheFile(scopeA)
	require.True(t, ok)
	b, ok := h.pendingCacheFile(scopeB)
	require.True(t, ok)
	assert.NotEqual(t, a, b)
	for _, p := range []string{a, b} {
		assert.FileExists(t, p)
		assert.Equal(t, filepath.Join(root, "elsewhere", "policy-pending"), filepath.Dir(p))
	}
	assert.NoDirExists(t, filepath.Join(root, ".magus"))
}

// A workspace nested in a repository has no marker of its own, and its
// checkout id is read from the repository above it.
func TestCheckoutIDReadsTheRepositoryAboveANestedWorkspace(t *testing.T) {
	root, h, _ := gitPolicy(t, map[string]string{"sub/magusfile.buzz": "x\n"})
	top, ok := checkoutID(h.driver, root)
	require.True(t, ok)
	nested, ok := checkoutID(h.driver, filepath.Join(root, "sub"))
	require.True(t, ok)
	assert.Equal(t, top, nested)
}

// Each hashed record is framed with its kind, so a file whose bytes spell out
// the missing marker does not hash like an absent file.
func TestHashPendingTellsContentFromAMissingFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "x.buzz")
	absent, ok := hashPending(root, []string{p})
	require.True(t, ok)
	require.NoError(t, os.WriteFile(p, []byte("\x07\x00\x00\x00missing"), 0o644))
	present, ok := hashPending(root, []string{p})
	require.True(t, ok)
	assert.NotEqual(t, absent, present)
}

// unbatchedDriver is a driver with no object batch that counts the reads it
// serves, as jj is.
type unbatchedDriver struct {
	types.VCSDriver
	reads *[]string
}

func (d unbatchedDriver) ReadFileAt(ctx context.Context, root, rev, rel string) (string, error) {
	*d.reads = append(*d.reads, rel)
	return d.VCSDriver.ReadFileAt(ctx, root, rev, rel)
}

// Without a batch every approved read is a process, so a file the whole
// tree's pending answer does not cover is read from disk. A batched authority
// reads every path through the batch, since its pending answer may be scoped.
func TestApprovedReaderReadsCleanFilesFromDiskWithoutABatch(t *testing.T) {
	root, h, _ := gitPolicy(t, map[string]string{"clean.buzz": "clean\n", "edited.buzz": "committed\n"})
	clean := filepath.Join(root, "clean.buzz")
	edited := filepath.Join(root, "edited.buzz")
	require.NoError(t, os.WriteFile(edited, []byte("loosened\n"), 0o644))

	var reads []string
	unbatched := h
	unbatched.driver = unbatchedDriver{VCSDriver: h.driver, reads: &reads}
	pending, err := unbatched.Pending(t.Context(), nil)
	require.NoError(t, err)
	read := approvedReader(t.Context(), unbatched, pending)
	got, err := read(clean)
	require.NoError(t, err)
	assert.Equal(t, "clean\n", string(got))
	got, err = read(edited)
	require.NoError(t, err)
	assert.Equal(t, "committed\n", string(got))
	assert.Equal(t, []string{"edited.buzz"}, reads, "only the pending file costs a process")

	assert.True(t, h.batched())
	defer func() { _ = h.Close() }()
	got, err = approvedReader(t.Context(), h, nil)(edited)
	require.NoError(t, err)
	assert.Equal(t, "committed\n", string(got), "a batched authority does not trust disk")
}

// The recorded answer is the guard's own state, private to its owner.
func TestWritePendingIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	ws := t.TempDir()
	h := headPolicy{workspace: ws, cacheDir: func(root string) (string, error) { return filepath.Join(root, ".magus"), nil }}
	h.writePending([]string{"hack"}, "fp", []string{"hack/a.buzz"})

	got, ok := h.readPending([]string{"hack"}, "fp")
	require.True(t, ok)
	assert.Equal(t, []string{"hack/a.buzz"}, got)
	path, ok := h.pendingCacheFile([]string{"hack"})
	require.True(t, ok)
	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// The working tree tightens at once and loosens only as far as the approved declaration,
// which falls back to the compiled defaults where it says nothing.
func TestStricterBuiltins(t *testing.T) {
	defaults := builtin.Defaults()
	require.Equal(t, builtin.Deny, defaults["capture-filter"], "the cases below assume capture-filter denies by default")
	require.Equal(t, builtin.Deny, defaults["whole-tree"], "the cases below assume whole-tree denies by default")

	cases := []struct {
		name     string
		approved map[string]builtin.Setting
		working  map[string]builtin.Setting
		rule     string
		want     builtin.Setting
	}{
		{
			name:     "a tightening applies before approval",
			approved: map[string]builtin.Setting{"capture-filter": {Decision: builtin.Advise}},
			working:  map[string]builtin.Setting{"capture-filter": {Decision: builtin.Deny}},
			rule:     "capture-filter",
			want:     builtin.Setting{Decision: builtin.Deny},
		},
		{
			name:    "a loosening below the default waits",
			working: map[string]builtin.Setting{"whole-tree": {Decision: builtin.Off}},
			rule:    "whole-tree",
			want:    builtin.Setting{Decision: builtin.Deny},
		},
		{
			name:     "a loosening below the approved setting waits",
			approved: map[string]builtin.Setting{"capture-filter": {Decision: builtin.Deny}},
			working:  map[string]builtin.Setting{"capture-filter": {Decision: builtin.Off}},
			rule:     "capture-filter",
			want:     builtin.Setting{Decision: builtin.Deny},
		},
		{
			name:     "an approved loosening applies",
			approved: map[string]builtin.Setting{"capture-filter": {Decision: builtin.Off}},
			working:  map[string]builtin.Setting{"capture-filter": {Decision: builtin.Off}},
			rule:     "capture-filter",
			want:     builtin.Setting{Decision: builtin.Off},
		},
		{
			name:     "dropping an approved loosening restores the default at once",
			approved: map[string]builtin.Setting{"capture-filter": {Decision: builtin.Off}},
			rule:     "capture-filter",
			want:     builtin.Setting{Decision: builtin.Deny},
		},
		{
			name:     "fewer lines is stricter",
			approved: map[string]builtin.Setting{"read-navigation": {Decision: builtin.Deny, Lines: 300}},
			working:  map[string]builtin.Setting{"read-navigation": {Decision: builtin.Deny, Lines: 120}},
			rule:     "read-navigation",
			want:     builtin.Setting{Decision: builtin.Deny, Lines: 120},
		},
		{
			name:     "raising lines waits",
			approved: map[string]builtin.Setting{"read-navigation": {Decision: builtin.Deny, Lines: 120}},
			working:  map[string]builtin.Setting{"read-navigation": {Decision: builtin.Deny, Lines: 300}},
			rule:     "read-navigation",
			want:     builtin.Setting{Decision: builtin.Deny, Lines: 120},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stricterBuiltins(tc.approved, tc.working)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got[tc.rule])
			assert.Len(t, got, len(defaults), "every compiled rule has a setting")
		})
	}
}

// Without an approval authority the working tree is the whole policy, so its loosening
// applies at once.
func TestEffectiveBuiltinsWithoutAnApprovalAuthority(t *testing.T) {
	disabled := false
	rules := &WorkspaceRules{root: t.TempDir(), opts: types.VCSOptions{Enabled: &disabled}, registry: workspace.NewWorkspaceRegistry()}

	got, err := rules.effectiveBuiltins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, builtin.Setting{Decision: builtin.Advise, Profile: types.GuardProfileLight}, got["stall"],
		"nothing declared is the light level")
	assert.Equal(t, builtin.Setting{Decision: builtin.Off}, got["root-repeat"], "a rule light leaves alone keeps its default")

	rules.registry.SetBuiltins(map[string]builtin.Setting{"whole-tree": {Decision: builtin.Off}})
	got, err = rules.effectiveBuiltins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, builtin.Setting{Decision: builtin.Off}, got["whole-tree"])
	assert.Equal(t, builtin.Setting{Decision: builtin.Deny}, got["capture-filter"])
}

// A builtins declaration is policy: the digest moves with it, and declaring one alone is
// enough for there to be one.
func TestPolicyDigestCoversBuiltins(t *testing.T) {
	none := buildPolicy(nil, false, false, false, nil, nil)
	assert.Empty(t, none.Digest)

	off := buildPolicy(nil, false, false, false, map[string]builtin.Setting{"capture-filter": {Decision: builtin.Off}}, nil)
	deny := buildPolicy(nil, false, false, false, map[string]builtin.Setting{"capture-filter": {Decision: builtin.Deny}}, nil)
	lines := buildPolicy(nil, false, false, false, map[string]builtin.Setting{"read-navigation": {Decision: builtin.Deny, Lines: 120}}, nil)
	moreLines := buildPolicy(nil, false, false, false, map[string]builtin.Setting{"read-navigation": {Decision: builtin.Deny, Lines: 300}}, nil)
	assert.NotEmpty(t, off.Digest)
	assert.Equal(t, 1, off.Builtins)
	assert.NotEqual(t, off.Digest, deny.Digest)
	assert.NotEqual(t, lines.Digest, moreLines.Digest)

	shell := []workspace.ShellRule{{Name: "x", Decision: "deny", Program: "curl", Reason: "r"}}
	assert.NotEqual(t,
		buildPolicy(shell, false, false, false, nil, nil).Digest,
		buildPolicy(shell, false, false, false, map[string]builtin.Setting{"capture-filter": {Decision: builtin.Off}}, nil).Digest)
}

func TestPendingSetCoversFilesUnderAnUntrackedDirectory(t *testing.T) {
	root := t.TempDir()
	set := newPendingSet([]string{filepath.Join(root, "new") + string(filepath.Separator), filepath.Join(root, "a.buzz")})
	assert.True(t, set.covers(filepath.Join(root, "a.buzz")))
	assert.True(t, set.covers(filepath.Join(root, "new", "deep", "b.buzz")))
	assert.False(t, set.covers(filepath.Join(root, "newer.buzz")), "a sibling sharing the prefix is not under it")
	assert.False(t, set.covers(filepath.Join(root, "b.buzz")))
}

// profileRules isolates the test and returns rules with no approval authority, so the
// working tree is the whole workspace policy, and the user tier's config directory. Rules
// are registered directly rather than loaded, so no magusfile is compiled.
func profileRules(t *testing.T, level types.GuardProfile) (*WorkspaceRules, string) {
	t.Helper()
	testkit.Isolate(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	disabled := false
	rules := &WorkspaceRules{root: t.TempDir(), opts: types.VCSOptions{Enabled: &disabled}, registry: workspace.NewWorkspaceRegistry()}
	if level != "" {
		rules.registry.SetProfile(level)
	}
	return rules, filepath.Join(home, "magus")
}

func userAgents(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(body), 0o600))
}

func TestGuardProfileUnsetIsLight(t *testing.T) {
	rules, _ := profileRules(t, "")
	assert.Equal(t, types.GuardProfileLight, EffectiveProfile(t.Context(), rules, ""))

	approved, err := LoadApprovedProfile(t.Context(), rules.root)
	require.NoError(t, err)
	assert.Equal(t, types.GuardProfileLight, approved, "no approval authority is light")
	assert.Equal(t, types.GuardProfileLight, EffectiveProfile(t.Context(), nil, rules.root))
}

// A person's agents.profile raises the workspace's level and never lowers it, including
// below the light a workspace that declares nothing gets.
func TestGuardProfileUserTierOnlyTightens(t *testing.T) {
	rules, user := profileRules(t, types.GuardProfileStandard)
	assert.Equal(t, types.GuardProfileStandard, EffectiveProfile(t.Context(), rules, ""))

	userAgents(t, user, "agents:\n  profile: strict\n")
	assert.Equal(t, types.GuardProfileStrict, EffectiveProfile(t.Context(), rules, ""))
	userAgents(t, user, "agents:\n  profile: off\n")
	assert.Equal(t, types.GuardProfileStandard, EffectiveProfile(t.Context(), rules, ""))

	unset, user := profileRules(t, "")
	userAgents(t, user, "agents:\n  profile: off\n")
	assert.Equal(t, types.GuardProfileLight, EffectiveProfile(t.Context(), unset, ""))

	off, user := profileRules(t, types.GuardProfileOff)
	assert.Equal(t, types.GuardProfileOff, EffectiveProfile(t.Context(), off, ""), "a workspace may declare off")
	userAgents(t, user, "agents:\n  profile: standard\n")
	assert.Equal(t, types.GuardProfileStandard, EffectiveProfile(t.Context(), off, ""))
}

// With no level that sets anything, a declaration still applies, and a magus.yaml that
// does not load is reported beside the answer, never in place of it.
func TestEffectiveBuiltinsReadsTheUserTier(t *testing.T) {
	rules, user := profileRules(t, types.GuardProfileOff)
	got, err := rules.effectiveBuiltins(t.Context())
	require.NoError(t, err)
	assert.Nil(t, got, "no level sets a rule yet, so nothing is declared")

	rules.registry.SetBuiltins(map[string]builtin.Setting{"whole-tree": {Decision: builtin.Off}})
	got, err = rules.effectiveBuiltins(t.Context())
	require.NoError(t, err)
	assert.Equal(t, builtin.Setting{Decision: builtin.Off}, got["whole-tree"])

	userAgents(t, user, "agents:\n  billing: oauth\n")
	got, err = rules.effectiveBuiltins(t.Context())
	require.Error(t, err)
	assert.Equal(t, builtin.Setting{Decision: builtin.Off}, got["whole-tree"], "the declaration still applies")
	assert.Equal(t, builtin.Setting{Decision: builtin.Off}, BuiltinSettings(t.Context(), rules, "")["whole-tree"])
}

// A workspace whose level sets nothing still hands the hook the light level's settings, not
// the bare defaults nil stands for.
func TestBuiltinSettingsFallsBackToTheLightLevel(t *testing.T) {
	rules, _ := profileRules(t, types.GuardProfileOff)
	got := BuiltinSettings(t.Context(), rules, "")
	require.NotNil(t, got, "the compiled-in light row resolves")
	assert.Equal(t, lightBuiltins(), got)
	assert.True(t, slices.ContainsFunc(slices.Collect(maps.Values(got)), func(s builtin.Setting) bool {
		return s.Profile == types.GuardProfileLight
	}), "the light row's settings are in it")
}

// The approved side alone resolves the declaration over the defaults when no working tree
// loaded; with no approval authority that is the defaults.
func TestLoadApprovedBuiltinsWithoutAnAuthority(t *testing.T) {
	rules, user := profileRules(t, "")
	got, err := LoadApprovedBuiltins(t.Context(), rules.root)
	require.NoError(t, err)
	assert.Equal(t, builtin.Setting{Decision: builtin.Deny}, got["whole-tree"])

	userAgents(t, user, "agents:\n  profile: max\n")
	_, err = LoadApprovedBuiltins(t.Context(), rules.root)
	require.Error(t, err, "a magus.yaml that does not load is an error")
}

// The push facts come through the version control that resolves in the checkout: a
// branch checkout names its branch, a detached one names none, and a directory under no
// version control is unknown.
func TestCheckoutStateAt(t *testing.T) {
	repo := testkit.NewRepo(t, map[string]string{"README": "x\n"})
	root := repo.Root()
	repo.Git("update-ref", "refs/remotes/origin/main", "HEAD")

	got := checkoutStateAt(t.Context(), root)
	require.NotNil(t, got)
	assert.NotEmpty(t, got.Branch)
	assert.Equal(t, []string{"origin/main"}, got.RemoteBranches)

	repo.Git("checkout", "-q", "--detach")
	got = checkoutStateAt(t.Context(), root)
	require.NotNil(t, got)
	assert.Empty(t, got.Branch)

	assert.Nil(t, checkoutStateAt(t.Context(), t.TempDir()), "outside any version control")
}

// The 10-13 lapses stalled in the job store's read, which sweeps it and can run the VCS
// once per row: every hook ran out after its load_rules phase with no phase named. The read
// is now a timed phase bounded by the deadline: a store that never answers pins the
// deadline's error, records job_rows lapsed, answers a second read at once, and reaches the
// workspace rules as a lapse. A FIFO with no writer stands in for the stalled store.
func TestJobRowsReadIsBoundedByTheDeadline(t *testing.T) {
	mkfifo, err := exec.LookPath("mkfifo")
	if err != nil {
		t.Skip("no mkfifo to stand in for a stalled store")
	}
	ctx, cacheDir := spawnFixture(t)
	at := hookLocation(ctx, Dependencies{})
	path, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: at.workspace}).Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, exec.CommandContext(ctx, mkfifo, path).Run())
	t.Cleanup(func() {
		// A writer that opens and closes ends the stalled read with EOF. The read is
		// parked in its own open, so this open does not wait for a reader.
		go func() {
			if w, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
				_ = w.Close()
			}
		}()
	})

	ctx = withPhaseLog(ctx, trail.Phase{})
	ctx, cancel := context.WithTimeout(ctx, judgeReserve+200*time.Millisecond)
	defer cancel()
	start := time.Now()
	pinned := withJobStoreRows(ctx, at)
	assert.Less(t, time.Since(start), time.Second, "bounded by the deadline less the reserve")
	snap, ok := job.SnapshotFromContext(pinned)
	require.True(t, ok)
	require.ErrorIs(t, snap.Err, context.DeadlineExceeded)
	phase, ok := judgmentPhases(ctx).lapsedPhase(phaseJobRows)
	require.True(t, ok, "recorded as the lapsed job_rows phase")
	assert.Positive(t, phase.BudgetMs)

	start = time.Now()
	snap, _ = job.SnapshotFromContext(withJobStoreRows(ctx, at))
	assert.Less(t, time.Since(start), 50*time.Millisecond, "a second read answers at once")
	assert.ErrorIs(t, snap.Err, context.DeadlineExceeded)

	asked := askWorkspaceRules(ctx, seamCommand, Dependencies{}, nil, nil)
	require.NotNil(t, asked.lapse)
	assert.Contains(t, asked.lapse.what(), "reading the job store did not finish within")
}
