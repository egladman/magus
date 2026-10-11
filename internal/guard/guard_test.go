package guard

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	// Blank-imported so its init installs the spell registry's ensure hook, exactly as
	// cmd/magus/packs_interp.go does for the real binary: without it,
	// project.DefaultSpellRegistry().All() in testDependencies below runs against a registry
	// nothing ever populated, and every raw-tool test would match against an empty
	// catalog. See internal/interp/bindings/spell.go's init.
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/cache"
	_ "github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// verdictWithRule is the verdict a rule reaches before any wording or lease attribution:
// pair it with unworded so a test compares the whole struct and still checks the prose by
// substring.
func verdictWithRule(decision, rule string) Verdict {
	return Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: decision, Rule: rule}
}

// unworded is v without the fields a rule test leaves to other assertions: the reason and
// context prose, which the test checks by substring, and the lease attribution, which follows
// the environment the test ran in.
func unworded(v Verdict) Verdict {
	v.Reason, v.Context, v.Lease, v.LeaseFrom = "", "", "", ""
	return v
}

// testDependencies resolves the workspace the way the CLI's hookDeps does, so a rule graded
// here reads the same tree the hook would.
func testDependencies() Dependencies {
	return Dependencies{
		Inspect: func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
			if root == "" {
				found, err := magus.FindRoot("")
				if err != nil {
					return nil, err
				}
				root = found
			}
			return magus.Inspect(ctx, root)
		},
		CacheDir: func(root string) (string, error) {
			if root == "" {
				found, err := magus.FindRoot("")
				if err != nil {
					return "", err
				}
				root = found
			}
			return magus.ResolveCacheDir(root)
		},
		Spells: project.DefaultSpellRegistry().All,
	}
}

// TestDecodeHookEnvelope_CommandStillWinsOverPrompt guards the ordering that makes the spawn
// branch purely additive: a payload carrying both is judged as the command it carries, so no
// envelope the guard already evaluated can start skipping the guard because a prompt appeared
// beside it.
func TestDecodeHookEnvelope_CommandStillWinsOverPrompt(t *testing.T) {
	req, ok := decodeHookEnvelope(`{"tool_input":{"command":"git stash","prompt":"delegate this"}}`)
	require.True(t, ok)
	assert.Equal(t, "git stash", req.Value)
	assert.False(t, req.IsSpawn)

	req, ok = decodeHookEnvelope(`{"tool_input":{"file_path":"MAGUS.md","prompt":"delegate this"}}`)
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
	req, ok := decodeHookEnvelope(cmdPayload)
	require.True(t, ok)
	assert.Equal(t, hookRequest{Value: "magus run ci | tail", Who: hookAttribution{Session: "s1", Event: "PreToolUse"}}, req)

	// A file_path payload is a WRITE, so the envelope decides the --path question too.
	writePayload := `{"hook_event_name":"PreToolUse","tool_input":{"file_path":"MAGUS.md"}}`
	req, ok = decodeHookEnvelope(writePayload)
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
		_, ok := decodeHookEnvelope(raw)
		assert.False(t, ok, "must fall through to the literal form: %q", raw)
	}

	// TestHookCmd_EnvelopeWithNothingToJudge (cmd/magus/hook_test.go) exercises the same
	// payload end to end through hookCmd; this pins the decoder's own half of it, which that
	// test cannot reach directly since decodeHookEnvelope is unexported to this package.
	const nothingToJudge = `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"TodoWrite",` +
		`"tool_input":{"todos":[{"content":"never use sed -i here"}]}}`
	req, ok = decodeHookEnvelope(nothingToJudge)
	require.True(t, ok, "a payload naming a host hook event is an envelope")
	assert.True(t, req.NothingToJudge)
	assert.Empty(t, req.Value)

	// The same JSON with no envelope marker on it is still judged as the text it is: only a
	// payload that identifies itself as a host hook may claim the exemption.
	_, ok = decodeHookEnvelope(`{"tool_input":{"todos":[{"content":"never use sed -i here"}]}}`)
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
		req, ok := decodeHookEnvelope(payload)
		require.True(t, ok, name)
		assert.True(t, req.IsPath, name)
		assert.Equal(t, "MAGUS.md", req.Value, name)
	}

	// A field arriving with an unexpected type still reaches the MCP arm. Typed, it
	// failed the unmarshal outright and the raw JSON was judged as a shell line, which is
	// the one outcome the default arm of the decoder exists to prevent.
	req, ok := decodeHookEnvelope(`{"tool_name":"mcp__magus__buzz","tool_input":{"path":123}}`)
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
	assert.Equal(t, Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "deny", Reason: got.Reason}, got)
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
	assert.Equal(t, "pass", got.Decision, "an empty input from a person passes")
	assert.NotContains(t, got.Reason, string(types.HookHostUnnamed))
}

