package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/codec"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	trailtypes "github.com/egladman/magus/libs/safetyscissors/trail"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// shellStdin runs `magus shell` with its input on stdin, which is the shape a host's
// pre-tool-use hook uses. A person passes the command as an operand instead; both reach
// the same evaluator, and the operand path has its own cases below.
//
// It judges by the built-in rules alone: these tests run inside magus's own checkout, whose
// Buzz policy is not what they are about. The cases that load a workspace's rules stand up
// their own checkout.
//
// It judges with only the flags in args. The display flags are process-global and parse
// with their current values as defaults, so a -q that another test's command left set
// would discard the verdict and leave nothing but the exit status.
func shellStdin(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	savedRoot, savedFlags := guardRoot, global
	guardRoot = func(string) (string, error) { return "", errors.New("no workspace rules in this test") }
	global = globalFlags{}
	defer func() { guardRoot, global = savedRoot, savedFlags }()
	return shellCommandWithErrorWriter(ctx, in, out, os.Stderr, args)
}

// A -q that an earlier in-process command left in the process-global flags made a hook
// case print "" for a deny, in a run narrowed so that no reset ran between them.
func TestShellStdinJudgesWithOnlyTheFlagsItIsGiven(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	saved := global
	t.Cleanup(func() { global = saved })
	global.quiet = true
	ctx := guard.WithLocation(context.Background(), t.TempDir(), "/repo/magus", "")

	var out bytes.Buffer
	err := shellStdin(ctx, strings.NewReader("git stash"), &out, []string{"-o", "name"})

	var silent reportedExit
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, "deny\n", out.String())
	assert.True(t, global.quiet, "the caller's flags are back once the call returns")
}

// TestShellJudgesAnOperandTheSameAsStdin is the claim the command is named for: a person
// typing the command and a host piping it get ONE verdict from one evaluator, not two
// code paths that agree today.
//
// The operand wins over stdin rather than being rejected, which is the reverse of what
// the hook command did. Rejecting it was right while stdin was the only contract; once a
// person is the other caller, a terminal's stdin is a keyboard, and reading it would hang
// on a command that was already supplied.
func TestShellJudgesAnOperandTheSameAsStdin(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	ctx := guard.WithLocation(context.Background(), t.TempDir(), "/repo/magus", "")

	var fromStdin, fromOperand strings.Builder
	require.Error(t, shellStdin(ctx, strings.NewReader("git stash"), &fromStdin, []string{"-o", "name"}))
	require.Error(t, shellCommandWithErrorWriter(ctx, strings.NewReader(""), &fromOperand, os.Stderr,
		[]string{"git stash", "-o", "name"}))

	assert.Equal(t, "deny\n", fromStdin.String())
	assert.Equal(t, fromStdin.String(), fromOperand.String(), "one evaluator, whichever door the command arrived through")
}

// TestWriteGuardVerdictNameGoesToTheHandedWriterAndTheTee: -o name was routed through the
// process stdout, so a caller that handed in its own writer read an empty verdict.
func TestWriteGuardVerdictNameGoesToTheHandedWriterAndTheTee(t *testing.T) {
	t.Setenv(trail.EnvBaggage, "")
	ctx := guard.WithLocation(context.Background(), t.TempDir(), "/repo/magus", "")
	tee := filepath.Join(t.TempDir(), "verdict")

	var out bytes.Buffer
	require.Error(t, shellStdin(ctx, strings.NewReader("git stash"), &out, []string{"-o", "name", "--tee", tee}))

	assert.Equal(t, "deny\n", out.String())
	teed, err := os.ReadFile(tee)
	require.NoError(t, err)
	assert.Equal(t, "deny\n", string(teed))
}

