package vcs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOwedRegenerationRecordMergesAndDrops pins the record's lifecycle: one entry per
// project and target however many files the merge kept, a drop that removes only what
// was settled, and no file at all once nothing is owed.
func TestOwedRegenerationRecordMergesAndDrops(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	ctx := t.Context()

	for _, o := range []OwedRegeneration{
		{Project: ".", Target: "generate", Paths: []string{"gen/b.md"}},
		{Project: "docs", Target: "generate", Paths: []string{"docs/gen/x.md"}},
		{Project: ".", Target: "generate", Paths: []string{"gen/a.md", "gen/b.md"}},
	} {
		ok, err := RecordOwedRegeneration(ctx, dir, o)
		require.NoError(t, err)
		require.True(t, ok)
	}

	got, err := OwedRegenerations(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, []OwedRegeneration{
		{Project: ".", Target: "generate", Paths: []string{"gen/a.md", "gen/b.md"}},
		{Project: "docs", Target: "generate", Paths: []string{"docs/gen/x.md"}},
	}, got)

	path, err := OwedRegenerationPath(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(gitDirOf(t, dir), owedFileName), path)

	require.NoError(t, DropOwedRegenerations(ctx, dir, []OwedRegeneration{
		{Project: ".", Target: "generate", Paths: []string{"gen/a.md"}},
		{Project: "docs", Target: "generate", Paths: []string{"docs/gen/x.md"}},
	}))
	got, err = OwedRegenerations(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, []OwedRegeneration{{Project: ".", Target: "generate", Paths: []string{"gen/b.md"}}}, got,
		"a path recorded but not settled stays owed")

	require.NoError(t, DropOwedRegenerations(ctx, dir, got))
	assert.NoFileExists(t, path, "an empty record is removed, so its presence alone means work is owed")
}

// TestOwedRegenerationRejectsUnknownSchema refuses to guess at a record a newer magus
// wrote.
func TestOwedRegenerationRejectsUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	path, err := OwedRegenerationPath(t.Context(), dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"schema_version": 99, "owed": []}`), 0o644))

	_, err = OwedRegenerations(t.Context(), dir)
	require.ErrorContains(t, err, "schema_version 99")
}

// TestOwedRegenerationOutsideARepository is the backend without hooks: nothing is
// recorded, because nothing would ever settle it.
func TestOwedRegenerationOutsideARepository(t *testing.T) {
	dir := t.TempDir()
	ok, err := RecordOwedRegeneration(t.Context(), dir, OwedRegeneration{Project: ".", Target: "generate", Paths: []string{"x"}})
	require.NoError(t, err)
	assert.False(t, ok)

	got, err := OwedRegenerations(t.Context(), dir)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestInstallRegenHookCoexists pins the five settle hooks, their guards, idempotence,
// that post-commit keeps the drift section it shares, and that the section an earlier
// magus wrote into post-merge is taken out.
func TestInstallRegenHookCoexists(t *testing.T) {
	dir := t.TempDir()
	gitInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	ctx := t.Context()
	hooks := filepath.Join(dir, ".git", "hooks")
	old := managedMarkers{begin: "# BEGIN magus-regenerate-owed", end: "# END magus-regenerate-owed"}
	for _, name := range []string{"post-merge", "post-rewrite"} {
		_, err := writeManagedSection(filepath.Join(hooks, name), old, "magus job run regenerate-owed >/dev/null 2>&1 || true\n", hookFile)
		require.NoError(t, err)
	}

	_, err := gitVCS{}.InstallDriftHook(ctx, dir, "magus job run check-drift")
	require.NoError(t, err)
	installed, err := gitVCS{}.InstallRegenHook(ctx, dir, "magus vcs resolve --hook")
	require.NoError(t, err)
	assert.Equal(t, []string{"pre-merge-commit", "pre-commit", "post-commit", "post-rewrite", "post-applypatch", "post-merge"}, installed)

	drift := driftMarkers.section("magus job run check-drift >/dev/null 2>&1 || true\n")
	notice := "  echo \"magus: the settle hook did not run: magus is missing or predates 'vcs resolve --hook', so this operation is not regenerated; run 'magus vcs resolve' once it is\" >&2\n"
	tolerate := "magus_rc=$?\nif [ $magus_rc -eq 2 ] || [ $magus_rc -eq 127 ]; then\n" + notice
	stop := "elif [ $magus_rc -ne 0 ]; then\n  exit $magus_rc\n"
	assertFile(t, filepath.Join(hooks, "pre-merge-commit"), "#!/bin/sh\n\n"+regenMarkers.section(
		"magus vcs resolve --hook pre-merge-commit\n"+tolerate+stop+"fi\n"), 0o755)
	assertFile(t, filepath.Join(hooks, "pre-commit"), "#!/bin/sh\n\n"+regenMarkers.section(
		"if magus_git_dir=$(git rev-parse --git-dir) && { [ -e \"$magus_git_dir/CHERRY_PICK_HEAD\" ] || [ -e \"$magus_git_dir/REVERT_HEAD\" ] || [ -e \"$magus_git_dir/MERGE_HEAD\" ]; }; then\n"+
			"  magus vcs resolve --hook pre-commit\n"+
			"  magus_rc=$?\n"+
			"  if [ $magus_rc -eq 2 ] || [ $magus_rc -eq 127 ]; then\n"+
			"  "+notice+
			"  elif [ $magus_rc -ne 0 ]; then\n"+
			"    exit $magus_rc\n"+
			"  fi\n"+
			"fi\n"), 0o755)
	assertFile(t, filepath.Join(hooks, "post-commit"), "#!/bin/sh\n\n"+drift+"\n"+regenMarkers.section(
		"if magus_git_dir=$(git rev-parse --git-dir) && { [ -e \"$magus_git_dir/CHERRY_PICK_HEAD\" ] || [ -e \"$magus_git_dir/REVERT_HEAD\" ]; }; then\n"+
			"  magus vcs resolve --hook post-commit\n"+
			"  magus_rc=$?\n"+
			"  if [ $magus_rc -eq 2 ] || [ $magus_rc -eq 127 ]; then\n"+
			"  "+notice+
			"  fi\n"+
			"fi\n"), 0o755)
	assertFile(t, filepath.Join(hooks, "post-rewrite"), "#!/bin/sh\n\n"+regenMarkers.section(
		"if [ \"$1\" = rebase ]; then\n"+
			"  magus vcs resolve --hook post-rewrite \"$@\"\n"+
			"  magus_rc=$?\n"+
			"  if [ $magus_rc -eq 2 ] || [ $magus_rc -eq 127 ]; then\n"+
			"  "+notice+
			"  fi\n"+
			"fi\n"), 0o755)
	assertFile(t, filepath.Join(hooks, "post-applypatch"), "#!/bin/sh\n\n"+regenMarkers.section(
		"magus vcs resolve --hook post-applypatch\n"+tolerate+"fi\n"), 0o755)
	assertFile(t, filepath.Join(hooks, "post-merge"), "#!/bin/sh\n", 0o755)

	again, err := gitVCS{}.InstallRegenHook(ctx, dir, "magus vcs resolve --hook")
	require.NoError(t, err)
	assert.Empty(t, again)
}

func gitDirOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := gitOutput(t.Context(), dir, gitOpts{}, "rev-parse", "--absolute-git-dir")
	require.NoError(t, err)
	return out
}