// A command typed at a terminal is the CLI's: it records no session and is not an agent's,
// while its terminal window still keys what the guard remembers for it.
func TestATerminalCallIsRecordedAsTheCLIWithNoSession(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
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
	// jobs are the rows the job store holds when the input arrives.
	jobs []types.Job
	// corruptStore replaces the job store with bytes it cannot decode.
	corruptStore bool
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
	root, err := magus.FindRoot("")
	require.NoError(t, err)
	m, err := magus.LoadGuardRules(t.Context(), root, types.VCSOptions{})
	require.NoError(t, err, "the policy loads the way the hook loads it")
	require.NotNil(t, m.CommandRule(), "the root magusfile registers a command rule")
	require.NotNil(t, m.SpawnRule(), "and a spawn rule")
	require.NotNil(t, m.WriteRule(), "and a write rule")

	cases := []policyCase{
		{rule: "ci-watch", name: "gh pr checks --watch", input: bash("gh pr checks 183 --watch"), decision: "deny", reason: "GREEN CHANGES NOTHING"},
		{rule: "ci-watch", name: "a while loop over gh pr checks", input: bash("while true; do gh pr checks 183; sleep 30; done"), decision: "deny", reason: "GREEN CHANGES NOTHING"},
		{rule: "ci-watch", name: "a for loop that sleeps over gh pr checks", input: bash("for i in $(seq 1 20); do gh pr checks 183; sleep 30; done"), decision: "deny", reason: "GREEN CHANGES NOTHING"},
		{rule: "ci-watch", name: "gh run watch behind env and timeout", input: bash("GH_TOKEN=x timeout 600 gh run watch 34069443069"), decision: "deny", reason: "GREEN CHANGES NOTHING"},
		{rule: "ci-batch-poll", name: "one pull request's checks", input: bash("gh pr checks 183"), decision: "advise", reason: "gh pr list --state open"},
		{rule: "queue-poll", name: "the queue's runs", input: bash("gh run list --workflow queue.yaml --limit 5"), decision: "advise", reason: "magus queue ls"},
		{rule: "admin-merge", name: "gh pr merge --admin", input: bash("gh pr merge 12 --squash --admin"), decision: "deny", reason: "--auto --squash"},
		{rule: "pr-watch", name: "a for loop that sleeps over a pull request's state", input: bash("for i in 1 2 3; do gh pr view 300 --json state,autoMergeRequest; sleep 60; done"), decision: "deny", reason: "CI monitor"},
		{rule: "pr-watch", name: "a while loop over the pulls endpoint", input: bash(`while true; do gh api repos/egladman/magus/pulls/300 --jq .merged; sleep 60; done`), decision: "deny", reason: "CI monitor"},
		{rule: "pr-watch", name: "one read of a pull request's state", input: bash("gh pr view 300 --json state,autoMergeRequest"), decision: "pass"},
		{rule: "worker-runs-gate", name: "a review worker runs the gate", input: bash("./magus affected ci --no-default-charms"), worker: "root/review footprint", decision: "deny", reason: "integrate"},
		{rule: "worker-runs-gate", name: "an integrate worker runs the gate", input: bash("./magus affected ci --no-default-charms"), worker: "root/integrate footprint", decision: "pass"},
		{rule: "detached-push-unqualified", name: "a detached push to a new branch", input: bash("git push origin HEAD:guard-pr-polling"),
			checkout: &types.CheckoutState{RemoteBranches: []string{"origin/main"}}, decision: "deny", reason: "HEAD:refs/heads/guard-pr-polling"},
		{rule: "push-authority", name: "a subagent opens a pull request", input: bash(`gh pr create --title "fix: pin the key" --body "Pins it."`),
			worker: "root/feat footprint", decision: "deny", reason: "only the main session pushes or opens a pull request"},
		{rule: "branch-name", name: "the main session pushes an uppercase, underscored name", input: bash("git push origin HEAD:Pin_Key"),
			checkout: &types.CheckoutState{Branch: "pin-key", Base: "origin/main", RemoteBranches: []string{"origin/main"}}, decision: "deny", reason: "`Pin_Key` is not a branch name to publish"},
		{rule: "spawn-without-job-row", name: "a spawn titled for no job", input: agentSpawn(map[string]any{
			"description": "audit the store", "prompt": "Audit it.", "model": "sonnet",
		}), decision: "deny", reason: "magus job fork <job> --model sonnet"},
		{rule: "spawn-without-job-row", name: "a spawn naming a row the store lacks", input: agentSpawn(map[string]any{
			"description": "root/review footprint", "prompt": "Review it.", "model": "sonnet",
		}), decision: "deny", reason: "magus job fork footprint --parent root --model sonnet --read-only"},
		{rule: "spawn-without-job-row", name: "a spawn naming an ended row", input: agentSpawn(map[string]any{
			"description": "root/review footprint", "prompt": "Review it.", "model": "sonnet",
		}), jobs: []types.Job{{ID: "footprint", State: types.StatePass}}, decision: "deny", reason: "has ended (pass)"},
		{rule: "spawn-without-job-row", name: "a spawn naming a live nested row", input: agentSpawn(map[string]any{
			"description": "root/review footprint", "prompt": "Review it.", "model": "sonnet",
		}), jobs: []types.Job{{ID: "root/footprint", Parent: "root", State: types.StateDeclared, ReadOnly: true}}, decision: "pass"},
		{rule: "spawn-without-job-row", name: "a spawn while the store cannot be read", input: agentSpawn(map[string]any{
			"description": "root/review footprint", "prompt": "Review it.", "model": "sonnet",
		}), corruptStore: true, decision: "advise", reason: "went unchecked"},
		{rule: "change-role-spawn-not-isolated", name: "a feat worker sharing the checkout", input: agentSpawn(map[string]any{
			"description": "root/feat footprint", "prompt": "Build it.", "model": "sonnet",
		}), jobs: []types.Job{{ID: "footprint", State: types.StateDeclared}}, decision: "deny", reason: `isolation: "worktree"`},
		{rule: "change-role-spawn-not-isolated", name: "a feat worker in its own worktree", input: agentSpawn(map[string]any{
			"description": "root/feat footprint", "prompt": "Build it.", "model": "sonnet", "isolation": "worktree",
		}), jobs: []types.Job{{ID: "footprint", State: types.StateDeclared}}, decision: "pass"},
		{rule: "worker-bootstrap", name: "a subagent runs the bootstrap", input: bash("go run -trimpath ./cmd/magus run go-build --no-cache ."),
			worker: "root/feat footprint", decision: "deny", reason: "a worker does not build magus"},
		{rule: "host-capture", name: "a cat of a run log", input: bash("cat .magus/logs/0123abcd.log"), decision: "deny", reason: "magus query output"},
		{rule: "host-terminals", name: "a mkdir of a terminals directory", input: bash("mkdir -p terminals"), decision: "deny", reason: "a directory named terminals is a host session folder, not a run"},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.rule] = true
		t.Run(tc.rule+"/"+tc.name, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			store := job.NewStore(job.Location{CacheDir: cacheDir, Root: hookLocation(ctx, Dependencies{}).workspace})
			for _, row := range tc.jobs {
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
			deps := Dependencies{CommandRule: m.CommandRule(), SpawnRule: m.SpawnRule(), WriteRule: m.WriteRule()}
			if tc.checkout != nil {
				deps.CheckoutState = func(context.Context, string) *types.CheckoutState { return tc.checkout }
			}
			v := Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, input)})
			assert.Equal(t, tc.decision, v.Decision, v.Reason)
			if tc.decision != "pass" {
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
	shellAt := func(t *testing.T, branch, workspace string) (func(command string) Verdict, string) {
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
		return func(command string) Verdict {
			return Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
				"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Bash",
				"tool_input": map[string]any{"command": command},
			})})
		}, cacheDir
	}
	shellIn := func(t *testing.T, branch, workspace string) func(command string) Verdict {
		t.Helper()
		run, _ := shellAt(t, branch, workspace)
		return run
	}

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
			assert.NotEqual(t, "deny", v.Decision, line+": "+v.Reason)
		}
		for _, line := range []string{
			`git -C . commit -m "Fix the port."`,
			`hg -q commit -m "Fix the port."`,
			`sl commit -m "Fix the port."`,
			`jj --color=never commit -m "Fix the port."`,
			`jj -R . describe -m "Fix the port."`,
		} {
			v := run(line)
			assert.Equal(t, verdictWithRule("deny", workspaceCommandRule), unworded(v), line)
			assert.Contains(t, v.Reason, "a capitalized first word", line)
			assert.Contains(t, v.Reason, "Write one subject line, lowercase and imperative", line)
			assert.NotContains(t, v.Reason, "Skill(", line)
		}

		v := run("git add a.go && git commit -m \"$(cat <<'EOF'\nFix the port.\n\nCo-Authored-By: a <b@c>\nEOF\n)\"")
		assert.Equal(t, "deny", v.Decision, v.Reason)
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
			assert.NotEqual(t, "deny", v.Decision, line+": "+v.Reason)
		}
		v := run(`git commit -m "trim a fragment"`)
		assert.Equal(t, "deny", v.Decision, v.Reason)
		assert.Contains(t, v.Reason, "A commit made on main lands as written")
		assert.Contains(t, v.Reason, `"trim a fragment": no `+"`<type>: `"+` prefix`)
	})

	covered["pull-request-text"] = true
	t.Run("pull-request-text", func(t *testing.T) {
		// The rule runs proofread, which `magus run proofread-build libs/conventions` links into
		// the workspace's libs/conventions/gen, so this one holds that module, built output
		// and all; the root test target needs proofread-build. It is not the checkout itself, whose
		// branch diff would add advice about the areas it touches to every verdict.
		conventions := filepath.Join(root, "libs", "conventions")
		ws := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(ws, "libs"), 0o755))
		require.NoError(t, os.Symlink(conventions, filepath.Join(ws, "libs", "conventions")))
		run, cacheDir := shellAt(t, "trim-key", ws)
		const describe = `gh pr create --title "fix(cache): pin the key" --body "Warm builds miss the cache because the key hashes its inputs in map order; sorting them pins one key per build."`
		v := run(describe)
		assert.Equal(t, "pass", v.Decision, "judged, not failed open: "+v.Reason+v.Context)

		// A cited ref is looked up in the store of the checkout being judged.
		store := cache.NewOutputStore(cacheDir)
		passed, err := store.Persist(t.Context(), strings.Repeat("a1", 32), []byte("ok\n"),
			cache.OutputDescriptor{Project: ".", Target: "go-test"})
		require.NoError(t, err)
		failed, err := store.Persist(t.Context(), strings.Repeat("b2", 32), []byte("FAIL\n"),
			cache.OutputDescriptor{Project: "libs/conventions", Target: "go-test", Failed: true})
		require.NoError(t, err)
		citing := func(ref string) string {
			return `gh pr edit 9 --title "fix(cache): pin the key" --body "Warm builds hit the cache again because the key now sorts its inputs; TestKey passed (` + ref + `)."`
		}
		v = run(citing(passed.Ref))
		assert.Equal(t, "pass", v.Decision, "a passing run this checkout holds: "+v.Reason+v.Context)
		v = run(citing(failed.Ref))
		assert.Equal(t, verdictWithRule("deny", workspaceShellPrefix+"pull-request-text"), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "`"+failed.Ref+"` cites a failed run (libs/conventions:go-test)")
		v = run(citing("outc3c3c3c3c3c3"))
		assert.Equal(t, verdictWithRule("deny", workspaceShellPrefix+"pull-request-text"), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "`outc3c3c3c3c3c3` cites a run this checkout holds no record of")

		// attribution ships off; this repository's decisions table turns it on.
		v = run("gh pr create --title \"Pin the key\" --body \"$(cat <<'EOF'\nClaude pinned the key.\nEOF\n)\"")
		assert.Equal(t, verdictWithRule("deny", workspaceShellPrefix+"pull-request-text"), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, `pr-title: "Pin the key": no `+"`<type>: `"+` prefix`)
		assert.Contains(t, v.Reason, "Drop 'Claude': describe the change, not who or what produced it. [attribution]")
		assert.NotContains(t, v.Reason, "Skill(")

		v = run(`gh pr edit 412 --title "fix(cache): pin the key" --body "Workers miss the skill rules because the guard reads a stale copy; it reads .claude/skills/x/SKILL.md instead."`)
		assert.Equal(t, "pass", v.Decision, "a path spelling a tool's name credits no one: "+v.Reason+v.Context)

		v = run(`gh pr comment 412 --body "Good catch: the map is written from two goroutines, so it takes a lock now."`)
		assert.NotEqual(t, "deny", v.Decision, "a reply is judged as a reply: "+v.Reason+v.Context)
		v = run(`gh pr comment 412 --body "No, as I said, the map is shared."`)
		assert.Equal(t, verdictWithRule("deny", workspaceShellPrefix+"pull-request-text"), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "[reply-opener]")

		// The same sources with no gen/ dir: a judge never built denies, and never compiles.
		unbuilt := t.TempDir()
		mod := filepath.Join(unbuilt, "libs", "conventions")
		require.NoError(t, os.MkdirAll(mod, 0o755))
		for _, name := range []string{"go.mod", "go.sum", "proofread", "cmd"} {
			require.NoError(t, os.Symlink(filepath.Join(conventions, name), filepath.Join(mod, name)))
		}
		v = shellIn(t, "trim-key", unbuilt)(describe)
		assert.Equal(t, "deny", v.Decision, v.Reason)
		assert.Contains(t, v.Reason, "libs/conventions/gen/proofread is not built; run `magus run proofread-build libs/conventions`")

		// The built judge beside a source it was not linked from is as stale as none.
		require.NoError(t, os.Symlink(filepath.Join(conventions, "gen"), filepath.Join(mod, "gen")))
		require.NoError(t, os.Remove(filepath.Join(mod, "proofread")))
		require.NoError(t, os.Mkdir(filepath.Join(mod, "proofread"), 0o755))
		sources, err := filepath.Glob(filepath.Join(conventions, "proofread", "*.go"))
		require.NoError(t, err)
		for _, src := range sources {
			require.NoError(t, os.Symlink(src, filepath.Join(mod, "proofread", filepath.Base(src))))
		}
		require.NoError(t, os.WriteFile(filepath.Join(mod, "proofread", "zz.go"), []byte("package proofread\n"), 0o644))
		v = shellIn(t, "trim-key", unbuilt)(describe)
		assert.Equal(t, "deny", v.Decision, v.Reason)
		assert.Contains(t, v.Reason, "was linked from other sources than the tree holds; run `magus run proofread-build libs/conventions`")
	})

	covered["code-comments"] = true
	t.Run("code-comments", func(t *testing.T) {
		ctx, ws, _ := writeFixture(t)
		write := func(name, content string) Verdict {
			return Judge(ctx, Dependencies{WriteRule: m.WriteRule()}, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
				"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Write",
				"tool_input": map[string]any{"file_path": filepath.Join(ws, name), "content": content},
			})})
		}
		const rule = "keep only the comments that say what the code cannot"
		assert.NotContains(t, write("notes.md", "# A heading\n").Context, rule, "prose is not code")
		v := write("a.go", "package a\n\n// keyOf is stable across runs.\nfunc keyOf() {}\n")
		assert.Equal(t, "advise", v.Decision, v.Reason)
		assert.Contains(t, v.Reason+v.Context, rule)
		assert.NotContains(t, v.Reason+v.Context, "Skill(")
		assert.NotContains(t, write("b.go", "package a\n\n// again\n").Context, rule, "once per session")
	})

	// The binary rules read the trees a line names, so each case builds its own.
	judgeIn := func(t *testing.T, ws, command string) Verdict {
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
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/egladman/magus\n\ngo 1.25\n"), 0o644))
		if withBinary {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "magus"), []byte("binary"), 0o755))
		}
		return dir
	}

	covered["foreign-binary"] = true
	t.Run("foreign-binary", func(t *testing.T) {
		built, here := checkout(t, true), checkout(t, false)
		v := judgeIn(t, here, filepath.Join(built, "magus")+" run lint .")
		assert.Equal(t, verdictWithRule("deny", workspaceCommandRule), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "another checkout of magus")
	})

	covered["go-into-checkout"] = true
	t.Run("go-into-checkout", func(t *testing.T) {
		other := checkout(t, true)
		v := judgeIn(t, checkout(t, false), "go -C "+other+" test ./...")
		assert.Equal(t, verdictWithRule("deny", workspaceCommandRule), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "runs a toolchain command in another checkout")

		bare := checkout(t, false)
		assert.Equal(t, "pass", judgeIn(t, checkout(t, false), "GOEXPERIMENT=jsonv2 go -C "+bare+" run -trimpath ./cmd/magus run go-build --no-cache .").Decision,
			"the bootstrap into a checkout with no binary passes")
		assert.Equal(t, "deny", judgeIn(t, checkout(t, false), "go -C "+bare+" build -o magus ./cmd/magus").Decision,
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
		assert.Equal(t, verdictWithRule("advise", workspaceCommandRule), unworded(v), v.Reason)
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
		assert.Equal(t, verdictWithRule("deny", workspaceCommandRule), unworded(v), v.Reason)
		assert.Contains(t, v.Reason, "no go-build stamp")
		assert.Equal(t, "advise", judgeIn(t, ws, "./magus init --dry-run").Decision, "a dry run writes nothing")
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
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(trail.EnvBaggage, "")
	root, elsewhere := t.TempDir(), t.TempDir()
	ctx := WithLocation(t.Context(), t.TempDir(), root, root)
	write := func(path string) Verdict {
		return Judge(ctx, Dependencies{}, Request{Input: path, IsPath: true, Session: "s1", Host: "test-host"})
	}

	assert.Equal(t, "pass", write(elsewhere+"/CLAUDE.md").Decision)
	inside := write(root + "/CLAUDE.md")
	assert.Equal(t, verdictWithRule("advise", string(advisoryInstruction)), unworded(inside))
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
func withoutVerdictRef(v Verdict) Verdict {
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
		want  Verdict
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
			want: Verdict{Decision: "advise", Rule: string(advisoryScopeDrift)},
		},
		{
			name: "a once-per-session notice",
			setup: func(t *testing.T) (context.Context, Dependencies, Request) {
				ctx, _ := fleetFixture(t, types.Job{ID: "finished", State: types.StatePass})
				return ctx, Dependencies{}, Request{Input: "ls", Lease: "finished", Session: "s1"}
			},
			want: Verdict{Decision: "advise", Rule: string(advisoryLeaseTerminal), Lease: "finished", LeaseFrom: types.LeaseSourceFlag},
		},
		{
			name: "a denied command",
			setup: func(t *testing.T) (context.Context, Dependencies, Request) {
				testkit.Isolate(t)
				return WithLocation(t.Context(), t.TempDir(), "/ws", "/ws"), strict(Dependencies{}), Request{Input: "magus run ci | tail", Session: "s1"}
			},
			want: Verdict{Decision: "deny"},
		},
		{
			name: "an unregistered worker's first write",
			setup: func(t *testing.T) (context.Context, Dependencies, Request) {
				ctx, root := fleetFixture(t, types.Job{ID: "worker", State: types.StateRunning, WritePaths: []string{"app/**"}})
				bindCaller(t, ctx, hookAttribution{}, "worker")
				deps := Dependencies{CheckoutBase: func(context.Context, string) string { return "77aa01c" }}
				return ctx, deps, Request{Input: filepath.Join(root, "app", "main.go"), IsPath: true}
			},
			want: Verdict{Decision: "pass", Lease: "worker", LeaseFrom: types.LeaseSourceMarker},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, deps, req := tc.setup(t)
			t.Setenv(trail.EnvBaggage, "")
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
					got := Verdict{Decision: real.Decision, Rule: real.Rule, Lease: real.Lease, LeaseFrom: real.LeaseFrom}
					if tc.want.Rule == "" {
						got.Rule = ""
					}
					assert.Equal(t, tc.want, got)
					continue
				}
				if real.Decision == "deny" {
					real.Reason, preview.Reason = "", ""
				}
				assert.Equal(t, withoutVerdictRef(real), preview, "round %d", round)
			}
		})
	}
}

