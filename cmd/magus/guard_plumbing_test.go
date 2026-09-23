package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// A committed spawn rule still answers when the working tree's magusfile no longer
// parses, so leaving a syntax error behind does not switch the rule off.
func TestApprovedSpawnRuleAtSurvivesABrokenWorkingTree(t *testing.T) {
	root := initGitRepo(t)
	magusfile := filepath.Join(root, "magusfile.buzz")
	require.NoError(t, os.WriteFile(magusfile, []byte(`import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > SpawnVerdict {
    return magus\guard.deny("Name a model.");
});
`), 0o644))
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-q", "-m", "init")
	require.NoError(t, os.WriteFile(magusfile, []byte("import \"magus\";\n\nmagus\\guard.spawn(\n"), 0o644))

	_, err := magus.Inspect(t.Context(), root)
	require.Error(t, err, "the working tree does not load")

	rule := magus.ApprovedSpawnRuleAt(t.Context(), root)
	require.NotNil(t, rule)
	got, err := rule(t.Context(), types.SpawnRequest{Kind: types.SpawnKindSpawn, Role: types.SpawnRoleRoot}, hint.NewGate(t.TempDir(), "claude-code/s1"))
	require.NoError(t, err)
	assert.Equal(t, types.SpawnVerdict{Decision: types.SpawnDeny, Reason: "Name a model."}, got)
}
