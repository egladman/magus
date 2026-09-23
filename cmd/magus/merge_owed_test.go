package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// owedWorkspace is a committed git repository whose root project's generate target
// declares gen/** and whose tree carries one generated file, gen/catalog.md.
func owedWorkspace(t *testing.T) (*magus.Magus, string) {
	t.Helper()
	root := initGitRepo(t)
	magusfile := `import "magus";
import "fs";

magus.project({})

export fun generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles("gen/**");
    fs\writeFile("gen/catalog.md", "regenerated\n");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".magus/\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gen"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gen", "catalog.md"), []byte("kept\n"), 0o644))
	runGit(t, root, "add", "magusfile.buzz", ".gitignore", "gen/catalog.md")
	runGit(t, root, "commit", "-m", "initial")

	m, err := magus.Open(context.Background(), root)
	require.NoError(t, err)
	return m, root
}

func recordOwed(t *testing.T, root string, o vcs.OwedRegeneration) {
	t.Helper()
	ok, err := vcs.RecordOwedRegeneration(t.Context(), root, o)
	require.NoError(t, err)
	require.True(t, ok)
}

// TestMergeDriverRecordsOwedRegeneration pins the driver's half: in a git checkout it
// keeps %A untouched and records the target that rebuilds the file.
func TestMergeDriverRecordsOwedRegeneration(t *testing.T) {
	ctx, root := mergeDriverWorkspace(t)
	runGit(t, root, "init")
	result := writeResultFile(t, t.TempDir(), "generated: the current version\n")

	require.NoError(t, mergeDriverRun(ctx, root, []string{"ancestor", result, "other", "7", "gen/catalog.md"}))
	require.NoError(t, mergeDriverRun(ctx, root, []string{"ancestor", result, "other", "7", "gen/index.md"}))

	owed, err := vcs.OwedRegenerations(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []vcs.OwedRegeneration{
		{Project: ".", Target: "generate", Paths: []string{"gen/catalog.md", "gen/index.md"}},
	}, owed, "two kept files of one target owe one run")
}

// TestSettleOwedRegenerationStagesAndClears is the hook-side half with the generator
// stubbed: one run per owed target, the rewritten output staged, the record gone.
func TestSettleOwedRegenerationStagesAndClears(t *testing.T) {
	m, root := owedWorkspace(t)
	recordOwed(t, root, vcs.OwedRegeneration{Project: ".", Target: "generate", Paths: []string{"gen/catalog.md"}})

	var ran [][]string
	s, err := settleOwedRegeneration(t.Context(), m, resolveGitDriver(t, root), func(_ context.Context, args []string) error {
		ran = append(ran, args)
		return os.WriteFile(filepath.Join(root, "gen", "catalog.md"), []byte("regenerated\n"), 0o644)
	})
	require.NoError(t, err)

	assert.Equal(t, owedSettlement{ran: [][]string{{"generate:rw", "."}}, staged: []string{"gen/catalog.md"}}, s)
	assert.Equal(t, s.ran, ran)
	assert.Equal(t, "gen/catalog.md", gitOut(t, root, "diff", "--cached", "--name-only"))
	owed, err := vcs.OwedRegenerations(t.Context(), root)
	require.NoError(t, err)
	assert.Empty(t, owed)
	assert.Equal(t,
		"regenerate-owed: ran "+hint.Run.With("generate:rw", ".")+"; staged 1 file(s); fold them into HEAD with `git commit --amend --no-edit`",
		s.notice("git commit --amend --no-edit"))
}

// TestSettleOwedRegenerationFailureKeepsRecord: a failed generator must not wedge
// anything, and must not lose the record the next run needs.
func TestSettleOwedRegenerationFailureKeepsRecord(t *testing.T) {
	m, root := owedWorkspace(t)
	want := vcs.OwedRegeneration{Project: ".", Target: "generate", Paths: []string{"gen/catalog.md"}}
	recordOwed(t, root, want)

	_, err := settleOwedRegeneration(t.Context(), m, resolveGitDriver(t, root), func(context.Context, []string) error {
		return errors.New("generator exploded")
	})
	require.ErrorContains(t, err, "generator exploded")

	owed, err := vcs.OwedRegenerations(t.Context(), root)
	require.NoError(t, err)
	assert.Equal(t, []vcs.OwedRegeneration{want}, owed)
	assert.Empty(t, gitOut(t, root, "diff", "--cached", "--name-only"))
}

// TestSettleOwedRegenerationDropsUndeclared: an entry whose target the merge removed
// can never settle, so it is dropped and named instead of reported forever.
func TestSettleOwedRegenerationDropsUndeclared(t *testing.T) {
	m, root := owedWorkspace(t)
	gone := vcs.OwedRegeneration{Project: ".", Target: "vanished", Paths: []string{"gen/old.md"}}
	recordOwed(t, root, gone)

	s, err := settleOwedRegeneration(t.Context(), m, resolveGitDriver(t, root), func(context.Context, []string) error {
		t.Fatal("nothing declared is owed, so nothing may run")
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, owedSettlement{undeclared: []vcs.OwedRegeneration{gone}}, s)
	assert.Equal(t, "regenerate-owed: nothing left to run; dropped, no longer declared: vanished in .", s.notice(""))
	owed, err := vcs.OwedRegenerations(t.Context(), root)
	require.NoError(t, err)
	assert.Empty(t, owed)
}

// TestOwedInvocationsRunDeepestFirst pins the order: nested projects before the
// ancestors that index their output, one run per depth and target.
func TestOwedInvocationsRunDeepestFirst(t *testing.T) {
	got := owedInvocations([]vcs.OwedRegeneration{
		{Project: ".", Target: "generate"},
		{Project: "docs", Target: "generate"},
		{Project: "api", Target: "generate"},
		{Project: "docs/site", Target: "generate"},
		{Project: ".", Target: "graph"},
	})
	assert.Equal(t, [][]string{
		{"generate:rw", "docs/site"},
		{"generate:rw", "api", "docs"},
		{"generate:rw", "."},
		{"graph:rw", "."},
	}, got)
}

// TestWaitForFinishedOperation: the job must not regenerate while git still owns the
// tree, and must go ahead the moment it lets go.
func TestWaitForFinishedOperation(t *testing.T) {
	root := initGitRepo(t)
	mergeHead := filepath.Join(root, ".git", "MERGE_HEAD")
	require.NoError(t, os.WriteFile(mergeHead, []byte("0000000000000000000000000000000000000000\n"), 0o644))

	finished, err := waitForFinishedOperation(t.Context(), root, 0, 0)
	require.NoError(t, err)
	assert.False(t, finished, "a merge in progress holds the regeneration back")

	require.NoError(t, os.Remove(mergeHead))
	finished, err = waitForFinishedOperation(t.Context(), root, 0, 0)
	require.NoError(t, err)
	assert.True(t, finished)
}