// Judging a spawn records it, so a dry run refuses one rather than record it.
func TestDryRunRefusesASpawn(t *testing.T) {
	testkit.Isolate(t)
	cacheDir := t.TempDir()
	ctx := WithLocation(t.Context(), cacheDir, "/ws", "/ws")
	v := Judge(ctx, Dependencies{}, Request{Input: claudeSpawnEnvelope, Host: "claude-code", DryRun: true})
	assert.Equal(t, "deny", v.Decision)
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
	want := hint.Next{
		ID: "deny-output-redirect", Run: "magus ls jobs -o json --tee f",
		Argv: []string{"magus", "ls", "jobs", "-o", "json", "--tee", "f"},
		Why:  "--tee writes the structured record to the file and still prints it.",
	}
	ref := verdictRef.FindString(v.Reason)
	require.NotEmpty(t, ref, "the first firing cites its stored verdict")
	require.Equal(t, Verdict{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      "deny",
		Reason: "`ls jobs >f` keeps console text, which is not a format anything should parse." +
			"\nnext:\n  magus ls jobs -o json --tee f" +
			verdictRefLine + "magus query output " + ref,
		Rule:      string(denyRuleOutputRedirect),
		Lease:     v.Lease, // follows the environment the test ran in
		LeaseFrom: v.LeaseFrom,
		Next:      []hint.Next{want},
	}, v, "one line plus the next and the verdict ref")

	assert.Equal(t, "deny-output-redirect", servedNextPreauthorizes(hint.NewGate(cacheDir, ""), want.Run))
	assert.NotEqual(t, "deny", Judge(ctx, deps, Request{Input: want.Run}).Decision, "the served line passes")
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
	require.Equal(t, Verdict{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      "deny",
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
// or one around it. Measured 2026-09-29: 22,081 recorded verdicts carried no rule name, and
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
			lit, ok := as.Rhs[i].(*ast.BasicLit)
			if verdictField(e, "Decision") && ok && lit.Kind == token.STRING && lit.Value != `"pass"` {
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

// TestJudgeClosesStdinForAShellCommand: an agent's command inherits a stdin nobody writes
// to, and a reader of it waits forever. Where the wiring can hand the host a rewrite, the
// guard closes it, says so once per session, and records that it did.
func TestJudgeClosesStdinForAShellCommand(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	dir := t.TempDir()
	ctx := WithLocation(t.Context(), dir, "/repo", "")
	judge := func(input string, rewrites bool) Verdict {
		return Judge(ctx, strict(Dependencies{}), Request{Input: input, Host: "test-host", Session: "s1", RewritesInput: rewrites})
	}

	first := judge("ls -la", true)
	assert.Equal(t, Verdict{
		SchemaVersion:  agent.GuardSchemaVersion,
		Decision:       "advise",
		Context:        stdinClosedNotice,
		Rule:           string(advisoryStdinClosed),
		UpdatedCommand: "exec </dev/null; ls -la",
	}, first, "the first rewrite of a session says so")

	again := judge("ls", true)
	assert.Equal(t, Verdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "pass", UpdatedCommand: "exec </dev/null; ls"}, again,
		"once per session, then the rewrite alone")

	assert.Empty(t, judge("exec </dev/null; ls", true).UpdatedCommand, "a command that closes stdin itself is never prefixed twice")
	assert.Empty(t, judge("ls", false).UpdatedCommand, "a wiring that cannot hand the host a rewrite gets none")

	denied := judge("git add -A", true)
	assert.Equal(t, "deny", denied.Decision)
	assert.Empty(t, denied.UpdatedCommand, "a refused call is never rewritten")

	events := trailEvents(t, dir, trail.KindAgentCommand)
	require.Len(t, events, 5)
	var closed []bool
	for _, e := range events {
		raw, err := trail.ReadBlob(dir, e.ResponseRef)
		require.NoError(t, err)
		var resp struct {
			StdinClosed bool `json:"stdin_closed"`
		}
		require.NoError(t, json.Unmarshal(raw, &resp))
		closed = append(closed, resp.StdinClosed)
	}
	assert.Equal(t, []bool{true, true, false, false, false}, closed, "the trail records each rewrite and nothing else")
}

// stdin-closed honors every decision a workspace may set: advise says so once a session,
// off keeps the rewrite and drops the notice, and deny refuses every line that leaves
// stdin open, without rewriting it.
func TestJudgeStdinClosedHonorsEveryDecision(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	refused := Verdict{Decision: "deny", Rule: string(advisoryStdinClosed)}
	for _, tt := range []struct {
		decision     builtin.Decision
		first, again Verdict
	}{
		{builtin.Advise,
			Verdict{Decision: "advise", Context: stdinClosedNotice, Rule: string(advisoryStdinClosed), UpdatedCommand: "exec </dev/null; ls -la"},
			Verdict{Decision: "pass", UpdatedCommand: "exec </dev/null; ls"}},
		{builtin.Off,
			Verdict{Decision: "pass", UpdatedCommand: "exec </dev/null; ls -la"},
			Verdict{Decision: "pass", UpdatedCommand: "exec </dev/null; ls"}},
		{builtin.Deny, refused, refused},
	} {
		t.Run(string(tt.decision), func(t *testing.T) {
			ctx := WithLocation(t.Context(), t.TempDir(), "/repo", "")
			deps := withSetting(string(advisoryStdinClosed), tt.decision)
			judge := func(input string) Verdict {
				v := Judge(ctx, deps, Request{Input: input, Host: "test-host", Session: "s1", RewritesInput: true})
				return Verdict{Decision: v.Decision, Context: v.Context, Rule: v.Rule, UpdatedCommand: v.UpdatedCommand}
			}

			assert.Equal(t, tt.first, judge("ls -la"))
			assert.Equal(t, tt.again, judge("ls"))
		})
	}
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
			{builtin.Advise, "advise", "pass"},
			{builtin.Off, "pass", "pass"},
			{builtin.Deny, "deny", "deny"},
		} {
			t.Run(string(notice.rule)+"/"+string(tt.decision), func(t *testing.T) {
				ctx, _ := fleetFixture(t, types.Job{ID: "finished", State: types.StatePass})
				t.Setenv(trail.EnvBaggage, "")
				deps := withSetting(string(notice.rule), tt.decision)
				judge := func() Verdict {
					v := Judge(ctx, deps, Request{Input: "ls", Lease: notice.lease, Session: "s1"})
					return Verdict{Decision: v.Decision, Rule: v.Rule}
				}
				want := func(decision string) Verdict {
					if decision == "pass" {
						return Verdict{Decision: "pass"}
					}
					return Verdict{Decision: decision, Rule: string(notice.rule)}
				}

				assert.Equal(t, want(tt.first), judge())
				assert.Equal(t, want(tt.again), judge())
			})
		}
	}
}

// TestClosedStdinLineRunsAsWritten runs each rewritten line in bash with a stdin that has
// bytes waiting, which is the case the prefix exists for: every reader must see
// end-of-file, and the line must otherwise behave exactly as the agent wrote it.
func TestClosedStdinLineRunsAsWritten(t *testing.T) {
	t.Parallel()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	for _, tc := range []struct {
		name, line, out string
		code            int
	}{
		{"a reader of stdin", `read -r x; echo "[$x] $?"`, "[] 1\n", 0},
		{"a filter with no operand", "wc -c", "0\n", 0},
		{"a heredoc", "cat <<'EOF'\nhello\nEOF", "hello\n", 0},
		{"a pipe", "echo piped | cat", "piped\n", 0},
		{"an explicit redirect", "cat < /dev/null; echo done", "done\n", 0},
		{"a trailing comment", "echo hi # read this", "hi\n", 0},
		{"a trailing ampersand", "echo bg &", "bg\n", 0},
		{"several lines", "echo a\necho b", "a\nb\n", 0},
		{"an exit status", "echo out; exit 3", "out\n", 3},
	} {
		line, closed := closeStdin(tc.line)
		require.True(t, closed, tc.name)
		again, rewrapped := closeStdin(line)
		assert.False(t, rewrapped, "%s: a wrapped line is never wrapped twice", tc.name)
		assert.Equal(t, line, again, tc.name)

		cmd := exec.Command(bash, "-c", line)
		cmd.Stdin = strings.NewReader("LEAKED\n")
		out, err := cmd.Output()
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			require.NoError(t, err, tc.name)
		}
		assert.Equal(t, tc.out, strings.TrimLeft(string(out), " "), "%s: %q", tc.name, line)
		assert.Equal(t, tc.code, code, "%s: the line's own exit status", tc.name)
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
		v := Evaluate(testDependencies(), command)
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
	root, err := magus.FindRoot("")
	require.NoError(b, err)
	cacheDir := b.TempDir()
	at := location{cacheDir: cacheDir, workspace: root, dir: root}
	store := job.NewStore(job.Location{CacheDir: cacheDir, Root: root})
	states := []types.JobState{types.StatePass, types.StatePass, types.StateFail, types.StateNoReturn, types.StateDeclared}
	for i := range 300 {
		row := types.Job{
			ID: fmt.Sprintf("root-%d/job-%d", i%20, i), Parent: fmt.Sprintf("root-%d", i%20),
			State: states[i%len(states)], WritePaths: []string{"internal/guard/guard.go"},
		}
		_, err := store.Update(b.Context(), row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(b, err)
	}
	path, err := store.Path()
	require.NoError(b, err)
	old := time.Now().Add(-time.Hour)
	require.NoError(b, os.Chtimes(path, old, old))
	ctx := b.Context()
	b.Run("read=list", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = store.List()
		}
	})
	b.Run("read=memo", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			withJobStoreRows(ctx, at)
		}
	})
}