// The wiring, end to end through the hook: a write that reaches a verdict leaves the
// project it landed in recorded for the session, whatever that verdict was. Nothing
// below this line fires the advisory, and that is the point: the set has to be filled
// by every write, not only by the ones this rule speaks about.
//
// The workspace comes from the same load, kept for the process, that the rule itself uses, rather than
// from a root this test resolves on its own. That load is a package-level sync.Once, so
// whichever test in this binary reaches it first pins it; asserting against a root this
// test picked would pass or fail on test ORDER, since the rule is silent whenever the
// loaded workspace is not the one the host reported.
func TestHookCmdRecordsTheProjectAWriteTouched(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	resetOpenedWorkspaces(t)
	t.Setenv(trail.EnvBaggage, "")
	ws, err := inspectWorkspace(t.Context(), "")
	require.NoError(t, err)

	base := t.TempDir()
	ctx := guard.WithLocation(t.Context(), base, ws.Root(), "")
	global = globalFlags{}
	var out strings.Builder
	require.NoError(t, shellStdin(ctx, strings.NewReader(filepath.Join(ws.Root(), "drift-fixture.txt")),
		&out, []string{"--path", "--session", "session-1", "--agent-name", "claude-code"}))

	assert.Equal(t, []string{"."}, touchedProjects(hint.NewGate(base, guard.FactsKey("claude-code", "session-1"))),
		"a workspace-root file belongs to the root project, whatever else this workspace declares")
}

// TestHookEnvelopeCwdLocatesTheWorkersCheckout pins the channel a host that runs its hooks
// somewhere else reaches the worker's marker through: the envelope's cwd names the worker's
// checkout, and the lease bound there scopes the verdict, whatever the hook process's own
// directory is.
func TestHookEnvelopeCwdLocatesTheWorkersCheckout(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	global = globalFlags{}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(""), 0o644))
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	worker := narrowLease()
	worker.Parent = "harness"
	_, err = job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}).
		Update(t.Context(), worker.ID, func(cur *jobtypes.Job) { *cur = worker })
	require.NoError(t, err)
	bindCheckout(t, cacheDir, worker.ID)

	// No session_id: the checkout's record answers only a caller its host names no session for.
	envelope := fmt.Sprintf(`{"hook_event_name":"PreToolUse","cwd":%q,"tool_input":{"command":"git commit -m done"}}`, root)
	var out bytes.Buffer
	err = shellStdin(context.Background(), strings.NewReader(envelope), &out, []string{"-o", "name"})
	var silent reportedExit
	require.ErrorAs(t, err, &silent, "the worker lease bound in the envelope's checkout must deny the commit")
	assert.Equal(t, "deny\n", out.String())

	events, err := trailtypes.ReadRecent(t.Context(), cacheDir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1, "the observation lands in the worker's trail, not the hook's cwd")
	assert.Equal(t, worker.ID, events[0].Lease)
}

// committedDenyRule is a git repository whose committed root magusfile is denyRules.
func committedDenyRule(t *testing.T) *testkit.Repo {
	t.Helper()
	return testkit.NewRepository(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyRules})
}

// A committed spawn rule still answers however the working tree leaves the root magusfile:
// a syntax error, the file deleted, or the other magusfile form added beside it.
func TestLoadApprovedSpawnRuleSurvivesABrokenWorkingTree(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, root string)
	}{
		{"a syntax error", func(t *testing.T, root string) {
			require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n\nmagus\\guard.spawn(\n"), 0o644))
		}},
		{"the magusfile deleted", func(t *testing.T, root string) {
			require.NoError(t, os.Remove(filepath.Join(root, "magusfile.buzz")))
		}},
		{"a magusfiles directory added beside it", func(t *testing.T, root string) {
			require.NoError(t, os.MkdirAll(filepath.Join(root, "magusfiles"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "magusfiles", "a.buzz"), []byte("import \"magus\";\n"), 0o644))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := committedDenyRule(t).Root()
			tc.mutate(t, root)

			rule, err := guard.LoadApprovedSpawnRule(t.Context(), root)
			require.NoError(t, err)
			require.NotNil(t, rule)
			got, err := rule(t.Context(), types.SpawnRequest{Kind: types.SpawnKindSpawn, Role: types.AgentRoleRoot}, hint.NewGate(t.TempDir(), "claude-code/s1"))
			require.NoError(t, err)
			assert.Equal(t, verdict.Verdict{Decision: verdict.Deny, Reason: "Name a model."}, got)

			command, err := guard.LoadApprovedCommandRule(t.Context(), root)
			require.NoError(t, err)
			require.NotNil(t, command)
			got, err = command(t.Context(), types.CommandRequest{Command: "ls", Role: types.AgentRoleRoot}, hint.NewGate(t.TempDir(), "claude-code/s1"))
			require.NoError(t, err)
			assert.Equal(t, verdict.Verdict{Decision: verdict.Deny, Reason: "Not in this repository."}, got)
		})
	}
}

