package settle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/gen/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestProjectKeyIsPathNotLabel pins the contract Plan.Paths depends on: the rebuild set
// is keyed by project PATH, and for the ROOT that key is ".", never the display label,
// which Display renders as the directory basename. Keying one side by label was a real
// bug: the set held "." while the lookup asked for "magus" (or a worktree's own directory
// name), so it missed every time and every root-owned regenerated output was left
// modified and unstaged, which is what makes `git rebase --continue` refuse.
func TestProjectKeyIsPathNotLabel(t *testing.T) {
	root := &types.Project{Path: "", Dir: "/repos/magus"}
	dotted := &types.Project{Path: ".", Dir: "/repos/magus"}
	nested := &types.Project{Path: "docs", Dir: "/repos/magus/docs"}

	assert.Equal(t, ".", projectKey(root), "the root keys as \".\", whatever its directory is called")
	assert.Equal(t, ".", projectKey(dotted), "a root spelled \".\" keys the same as one spelled \"\"")
	assert.Equal(t, "docs", projectKey(nested))

	// The label is what this must NOT be, and only the root can tell the two apart.
	assert.NotEqual(t, types.ProjectLabel(root.Path, root.Dir), projectKey(root),
		"keying the root by its label is the bug; Display gives the directory basename")
	assert.Equal(t, types.ProjectLabel(nested.Path, nested.Dir), projectKey(nested),
		"a nested project's label and path agree, which is why the bug hid")
}

// TestRebuiltProjectsFindsRoot proves the two sides now meet: a plan filled the way
// PlanConflicts fills it is readable the way Plan.Paths reads it, for the root project.
func TestRebuiltProjectsFindsRoot(t *testing.T) {
	root := &types.Project{Path: "", Dir: "/repos/magus"}
	plan := Plan{Rebuild: map[string][]string{"generate": {projectKey(root)}}}

	assert.True(t, plan.rebuiltProjects()[projectKey(root)],
		"a root-owned regenerated output must be recognized as covered by the rebuild")
}

// TestPlanConflictsLeavesAnExcludedHandFileToAPerson is the jj path: jj routes nothing by
// pattern, so `magus vcs resolve` picks the paths to settle itself, through
// FindOutputProducer. A hand file an output carves out is source, so it goes to a person
// while the generated file beside it is kept and rebuilt.
func TestPlanConflictsLeavesAnExcludedHandFileToAPerson(t *testing.T) {
	root := t.TempDir()
	magusfile := `import "magus";

magus\project({})

export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles("gen/*.go", "!gen/runtime.go");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
	m, err := magus.Open(t.Context(), root)
	require.NoError(t, err)
	resolver := mocks.NewMockConflictResolver(t)
	resolver.EXPECT().IgnoredPaths(mock.Anything, mock.Anything, mock.Anything).Return(map[string]bool{}, nil)

	plan, err := PlanConflicts(t.Context(), m, resolver, []types.Conflict{
		{Path: "gen/fs.go", Kind: types.ConflictKindContent},
		{Path: "gen/runtime.go", Kind: types.ConflictKindContent},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"gen/fs.go"}, plan.Keep)
	assert.Equal(t, []string{"gen/runtime.go"}, plan.Manual)
	assert.Equal(t, map[string][]string{"generate": {"."}}, plan.Rebuild)
}