// A store edited between two calls in one process is read again, however recent the
// memo, and a memo is only ever the answer for the file it was taken from.
func TestJobStoreRowsMemo(t *testing.T) {
	testkit.Isolate(t)
	root, cacheDir := t.TempDir(), t.TempDir()
	at := location{cacheDir: cacheDir, workspace: root, dir: root}
	store := job.NewStore(job.Location{CacheDir: cacheDir, Root: root})
	path, err := store.Path()
	require.NoError(t, err)
	memoPath := filepath.Join(filepath.Dir(path), jobRowsMemoFile)

	put := func(row types.Job) {
		t.Helper()
		_, err := store.Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
		require.NoError(t, err)
	}
	backdate := func() {
		t.Helper()
		old := time.Now().Add(-time.Hour)
		require.NoError(t, os.Chtimes(path, old, old))
	}
	rowsOf := func() []types.Job {
		t.Helper()
		snap, ok := job.SnapshotFromContext(withJobStoreRows(t.Context(), at))
		require.True(t, ok)
		require.NoError(t, snap.Err)
		return snap.Rows
	}
	ids := func(rows []types.Job) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	// plant replaces the memo's rows, keeping the stamp, so a read that used the memo is
	// visible in what it returns.
	plant := func(when time.Time, rows ...types.Job) {
		t.Helper()
		raw, err := os.ReadFile(memoPath)
		require.NoError(t, err)
		var memo jobRowsMemo
		require.NoError(t, json.Unmarshal(raw, &memo))
		memo.Rows, memo.AtNS = rows, when.UnixNano()
		raw, err = json.Marshal(memo)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(memoPath, raw, 0o644))
	}

	put(types.Job{ID: "a", State: types.StateDeclared, Parent: "", WritePaths: []string{"x.go"}, Model: "sonnet"})
	fresh := rowsOf()
	assert.NoFileExists(t, memoPath, "a store written this second is not memoized")

	backdate()
	first := rowsOf()
	assert.Equal(t, fresh, first)
	require.FileExists(t, memoPath)
	assert.Equal(t, first, rowsOf(), "the memo returns the rows the store did")

	plant(time.Now(), types.Job{ID: "memo-only"})
	assert.Equal(t, []string{"memo-only"}, ids(rowsOf()), "an unchanged store answers from the memo")

	put(types.Job{ID: "b", State: types.StateDeclared})
	assert.Equal(t, []string{"a", "b"}, ids(rowsOf()), "an edited store is read again")

	backdate()
	rowsOf()
	plant(time.Now().Add(-time.Minute), types.Job{ID: "memo-only"})
	assert.Equal(t, []string{"a", "b"}, ids(rowsOf()), "a memo past its TTL is not trusted")
}