// A command loaded with --root from inside another checkout looks symbols and graph ids up
// in the guard index of the workspace it loaded, not the cwd's.
func TestGuardIndexAnswersFromTheLoadedRoot(t *testing.T) {
	loaded := rootElsewhere(t)
	cacheDir, err := magus.ResolveCacheDir(loaded, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{
		{ID: "symbol:x loadedOnly().", Kind: types.KindSymbol, Label: "loadedOnly"},
		{ID: "target:.:build", Kind: types.KindTarget, Label: "build"},
	}, nil)
	require.NoError(t, knowledge.WriteGuardIndex(cacheDir, loaded, g, true, knowledge.GuardCheckout{}))

	deps := guardDependencies(t.Context(), loaded)
	defined, _ := deps.SymbolDefined("loadedOnly")
	assert.True(t, defined)
	ids, ok := deps.IndexedIDs(t.Context(), types.KindTarget)
	assert.True(t, ok)
	assert.Equal(t, []string{"target:.:build"}, ids)
}

// The stale-graph advice inspects the loaded workspace. Inspecting the cwd's after loading
// another panics.
func TestGuardDependenciesStaleAdviceInspectsTheLoadedRoot(t *testing.T) {
	loaded := rootElsewhere(t)
	_, err := inspectWorkspace(t.Context(), loaded)
	require.NoError(t, err)

	deps := guardDependencies(t.Context(), loaded)
	require.NotPanics(t, func() { _ = deps.GraphStaleAdvice(t.Context()) })
}

// The index cause is diagnosed from the loaded checkout's sync record.
func TestIndexCauseNamesTheLoadedRoot(t *testing.T) {
	loaded := rootElsewhere(t)
	dir, err := knowledgeStoreDir(loaded)
	require.NoError(t, err)
	require.NoError(t, maintenance.RecordSyncRequest(dir, maintenance.SyncRequest{
		At: time.Now(), Outcome: maintenance.SyncRefused, Detail: "refused in the loaded checkout",
	}))

	assert.Contains(t, indexCause(t.Context(), loaded).Why, "refused in the loaded checkout")
}

// spawnEnvelope is a Claude Code spawn as its hook wiring hands it to `magus shell`.
const spawnEnvelope = `{"session_id":"8f2c6a1e","hook_event_name":"PreToolUse","tool_name":"Agent",` +
	`"tool_input":{"description":"implement adr 0002","prompt":"Implement ADR 0002.","subagent_type":"general-purpose"}}`

// judgeSpawnAt runs one spawn through the hook's own dependencies with the process sitting
// in root, as the shipped hook does.
func judgeSpawnAt(t *testing.T, root string) verdict.Judgment {
	t.Helper()
	testkit.Isolate(t)
	t.Chdir(root)
	ctx := guard.WithLocation(t.Context(), t.TempDir(), root, root)
	return guard.Judge(ctx, guardDependencies(ctx, ""), guard.Request{Input: spawnEnvelope, Host: "claude-code"})
}

// resetOpenedWorkspaces gives a test its own workspace load and restores the
// process-wide state afterwards.
func resetOpenedWorkspaces(t *testing.T) {
	t.Helper()
	savedVersion := version
	resetStartupSingletons()
	t.Cleanup(func() {
		resetStartupSingletons()
		version = savedVersion
	})
}

// The hook reads the root magusfile alone, so a working tree the full load would refuse,
// here for a version floor this binary is below, still has its rules applied.
func TestSpawnRuleSurvivesAVersionFloor(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	root := committedDenyRule(t).Root()
	resetOpenedWorkspaces(t)
	version = "v0.5.0"
	globalCfg.RequiredVersion = ">= 99.0.0"

	v := judgeSpawnAt(t, root)
	assert.Equal(t, verdict.Judgment{
		APIVersion: v.APIVersion,
		Decision:   verdict.Deny,
		Reason:     "Name a model.",
		Rule:       v.Rule, // the rule's catalogued name is pinned by the guard's own tests
		Lease:      v.Lease,
		LeaseFrom:  v.LeaseFrom,
		NextSteps:  v.NextSteps,
	}, v)
}

