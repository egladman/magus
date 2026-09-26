package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestWorkspaceLoadWritesNoHook: the load-time refresh registers the driver and leaves
// the hooks dir byte for byte as it was. The hooks dir is shared by every worktree of
// the repository, and only `magus init --vcs git` writes into it.
func TestWorkspaceLoadWritesNoHook(t *testing.T) {
	ctx := context.Background()
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
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gen"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gen", "catalog.md"), []byte("kept\n"), 0o644))
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "initial")
	m, err := magus.Open(ctx, root)
	require.NoError(t, err)

	hooks := filepath.Join(root, ".git", "hooks")
	before := dirSnapshot(t, hooks)
	ensureMergeDriver(ctx, m)

	registered, err := resolveGitDriver(t, root).MergeDriverCommand(ctx, root)
	require.NoError(t, err)
	assert.Contains(t, registered, "vcs merge-driver", "the refresh did register the driver")
	assert.Equal(t, before, dirSnapshot(t, hooks))
	missing, err := vcs.SettleHooksMissing(ctx, root)
	require.NoError(t, err)
	assert.Equal(t, vcs.SettleHooks, missing)
}

// TestGeneratorProjectsCountsMovedOutputs: a project whose output the operation moved
// regenerates too, since that output was made against a base the operation replaced.
func TestGeneratorProjectsCountsMovedOutputs(t *testing.T) {
	got := generatorProjects([]types.FileEntry{
		{Path: "docs/a.md", Role: "source", SourceOf: []string{".", "docs"}},
		{Path: "api/gen/x.go", Role: "output", OutputOf: []string{"api"}},
		{Path: "README", Role: "unclaimed"},
	})
	assert.Equal(t, []string{".", "api", "docs"}, got)
}

// TestSettleInvocationsRunDeepestFirst: one `<target>:rw` run per depth and target,
// nested projects before the ancestors that index their output, the owed records'
// own targets beside the generate convention, and a project with no such target left
// out rather than failing the run.
func TestSettleInvocationsRunDeepestFirst(t *testing.T) {
	defines := func(path, target string) bool {
		return target != "generate" || (path != "api" && path != "gone")
	}
	got := settleInvocations([]string{".", "api", "docs", "docs/site", "gone"}, []vcs.OwedRegeneration{
		{Project: ".", Target: "index-generate"},
		{Project: "docs", Target: "generate"},
		{Project: "api", Target: "proto"},
	}, defines)
	assert.Equal(t, [][]string{
		{"generate:rw", "docs/site"},
		{"generate:rw", "docs"},
		{"proto:rw", "api"},
		{"generate:rw", "."},
		{"index-generate:rw", "."},
	}, got)
}

// TestSettlementNotice pins the one line each hook prints, since it is the whole
// account a person gets of what the hook did to their commit.
func TestSettlementNotice(t *testing.T) {
	ran := [][]string{{"generate:rw", "docs", "."}}
	run := hint.Run.With("generate:rw", "docs", ".")
	cases := []struct {
		name    string
		s       settlement
		hook    string
		fold    string
		want    string
		pending bool
	}{
		{name: "current", s: settlement{kind: "merge", ran: ran}, hook: vcs.HookPreMergeCommit,
			want: "magus: this merge changed generator inputs; ran " + run + "; the generated output was already current"},
		{name: "merge stopped", s: settlement{kind: "merge", ran: ran, staged: []string{"MAGUS.md"}}, hook: vcs.HookPreMergeCommit, pending: true,
			want: "magus: this merge changed generator inputs; ran " + run + "; staged 1 regenerated file(s) into the merge, so the commit is left to you: `git commit` concludes it"},
		{name: "into the commit", s: settlement{kind: "cherry-pick", ran: ran, staged: []string{"MAGUS.md", "docs/gen/a"}}, hook: vcs.HookPreCommit, pending: true,
			want: "magus: this cherry-pick changed generator inputs; ran " + run + "; staged 2 regenerated file(s) into this commit"},
		{name: "fold", s: settlement{kind: "rebase", ran: ran, staged: []string{"MAGUS.md"}}, hook: vcs.HookPostRewrite, fold: "git commit --amend --no-edit",
			want: "magus: this rebase changed generator inputs; ran " + run + "; staged 1 regenerated file(s); fold them into HEAD with `git commit --amend --no-edit`"},
		{name: "pushed", s: settlement{kind: "rebase", ran: ran, staged: []string{"MAGUS.md"}}, hook: vcs.HookPostRewrite,
			want: "magus: this rebase changed generator inputs; ran " + run + "; staged 1 regenerated file(s); HEAD may already be pushed, so commit them as a new commit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.s
			s.op = vcs.HookOperation{CommitPending: tc.pending}
			assert.Equal(t, tc.want, s.notice(tc.hook, tc.fold))
		})
	}
}