// recoverableRules are the compiled rules that refused before they advised by default.
var recoverableRules = []string{
	"architecture-unbriefed", "brief-command", "busy-wait", "buzz-unbriefed", "chained-run", "exit-status-echo",
	"filter-without-input", "grep-reader", "interpreter-rewrite", "magus-timeout",
	"output-pipe", "output-redirect", "process-poll", "raw-tool", "read-navigation",
	"scripted-rewrite", "search-translation", "sed-in-place", "sibling-checkout",
	"spawn-unbriefed", "stage-all", "symbol-search", "throwaway-copy", "unknown-env",
}

// strict returns deps with every refusing rule set to deny, so a test pins a denial a
// workspace has to ask for. Advisories keep their defaults: raising them would turn the
// advisory rows of the same tables into denies.
func strict(deps Dependencies) Dependencies {
	deps.Builtins = make(map[string]builtin.Setting)
	for name, d := range builtin.Defaults() {
		deps.Builtins[name] = builtin.Setting{Decision: d}
	}
	for _, name := range recoverableRules {
		deps.Builtins[name] = builtin.Setting{Decision: builtin.Deny}
	}
	return deps
}

func TestStrictDeniesEveryRecoverableRule(t *testing.T) {
	t.Parallel()
	defaults := builtin.Defaults()
	deps := strict(Dependencies{})

	for _, name := range recoverableRules {
		require.Contains(t, defaults, name, "a recoverable rule the table does not carry")
		assert.Equal(t, builtin.Advise, defaults[name], name)
		assert.Equal(t, builtin.Deny, deps.Builtins[name].Decision, name)
	}
	assert.Len(t, deps.Builtins, len(defaults))

	assert.Empty(t, Evaluate(Dependencies{}, sedInPlace).Deny, "the default advises")
	assert.Equal(t, denyRuleSedInPlace, Evaluate(deps, sedInPlace).Rule.Name)
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

	assert.True(t, deps.grade(ShellVerdict{Deny: "x", Rule: denyRule{Name: denyRuleSedInPlace}}).demoted, "nil Builtins grades by the defaults")
	whole := ShellVerdict{Deny: "x", Rule: denyRule{Name: denyRuleWholeTree}}
	assert.Equal(t, whole, deps.grade(whole))
	uncatalogued := ShellVerdict{Deny: "x", Rule: denyRule{Name: "not-a-rule"}}
	assert.Equal(t, uncatalogued, deps.grade(uncatalogued), "a rule the table does not carry keeps its decision")
}