// A root magusfile that does not load is a load failure, not a workspace with no rules,
// so the committed rules still apply to a spawn and to a command.
func TestUnloadableWorkingTreeStillRunsTheCommittedRules(t *testing.T) {
	root := committedDenyRule(t).Root()
	resetOpenedWorkspaces(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n\nmagus\\guard.spawn(\n"), 0o644))
	t.Chdir(root)
	_, err := loadGuardRules(t.Context(), "")
	require.Error(t, err)

	v := judgeSpawnAt(t, root)
	assert.Equal(t, verdict.Deny, v.Decision)
	assert.True(t, strings.HasPrefix(v.Reason, "Name a model.\n\n"), v.Reason)
	assert.Contains(t, v.Reason, "The working tree's magus\\guard.spawn rule judged nothing: the magusfile failed to load")

	ctx := guard.WithLocation(t.Context(), t.TempDir(), root, root)
	v = guard.Judge(ctx, guardDependencies(ctx, ""), guard.Request{Input: "ls -la", Host: "claude-code"})
	assert.Contains(t, v.Reason, "Not in this repository.")
	assert.Equal(t, verdict.Judgment{
		APIVersion: v.APIVersion,
		Decision:   verdict.Deny,
		Reason:     v.Reason, // checked above
		Rule:       "workspace:command",
		Lease:      v.Lease,
		LeaseFrom:  v.LeaseFrom,
		NextSteps:  v.NextSteps,
	}, v)
}

// The working tree may point its magusfile at another policy file and loosen the one the
// committed magusfile imports in the same edit. Its own load never reads that file, and the
// committed load still has to read it as committed, not as edited.
func TestApprovedRulesReadAnImportTheWorkingTreeDropped(t *testing.T) {
	rule := func(ns, verdict string) string {
		return fmt.Sprintf(`namespace %s;

import "magus";

export fun judge(req: CommandRequest) > GuardVerdict {
    return %s;
}
`, ns, verdict)
	}
	importing := func(name string) string {
		return fmt.Sprintf(`import "magus";
import "./policy/%s" as p;
magus\guard.command(p\judge);
`, name)
	}
	repo := testkit.NewRepository(t, map[string]string{
		"magus.yaml":     "{}\n",
		"policy/a.buzz":  rule("a", `magus\guard.deny("Not in this repository.")`),
		"policy/b.buzz":  rule("b", `magus\guard.defer()`),
		"magusfile.buzz": importing("a"),
	})
	root, write := repo.Root(), repo.Write

	write("magusfile.buzz", importing("b"))
	write("policy/a.buzz", rule("a", `magus\guard.defer()`))
	resetOpenedWorkspaces(t)
	t.Chdir(root)

	rules, err := loadGuardRules(t.Context(), "")
	require.NoError(t, err)
	approved, err := rules.ApprovedCommandRule(t.Context())
	require.NoError(t, err)
	require.NotNil(t, approved)
	got, err := approved(t.Context(), types.CommandRequest{Command: "ls"}, hint.NewGate(t.TempDir(), "s"))
	require.NoError(t, err)
	assert.Equal(t, verdict.Deny, got.Decision)
}

// A write to the root magusfile whose judgment ran out of time, its rules loaded through the
// hook's own dependencies, goes through while no policy change is pending, so a slow
// committed rule can be fixed, and is refused once a change is pending.
func TestLapsedPolicyWriteIsSparedOnlyWithNothingPending(t *testing.T) {
	testkit.Isolate(t)
	const allow = "import \"magus\";\n\nmagus\\guard.write(fun (req: WriteRequest) > GuardVerdict {\n    return magus\\guard.defer();\n});\n"
	repo := testkit.NewRepository(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": allow})
	root := repo.Root()
	resetOpenedWorkspaces(t)
	t.Chdir(root)
	judge := func() verdict.Judgment {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()
		ctx = guard.WithLocation(ctx, t.TempDir(), root, root)
		return guard.Judge(ctx, guardDependencies(ctx, ""), guard.Request{
			Input: filepath.Join(root, "magusfile.buzz"), IsPath: true, Host: "claude-code", Session: "lapsed-policy-write",
		})
	}

	v := judge()
	assert.Equal(t, verdict.Advise, v.Decision, v.Reason)
	assert.Contains(t, v.Context, "lets it through, because the guard loads this file itself")

	repo.Write("magusfile.buzz", allow+"// pending\n")
	v = judge()
	assert.Equal(t, verdict.Deny, v.Decision)
	assert.Contains(t, v.Reason, "magus ran out of time judging this file write")
}

// The committed side is loaded only while a file the root load read differs from it:
// an uncommitted edit anywhere else changes nothing either side registers.
func TestApprovedRulesLoadOnlyForAPendingPolicySource(t *testing.T) {
	root := committedDenyRule(t).Root()
	resetOpenedWorkspaces(t)
	t.Chdir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "spells", "other"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "spells", "other", "spell.buzz"), []byte("// untracked\n"), 0o644))

	rules, err := loadGuardRules(t.Context(), "")
	require.NoError(t, err)
	approved, err := rules.ApprovedCommandRule(t.Context())
	require.NoError(t, err)
	assert.Nil(t, approved, "no policy source is pending, so the working tree's rule is the approved one")

	// Loosen the rule in the working tree: now the committed deny must still answer.
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n"), 0o644))
	rules, err = loadGuardRules(t.Context(), "")
	require.NoError(t, err)
	assert.Nil(t, rules.CommandRule())
	approved, err = rules.ApprovedCommandRule(t.Context())
	require.NoError(t, err)
	require.NotNil(t, approved)
	got, err := approved(t.Context(), types.CommandRequest{Command: "ls"}, hint.NewGate(t.TempDir(), "s"))
	require.NoError(t, err)
	assert.Equal(t, verdict.Deny, got.Decision)
}

