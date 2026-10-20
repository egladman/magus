package guard_test //nolint:testlayout // in-package would close a cycle: the root package imports internal/guard

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rule tests in package guard open real workspaces, which only the root package can do,
// and the root package imports this one. An external test package may import both.
func init() {
	guard.WorkspaceInspect = func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
		return magus.Inspect(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
	}
	guard.WorkspaceCacheDir = func(root string) (string, error) { return magus.ResolveCacheDir(root) }
}

const shellAndCommandRules = `import "magus";

magus\guard.shell({"name": "no-frobnicate", "decision": "deny", "program": "frobnicate", "reason": "Not here."});

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.allow();
});
`

// The builder hands Judge every rule the root magusfile registers, with the approved side
// beside it, and returns the rules for a caller that records where they came from.
func TestNewDependenciesLoadsTheWorkspaceRules(t *testing.T) {
	testkit.Isolate(t)
	repo := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": shellAndCommandRules})

	deps, rules := guard.NewDependencies(t.Context(), guard.Wiring{Root: repo.Root()})
	require.NotNil(t, rules)
	require.NoError(t, deps.LoadFailure)
	require.Len(t, deps.ShellRules, 1)
	assert.Equal(t, "no-frobnicate", deps.ShellRules[0].Name)
	assert.NotNil(t, deps.CommandRule)
	assert.NotNil(t, deps.ApprovedCommandRule)
	require.NotNil(t, deps.Policy)
	files := deps.Policy().Files
	require.Len(t, files, 1, "the root magusfile is the one source the rules came from")
	assert.Equal(t, "magusfile.buzz", filepath.Base(files[0]))
	assert.NotEmpty(t, deps.Builtins, "the light level sets rules")
	assert.Len(t, rules.Policy().Sources, 1)
}

// A working tree that does not load is reported, not read as "no rules": the failure rides
// on the dependencies and the approved sources still judge.
func TestNewDependenciesReportsAMagusfileThatDoesNotLoad(t *testing.T) {
	testkit.Isolate(t)
	repo := testkit.NewRepo(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": shellAndCommandRules})
	repo.Magusfile("import \"magus\";\n\nmagus\\guard.shell(\n")

	deps, rules := guard.NewDependencies(t.Context(), guard.Wiring{Root: repo.Root()})
	assert.Nil(t, rules)
	require.Error(t, deps.LoadFailure)
	assert.Nil(t, deps.CommandRule)
	assert.NotNil(t, deps.ApprovedCommandRule, "the committed rule still judges")
	assert.NotNil(t, deps.ApprovedSpawnRule)
	assert.NotNil(t, deps.ApprovedWriteRule)
}

// Outside a workspace only the compiled rules judge, and nothing failed.
func TestNewDependenciesOutsideAWorkspace(t *testing.T) {
	deps, rules := guard.NewDependencies(t.Context(), guard.Wiring{})
	assert.Nil(t, rules)
	require.NoError(t, deps.LoadFailure)
	assert.Nil(t, deps.ApprovedCommandRule)
	assert.Nil(t, deps.Policy)
	assert.NotNil(t, deps.Spells, "the raw-tool rule still has the spell catalog")
}

func TestHostJudgedMarksOnlyItsContext(t *testing.T) {
	assert.False(t, guard.HostJudged(t.Context()))
	assert.True(t, guard.HostJudged(guard.WithHostJudged(t.Context())))
}
