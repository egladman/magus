package vcs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// TestOwedRegenerationRecordMergesAndDrops pins the record's lifecycle: one entry per
// project and target however many files the merge kept, a drop that removes only what
// was settled, and no file at all once nothing is owed.
func TestOwedRegenerationRecordMergesAndDrops(t *testing.T) {
	dir := testkit.NewRepository(t, map[string]string{"a.txt": "a\n"}).Root()
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
	dir := testkit.NewRepository(t, map[string]string{"a.txt": "a\n"}).Root()
	path, err := OwedRegenerationPath(t.Context(), dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(`{"api_version": "magus.owed/v1alpha99", "owed": []}`), 0o644))

	_, err = OwedRegenerations(t.Context(), dir)
	require.ErrorContains(t, err, "record is magus.owed/v1alpha99, newer than the magus.owed/v1alpha1 this reader reads")
}

func TestOwedRegenerationsRefusesAnIntegerStamp(t *testing.T) {
	dir := testkit.NewRepository(t, map[string]string{"a.txt": "a\n"}).Root()
	path, err := OwedRegenerationPath(t.Context(), dir)
	require.NoError(t, err)
	older := `{"schema_version": 1, "owed": [{"project": ".", "target": "generate", "paths": ["gen/a.md"]}]}`
	require.NoError(t, os.WriteFile(path, []byte(older), 0o644))

	_, err = OwedRegenerations(t.Context(), dir)
	require.ErrorContains(t, err, "record carries no api_version")
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

func absoluteGitDir(t *testing.T, dir string) string {
	t.Helper()
	out, err := gitOutput(t.Context(), dir, gitOpts{}, "rev-parse", "--absolute-git-dir")
	require.NoError(t, err)
	return out
}

// apiRoles classifies the fixture's paths as a workspace declaring project api would,
// with api/src.proto a generator's input and api/gen.go its output.
func apiRoles(_ context.Context, paths []string) ([]types.FileEntry, error) {
	var out []types.FileEntry
	for _, p := range paths {
		switch p {
		case "api/src.proto":
			out = append(out, types.FileEntry{Path: p, Role: "source", SourceOf: []string{"api"}})
		case "api/gen.go":
			out = append(out, types.FileEntry{Path: p, Role: "output", OutputOf: []string{"api"}})
		default:
			out = append(out, types.FileEntry{Path: p, Role: "unclaimed"})
		}
	}
	return out, nil
}

func everyProjectGenerates(string) bool { return true }

// commitNew records one new file on top of the checkout and returns the commit's id.
// jj then starts a new change, so the next commit stacks on this one.
func commitNew(t *testing.T, b parityBackend, dir, name, msg string) string {
	t.Helper()
	writeRepositoryFile(t, dir, name, msg+"\n")
	addPath(t, b, dir, name)
	commitAll(t, b, dir, msg)
	c, err := b.drv.FindCommit(t.Context(), dir, "")
	require.NoErrorf(t, err, "%s FindCommit", b.name)
	if b.name == "jj" {
		vcsTestRun(t, dir, "jj", "new")
	}
	return c.ID
}

// A range that changes an input and no output owes its project. The fold rewrites the
// commit that changed the input, and nothing is recorded to say so.
func TestParityRangeOwesAnInputWithoutItsOutput(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"README": "r\n"})
		base := initialCommitID(t, b, dir)
		changed := commitNew(t, b, dir, "api/src.proto", "change the input")
		head := commitNew(t, b, dir, "notes.txt", "an unrelated commit on top")

		owed, err := RangeOwes(t.Context(), b.drv, dir, base, head, apiRoles, everyProjectGenerates)
		require.NoErrorf(t, err, "%s RangeOwes", b.name)
		assert.Equalf(t, RangeOwed{Base: base, Head: head, Projects: []string{"api"}, Last: changed}, owed,
			"%s: the fold rewrites the commit that changed the input", b.name)
	})
}

// An output that moves in the range settles its project, whichever commit moved it.
func TestParityRangeOwesNothingOnceTheOutputMoves(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"README": "r\n"})
		base := initialCommitID(t, b, dir)
		commitNew(t, b, dir, "api/src.proto", "change the input")
		head := commitNew(t, b, dir, "api/gen.go", "regenerate")

		owed, err := RangeOwes(t.Context(), b.drv, dir, base, head, apiRoles, everyProjectGenerates)
		require.NoErrorf(t, err, "%s RangeOwes", b.name)
		assert.Equalf(t, RangeOwed{Base: base, Head: head}, owed, "%s", b.name)
	})
}

// A project with no generator owes nothing, since no regeneration could settle it.
func TestParityRangeOwesOnlyGenerators(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"README": "r\n"})
		base := initialCommitID(t, b, dir)
		head := commitNew(t, b, dir, "api/src.proto", "change the input")

		owed, err := RangeOwes(t.Context(), b.drv, dir, base, head, apiRoles, func(string) bool { return false })
		require.NoErrorf(t, err, "%s RangeOwes", b.name)
		assert.Emptyf(t, owed.Projects, "%s", b.name)
	})
}

// A range the backend cannot read is unknown, never a range that owes nothing.
func TestParityRangeOwesRefusesAnUnreadableRange(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"README": "r\n"})
		base := initialCommitID(t, b, dir)

		_, err := RangeOwes(t.Context(), b.drv, dir, base, "no-such-revision", apiRoles, everyProjectGenerates)
		assert.Errorf(t, err, "%s", b.name)
	})
}

// A base the repository lacks names the setting that declares the right one.
func TestParityRangeOwesNamesTheBaseSetting(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"README": "r\n"})
		head := initialCommitID(t, b, dir)

		_, err := RangeOwes(t.Context(), b.drv, dir, "no-such-base", head, apiRoles, everyProjectGenerates)
		require.Errorf(t, err, "%s", b.name)
		assert.Containsf(t, err.Error(), "set vcs.base_ref in magus.yaml or MAGUS_VCS_BASE_REF", "%s", b.name)
	})
}
