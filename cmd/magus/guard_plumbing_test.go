package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

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

// committedDenyRule is a git repository whose committed root magusfile is denyRules.
func committedDenyRule(t *testing.T) string {
	t.Helper()
	root := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("{}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(denyRules), 0o644))
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "init")
	return root
}

// A committed spawn rule still answers however the working tree leaves the root magusfile:
// a syntax error, the file deleted, or the other magusfile form added beside it.
func TestApprovedSpawnRuleAtSurvivesABrokenWorkingTree(t *testing.T) {
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
			root := committedDenyRule(t)
			tc.mutate(t, root)

			rule, err := magus.ApprovedSpawnRuleAt(t.Context(), root)
			require.NoError(t, err)
			require.NotNil(t, rule)
			got, err := rule(t.Context(), types.SpawnRequest{Kind: types.SpawnKindSpawn, Role: types.AgentRoleRoot}, hint.NewGate(t.TempDir(), "claude-code/s1"))
			require.NoError(t, err)
			assert.Equal(t, types.GuardVerdict{Decision: types.GuardDeny, Reason: "Name a model."}, got)

			command, err := magus.ApprovedCommandRuleAt(t.Context(), root)
			require.NoError(t, err)
			require.NotNil(t, command)
			got, err = command(t.Context(), types.CommandRequest{Command: "ls", Role: types.AgentRoleRoot}, hint.NewGate(t.TempDir(), "claude-code/s1"))
			require.NoError(t, err)
			assert.Equal(t, types.GuardVerdict{Decision: types.GuardDeny, Reason: "Not in this repository."}, got)
		})
	}
}

// spawnEnvelope is a Claude Code spawn as its hook wiring hands it to `magus shell`.
const spawnEnvelope = `{"session_id":"8f2c6a1e","hook_event_name":"PreToolUse","tool_name":"Agent",` +
	`"tool_input":{"description":"implement adr 0002","prompt":"Implement ADR 0002.","subagent_type":"general-purpose"}}`

// judgeSpawnAt runs one spawn through the hook's own dependencies with the process sitting
// in root, as the shipped hook does.
func judgeSpawnAt(t *testing.T, root string) guard.Verdict {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Chdir(root)
	ctx := guard.WithLocation(t.Context(), t.TempDir(), root, root)
	return guard.Judge(ctx, guardDependencies(), guard.Request{Input: spawnEnvelope, Host: "claude-code"})
}

// resetWorkspaceMemo gives a test its own memoized workspace load and restores the
// process-wide state afterwards.
func resetWorkspaceMemo(t *testing.T) {
	t.Helper()
	savedVersion := version
	resetStartupSingletons()
	t.Cleanup(func() {
		resetStartupSingletons()
		version = savedVersion
	})
}

// The push facts come from git itself: a branch checkout is attached, a commit checkout is
// detached, and remote-tracking branches are listed as git names them short.
func TestGitStateForGuard(t *testing.T) {
	root := committedDenyRule(t)
	runGit(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")

	got := gitStateForGuard(t.Context(), root)
	require.NotNil(t, got)
	assert.Equal(t, types.GitState{Detached: false, RemoteBranches: []string{"origin/main"}}, *got)

	runGit(t, root, "checkout", "-q", "--detach")
	got = gitStateForGuard(t.Context(), root)
	require.NotNil(t, got)
	assert.True(t, got.Detached)

	assert.Nil(t, gitStateForGuard(t.Context(), t.TempDir()), "outside any git checkout")
}

// The hook reads the root magusfile alone, so a working tree the full load would refuse,
// here for a version floor this binary is below, still has its rules applied.
func TestSpawnRuleSurvivesAVersionFloor(t *testing.T) {
	root := committedDenyRule(t)
	resetWorkspaceMemo(t)
	version = "v0.5.0"
	globalCfg.RequiredVersion = ">= 99.0.0"

	v := judgeSpawnAt(t, root)
	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, "Name a model.", v.Reason)
}

// A root magusfile that does not load is a load failure, not a workspace with no rules,
// so the committed rules still apply to a spawn and to a command.
func TestUnloadableWorkingTreeStillRunsTheCommittedRules(t *testing.T) {
	root := committedDenyRule(t)
	resetWorkspaceMemo(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n\nmagus\\guard.spawn(\n"), 0o644))
	t.Chdir(root)
	_, err := loadGuardRules(t.Context())
	require.Error(t, err)

	v := judgeSpawnAt(t, root)
	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, "Name a model.", v.Reason)

	ctx := guard.WithLocation(t.Context(), t.TempDir(), root, root)
	v = guard.Judge(ctx, guardDependencies(), guard.Request{Input: "ls -la", Host: "claude-code"})
	assert.Equal(t, "deny", v.Decision)
	assert.Contains(t, v.Reason, "Not in this repository.")
	assert.Equal(t, "workspace:command", v.Rule)
}

// The committed side is loaded only while a file the root load read differs from it:
// an uncommitted edit anywhere else changes nothing either side registers.
func TestApprovedRulesLoadOnlyForAPendingPolicySource(t *testing.T) {
	root := committedDenyRule(t)
	resetWorkspaceMemo(t)
	t.Chdir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "spells", "other"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "spells", "other", "spell.buzz"), []byte("// untracked\n"), 0o644))

	rules, err := loadGuardRules(t.Context())
	require.NoError(t, err)
	approved, err := rules.ApprovedCommandRule(t.Context())
	require.NoError(t, err)
	assert.Nil(t, approved, "no policy source is pending, so the working tree's rule is the approved one")

	// Loosen the rule in the working tree: now the committed deny must still answer.
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n"), 0o644))
	rules, err = loadGuardRules(t.Context())
	require.NoError(t, err)
	assert.Nil(t, rules.CommandRule())
	approved, err = rules.ApprovedCommandRule(t.Context())
	require.NoError(t, err)
	require.NotNil(t, approved)
	got, err := approved(t.Context(), types.CommandRequest{Command: "ls"}, hint.NewGate(t.TempDir(), "s"))
	require.NoError(t, err)
	assert.Equal(t, types.GuardDeny, got.Decision)
}