func TestEvaluateGradesEachSite(t *testing.T) {
	t.Parallel()

	advised := Evaluate(Dependencies{}, sedInPlace)
	assert.Empty(t, advised.Deny)
	assert.True(t, advised.demoted)
	assert.Equal(t, hint.MarkerKind(denyRuleSedInPlace), advised.Kind)

	assert.Equal(t, denyRuleSedInPlace, Evaluate(withSetting(string(denyRuleSedInPlace), builtin.Deny), sedInPlace).Rule.Name)
	assert.Equal(t, ShellVerdict{}, Evaluate(withSetting(string(denyRuleSedInPlace), builtin.Off), sedInPlace))
}

func TestDemotedDenyDoesNotHideALaterDeny(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		sedInPlace + " && git reset --hard",
		"git add -A && git reset --hard",
		"git add -A && git stash",
	} {
		v := Evaluate(Dependencies{}, command)
		assert.Equal(t, denyRuleWholeTree, v.Rule.Name, command)
		assert.NotEmpty(t, v.Deny, command)
	}
}

func TestDemotedDenyOutranksAdvisories(t *testing.T) {
	t.Parallel()

	v := Evaluate(Dependencies{}, sedInPlace+" && git commit -m x")
	assert.True(t, v.demoted, "the held advice outranks the commit advisory")
	assert.Equal(t, hint.MarkerKind(denyRuleSedInPlace), v.Kind)

	off := Evaluate(withSetting(string(denyRuleSedInPlace), builtin.Off), sedInPlace+" && git commit -m x")
	assert.Equal(t, advisoryStageClassify, off.Kind, "an off rule leaves the line to the advisory")
}

func TestDemotedDenyKeepsThePushGate(t *testing.T) {
	t.Parallel()

	v := Evaluate(Dependencies{}, sedInPlace+" && git push")
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

	got := Dependencies{}.rankGraded(advisory, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout)
	assert.True(t, got.demoted, "a demoted sibling checkout outranks an advisory")
	assert.Equal(t, deny, Dependencies{}.rankGraded(deny, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout))

	strictDeps := withSetting(string(denyRuleSiblingCheckout), builtin.Deny)
	assert.Equal(t, denyRuleSiblingCheckout, strictDeps.rankGraded(advisory, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout).Rule.Name)
	assert.Equal(t, advisory, withSetting(string(denyRuleSiblingCheckout), builtin.Off).rankGraded(advisory, denyRuleSiblingCheckout, "relocated", rankSiblingCheckout))
}
