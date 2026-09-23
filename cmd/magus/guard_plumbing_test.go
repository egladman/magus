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

// denySpawns is a magusfile whose spawn rule denies every spawn.
const denySpawns = `import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > SpawnVerdict {
    return magus\guard.deny("Name a model.");
});
`

// committedDenyRule is a git repository whose committed root magusfile is denySpawns.
func committedDenyRule(t *testing.T) string {
	t.Helper()
	root := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("{}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(denySpawns), 0o644))
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
			got, err := rule(t.Context(), types.SpawnRequest{Kind: types.SpawnKindSpawn, Role: types.SpawnRoleRoot}, hint.NewGate(t.TempDir(), "claude-code/s1"))
			require.NoError(t, err)
			assert.Equal(t, types.SpawnVerdict{Decision: types.SpawnDeny, Reason: "Name a model."}, got)
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

// A working tree that fails to load for a reason outside any .buzz file, here a version
// floor this binary is below, must not take the committed rule down with it: resolving the
// approved rule reads nothing the working tree's config says.
func TestCommittedSpawnRuleSurvivesAVersionFloor(t *testing.T) {
	root := committedDenyRule(t)
	resetWorkspaceMemo(t)
	version = "v0.5.0"
	globalCfg.RequiredVersion = ">= 99.0.0"

	v := judgeSpawnAt(t, root)
	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, "Name a model.", v.Reason)
}

// otherRepository is a workspace that is not a *magus.Magus.
type otherRepository struct{ types.WorkspaceRepository }

// A workspace the hook cannot read its rules from is a load failure, not a workspace with
// no rules, so the committed rule still applies.
func TestUnreadableWorkspaceStillRunsTheCommittedRule(t *testing.T) {
	root := committedDenyRule(t)
	resetWorkspaceMemo(t)
	inspectOnce.Do(func() { inspectValue = otherRepository{} })

	_, err := loadedWorkspace(t.Context())
	require.Error(t, err)
	v := judgeSpawnAt(t, root)
	assert.Equal(t, "deny", v.Decision)
	assert.Equal(t, "Name a model.", v.Reason)
}
