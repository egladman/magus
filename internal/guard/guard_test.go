package guard

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	// Blank-imported so its init installs the spell registry's ensure hook, exactly as
	// cmd/magus/packs_interp.go does for the real binary: without it,
	// project.DefaultSpellRegistry().All() in testDependencies below runs against a registry
	// nothing ever populated, and every raw-tool test would match against an empty
	// catalog. See internal/interp/bindings/spell.go's init.
	"github.com/egladman/magus/internal/agent"
	_ "github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

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
	assert.Equal(t, "magus run ci | tail", req.Value)
	assert.False(t, req.IsPath)
	assert.Equal(t, "s1", req.Who.Session)
	assert.Equal(t, "PreToolUse", req.Who.Event)

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

// TestDecodeHookEnvelopeReadsEveryWritePathSpelling: the path surface used to see
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
	req, ok := decodeHookEnvelope(`{"tool_name":"mcp__magus__magus_job","tool_input":{"op":123,"id":"a/b"}}`)
	require.True(t, ok)
	assert.Equal(t, "magus_job op=123 id=a/b", req.Value)
}

// TestGuardGradesTwoSessionsInOneCheckoutSeparately is the enforcement half of the
// per-session binding: each session's own marker decides which write paths its writes are graded
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
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir

	require.NoError(t, job.Checkout{CacheDir: cacheDir, Session: "session-one"}.Bind(one.ID))
	require.NoError(t, job.Checkout{CacheDir: cacheDir, Session: "session-two"}.Bind(two.ID))

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
	assert.Contains(t, got.Reason, "magus agent harness apply")
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

// policyRule matches a rule's line in tools/policy/guard.buzz's header: `//   name (`.
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
}

func bash(command string) map[string]any {
	return map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": command}}
}

// The Buzz tests in tools/policy/guard.buzz pin each rule against argvs it builds by hand.
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
		{rule: "change-role-spawn-not-isolated", name: "a feat worker sharing the checkout", input: map[string]any{
			"tool_name": "Agent", "tool_input": map[string]any{"description": "root/feat footprint", "prompt": "Build it.", "model": "sonnet"},
		}, decision: "deny", reason: `isolation: "worktree"`},
		{rule: "change-role-spawn-not-isolated", name: "a feat worker in its own worktree", input: map[string]any{
			"tool_name": "Agent", "tool_input": map[string]any{"description": "root/feat footprint", "prompt": "Build it.", "model": "sonnet", "isolation": "worktree"},
		}, decision: "pass"},
	}

	covered := map[string]bool{}
	for _, tc := range cases {
		covered[tc.rule] = true
		t.Run(tc.rule+"/"+tc.name, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
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

	// changelog-unreleased-edit reads the checkout it lands in, so it gets one of its own.
	covered["changelog-unreleased-edit"] = true
	t.Run("changelog-unreleased-edit", func(t *testing.T) {
		ctx, ws, _ := writeFixture(t)
		changelog := filepath.Join(ws, "CHANGELOG.md")
		require.NoError(t, os.WriteFile(changelog, []byte("# Changelog\n\n## [Unreleased]\n\n### Added\n\n- one\n\n## [0.4.0]\n\n- old\n"), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(ws, "changes", "unreleased"), 0o755))
		edit := func(oldText, newText string) Verdict {
			return Judge(ctx, Dependencies{WriteRule: m.WriteRule()}, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
				"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Edit",
				"tool_input": map[string]any{"file_path": changelog, "old_string": oldText, "new_string": newText},
			})})
		}
		v := edit("- one\n", "- one\n- two\n")
		assert.Equal(t, "deny", v.Decision)
		assert.Equal(t, workspaceWriteRule, v.Rule)
		assert.Contains(t, v.Reason, "changes/unreleased/")
		assert.NotEqual(t, "deny", edit("- old\n", "- old, fixed\n").Decision, "a released section's fix passes")
	})

	source, err := os.ReadFile(filepath.Join(root, "tools", "policy", "guard.buzz"))
	require.NoError(t, err)
	listed := policyRule.FindAllStringSubmatch(string(source), -1)
	require.NotEmpty(t, listed, "the header still lists its rules as `//   name (`")
	for _, rule := range listed {
		assert.True(t, covered[rule[1]], "%s is in the policy's header with no real-input case here", rule[1])
	}
}