// A repeated deny cites a grd ref the reader resolves with `query output` in the checkout
// the call ran in. A host may run its hook from the primary checkout while a worker sits in
// a linked worktree, so the envelope's cwd, not the hook process's, decides the store.
func TestRepeatDenyInALinkedWorktreeIsReadableThere(t *testing.T) {
	testkit.Isolate(t)
	resetOpenedWorkspaces(t)
	repo := testkit.NewRepository(t, map[string]string{
		"magus.yaml":     "{}\n",
		"magusfile.buzz": "import \"magus\";\nmagus\\guard.builtins({\"output-redirect\": \"deny\"});\n",
	})
	primary := repo.Root()
	worktree := repo.AddWorktree("worker").Root()
	t.Chdir(primary)

	envelope := fmt.Sprintf(`{"session_id":"worktree-verdict","hook_event_name":"PreToolUse","cwd":%q,`+
		`"tool_name":"Bash","tool_input":{"command":"magus run build > build.log"}}`, worktree)
	judge := func() verdict.Judgment {
		ctx := t.Context()
		return guard.Judge(ctx, guardDependencies(ctx, ""), guard.Request{Input: envelope, Host: "claude-code"})
	}
	first := judge()
	require.Equal(t, verdict.Deny, first.Decision, first.Reason)
	again := judge()
	ref := regexp.MustCompile(`grd[0-9a-f]{16}`).FindString(again.Reason)
	require.NotEmpty(t, ref, "a repeat deny cites its full verdict: %s", again.Reason)

	out := captureStdout(t, func() {
		require.NoError(t, queryTrailPayload(t.Context(), worktree, ref, output.Options{Format: outputText}))
	})
	assert.Contains(t, out, "see: https://eli.gladman.cc/magus/reference/rules/")

	primaryCache, err := magus.ResolveCacheDir(primary)
	require.NoError(t, err)
	_, err = trailtypes.ReadBlob(primaryCache, ref)
	assert.NoError(t, err, "the primary checkout holds a copy, which every checkout's query output searches")
}

