package vcs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/stamp"
	"github.com/egladman/magus/libs/testkit"
)

// TestOwedRegenerationRecordMergesAndDrops pins the record's lifecycle: one entry per
// project and target however many files the merge kept, a drop that removes only what
// was settled, and no file at all once nothing is owed.
func TestOwedRegenerationRecordMergesAndDrops(t *testing.T) {
	dir := testkit.NewRepo(t, map[string]string{"a.txt": "a\n"}).Root()
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
	assert.Equal(t, filepath.Join(absoluteGitDir(t, dir), owedFileName), path)

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
	dir := testkit.NewRepo(t, map[string]string{"a.txt": "a\n"}).Root()
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

// TestInstallRegenHookStripsTheBlockingSections pins that no settle hook is installed, that
// the regenerate section an earlier magus wrote is taken out of every hook that carried
// it, and that the drift section post-commit shares and a hand-written body stay.
func TestInstallRegenHookStripsTheBlockingSections(t *testing.T) {
	dir := testkit.NewRepo(t, map[string]string{"a.txt": "a\n"}).Root()
	ctx := t.Context()
	hooks := filepath.Join(dir, ".git", "hooks")

	_, err := gitVCS{}.InstallDriftHook(ctx, dir, "magus job run check-drift")
	require.NoError(t, err)
	for _, name := range append([]string{"post-merge"}, SettleHooks...) {
		_, err := writeManagedSection(hooks, filepath.Join(hooks, name), regenMarkers, "magus vcs resolve --hook "+name+"\n", hookFile, stamp.Judge{})
		require.NoError(t, err)
	}
	foreign := "#!/bin/sh\necho mine\n"
	require.NoError(t, os.WriteFile(filepath.Join(hooks, "post-applypatch"), []byte(foreign), 0o755))
	stale, err := SettleHooksStale(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"post-merge", "pre-merge-commit", "pre-commit", "post-commit", "post-rewrite"}, stale)

	removed, err := gitVCS{}.InstallRegenHook(ctx, dir, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"post-merge", "pre-merge-commit", "pre-commit", "post-commit", "post-rewrite"}, removed)

	drift := driftMarkers.section("magus job run check-drift post-commit >/dev/null 2>&1 || true\n")
	assertFile(t, filepath.Join(hooks, "post-commit"), "#!/bin/sh\n\n"+drift, 0o755)
	assertFile(t, filepath.Join(hooks, "post-applypatch"), foreign, 0o755)
	for _, name := range []string{"pre-merge-commit", "pre-commit"} {
		data, err := os.ReadFile(filepath.Join(hooks, name))
		require.NoError(t, err)
		assert.NotContains(t, string(data), "vcs resolve", name)
	}
	stale, err = SettleHooksStale(ctx, dir)
	require.NoError(t, err)
	assert.Empty(t, stale)

	again, err := gitVCS{}.InstallRegenHook(ctx, dir, "")
	require.NoError(t, err)
	assert.Empty(t, again)
}

// TestInstallRegenHookInstallsNothing: a repository no earlier magus wrote a settle hook
// into gains none.
func TestInstallRegenHookInstallsNothing(t *testing.T) {
	dir := testkit.NewRepo(t, map[string]string{"a.txt": "a\n"}).Root()
	hooks := filepath.Join(dir, ".git", "hooks")
	before := snapshotHooks(t, hooks)
	installed, err := gitVCS{}.InstallRegenHook(t.Context(), dir, "")
	require.NoError(t, err)
	assert.Empty(t, installed)
	assert.Equal(t, before, snapshotHooks(t, hooks))
}

func snapshotHooks(t *testing.T, hooks string) []string {
	t.Helper()
	entries, err := os.ReadDir(hooks)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func absoluteGitDir(t *testing.T, dir string) string {
	t.Helper()
	out, err := gitOutput(t.Context(), dir, gitOpts{}, "rev-parse", "--absolute-git-dir")
	require.NoError(t, err)
	return out
}