// A guard-rule load that runs past the hook's deadline still stores every chunk it
// compiled, so the hook after it loads warm: a cold cache costs one hook its time, never
// every hook after it.
func TestGuardRuleLoadPastItsDeadlineWarmsTheNextHook(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	testkit.Isolate(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	repo := testkit.NewRepository(t, map[string]string{
		"magus.yaml":      "{}\n",
		"magusfile.buzz":  "import \"magus\";\nimport \"./lib/policy\" as policy;\n\nmagus\\guard.command(policy\\rule);\n",
		"lib/policy.buzz": "import \"magus\";\n\nexport fun rule(req: CommandRequest) > GuardVerdict {\n    return magus\\guard.deny(\"no\");\n}\n",
	})
	chunks := func() []string {
		var out []string
		_ = filepath.WalkDir(filepath.Join(cache, "magus", "buzz-bytecode"), func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() != ".swept" {
				out = append(out, p)
			}
			return nil
		})
		return out
	}
	expired, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	<-expired.Done()

	rules, err := loadGuardRules(expired, repo.Root())
	require.NoError(t, err)
	require.NotNil(t, rules.CommandRule())
	stored := chunks()
	require.Len(t, stored, 3, "the magusfile, its import, and the magusfile's facts")

	_, err = loadGuardRules(t.Context(), repo.Root())
	require.NoError(t, err)
	assert.Equal(t, stored, chunks(), "the next load compiles nothing")
}

// The approved side keeps its compile in its own sealed store, keyed by committed content,
// and never reads or writes the working tree's chunk cache: a pending edit cannot seed what
// the approved rule runs. A resolve that starts past its deadline reads nothing from
// version control and answers no rule, which the guard takes for a lapse rather than for
// "none" (askWorkspaceRules).
func TestApprovedResolveLeavesTheWorkingTreeChunkCacheAlone(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	testkit.Isolate(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	const rule = "import \"magus\";\n\nexport fun rule(req: CommandRequest) > GuardVerdict {\n    return magus\\guard.%s;\n}\n"
	const magusfile = "import \"magus\";\nimport \"./lib/policy\" as policy;\n\nmagus\\guard.command(policy\\rule);\n"
	repo := testkit.NewRepository(t, map[string]string{
		"magus.yaml":      "{}\n",
		"magusfile.buzz":  magusfile,
		"lib/policy.buzz": fmt.Sprintf(rule, `deny("no")`),
	})
	repo.Write("magusfile.buzz", magusfile+"// pending\n")
	repo.Write("lib/policy.buzz", fmt.Sprintf(rule, "defer()"))
	chunks := func() []string {
		var out []string
		_ = filepath.WalkDir(filepath.Join(cache, "magus", "buzz-bytecode"), func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				out = append(out, p)
			}
			return nil
		})
		return out
	}
	rules, err := loadGuardRules(t.Context(), repo.Root())
	require.NoError(t, err)
	worktree := chunks()

	expired, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	<-expired.Done()
	late, err := rules.ApprovedCommandRule(expired)
	require.NoError(t, err)
	assert.Nil(t, late, "past its deadline the resolve reads no approved state")
	assert.Equal(t, worktree, chunks(), "and compiles nothing")

	approved, err := rules.ApprovedCommandRule(t.Context())
	require.NoError(t, err)
	require.NotNil(t, approved)
	got, err := approved(t.Context(), types.CommandRequest{Command: "ls"}, hint.NewGate(t.TempDir(), "s"))
	require.NoError(t, err)
	assert.Equal(t, verdict.Deny, got.Decision, "the committed rule answers")
	assert.Equal(t, worktree, chunks(), "compiling the committed sources stores nothing")
}

// staleConfigSuite pins the guard over a magus.yaml key this build predates
// (MGS1040): it judges the call as a stale binary's rather than exiting.
// The checkout carries another contract than the test binary's.
type staleConfigSuite struct {
	suite.Suite
	root string
}

func TestStaleConfigSuite(t *testing.T) {
	suite.Run(t, new(staleConfigSuite))
}

func (s *staleConfigSuite) SetupTest() {
	ws := testkit.NewWorkspace(s.T())
	ws.Write("go.mod", "module github.com/egladman/magus\n\ngo 1.25\n")
	ws.Write("magusfile.buzz", "import \"magus\";\nmagus\\guard.command(judge);\n")
	ws.Write("magus.yaml", "checkout:\n  bootstrap: hack/dev/bootstrap-checkout.buzz\n")
	ws.Write("internal/commands/testdata/api.lock", "config another.key\n")
	ws.WriteExecutable("magus", "#!/bin/sh\n")
	s.root = ws.Root()
	s.T().Chdir(s.root)
	savedRoot, savedFlags := guardRoot, global
	guardRoot = func(string) (string, error) { return s.root, nil }
	global = globalFlags{}
	s.T().Cleanup(func() { guardRoot, global = savedRoot, savedFlags })
}

func (s *staleConfigSuite) judge(command string) verdict.Judgment {
	failure := types.DiagnosticErrorf(types.UnknownConfigKey,
		"magus.yaml has a key this magus does not know:\n  - checkout  (line 1)")
	ctx := guard.WithStaleConfig(s.T().Context(), failure)
	var out bytes.Buffer
	_ = shellCommandWithErrorWriter(ctx, strings.NewReader(""), &out, io.Discard, []string{"-o", "json", command})
	var v verdict.Judgment
	s.Require().NoError(codec.Unmarshal(out.Bytes(), &v), out.String())
	return v
}

func (s *staleConfigSuite) TestAStateChangeIsRefusedAsUnjudged() {
	v := s.judge("touch notes.txt")
	s.Equal(verdict.Deny, v.Decision)
	s.Contains(v.Reason, "magus guard could not judge this call, so it is refused")
	s.Contains(v.Reason, "hack/dev/bootstrap-checkout.buzz -- --here --verify-ready")
}

func (s *staleConfigSuite) TestAReadIsRefusedToo() {
	v := s.judge("cat README.md")
	s.Equal(verdict.Deny, v.Decision, "no policy loaded to say which reads the workspace guards")
}

func (s *staleConfigSuite) TestStartupHandsAnUnknownKeyToTheGuard() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magus.yaml"), []byte("a_section_from_a_newer_magus:\n  enabled: true\n"), 0o644))
	s.T().Setenv("MAGUS_PROC_SOCKET", "")
	s.T().Setenv("XDG_RUNTIME_DIR", s.T().TempDir())
	s.T().Setenv("XDG_CONFIG_HOME", s.T().TempDir())
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	saved := os.Stdout
	os.Stdout = w
	_, res, code := startup(s.T().Context(), []string{"shell", "-o", "json", "touch notes.txt"})
	os.Stdout = saved
	s.Require().NoError(w.Close())
	if res.cleanup != nil {
		s.T().Cleanup(res.cleanup)
	}
	body, err := io.ReadAll(r)
	s.Require().NoError(err)
	var v verdict.Judgment
	s.Require().NoError(codec.Unmarshal(body, &v), string(body))
	s.Contains(v.Reason, "magus guard could not judge this call, so it is refused")
	s.Equal(verdict.Deny, v.Decision)
	s.Equal(2, code, "the guard's deny exit, never startup's 1")
}

func (s *staleConfigSuite) TestTheBootstrapItServesRuns() {
	v := s.judge(fmt.Sprintf("GOEXPERIMENT=jsonv2 go -C %s run -trimpath ./cmd/magus run go-build --no-cache .", s.root))
	s.NotEqual(verdict.Deny, v.Decision, v.Reason)
}

// narrowLease is a delegated worker assigned one package's tests: the shape the
// multi-agent skill hands out, and the shape the gate deny is scoped to. Mirrors
// internal/guard's own narrowLease, unexported there.
func narrowLease() jobtypes.Job {
	return jobtypes.Job{
		ID:         "harness/lease-scoped-deny",
		Agent:      jobtypes.Instructed("lease-scoped denies in the guard"),
		Paths:      jobtypes.Paths{Write: []string{"cmd/magus/**"}},
		Validation: "magus run go::go-test . -- ./internal/ledger/",
		State:      jobtypes.StateRunning,
		Registered: 1,
	}
}

// touchedProjects reads the marker recording which projects this session has written
// to, by the same kind string internal/guard's own touchedProjects (drift.go)
// reads ("touched-projects"); that helper is unexported to internal/guard, so this
// package mirrors its read side for the one assertion that needs it.
func touchedProjects(g hint.Gate) []string {
	if g.CacheDir() == "" {
		return nil
	}
	body, err := os.ReadFile(hint.MarkerPath(g.CacheDir(), g.Session(), "touched-projects"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		if line = strings.TrimSpace(line); line != "" && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

// denyRules is a magusfile whose spawn rule denies every spawn and whose command rule
// denies every command.
const denyRules = `import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > GuardVerdict {
    return magus\guard.deny("Name a model.");
});
magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.deny("Not in this repository.");
});
`
