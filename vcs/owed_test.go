package vcs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
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
	require.ErrorContains(t, err, "is magus.owed/v1alpha99, newer than the magus.owed/v1alpha1 this magus reads")
}

type PreFoldOwedSuite struct {
	suite.Suite
	dir, path string
}

func TestPreFoldOwedSuite(t *testing.T) {
	suite.Run(t, new(PreFoldOwedSuite))
}

func (s *PreFoldOwedSuite) SetupTest() {
	s.dir = testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n"}).Root()
	path, err := OwedRegenerationPath(s.T().Context(), s.dir)
	s.Require().NoError(err)
	s.path = path
	older := `{"schema_version": 1, "owed": [{"project": ".", "target": "generate", "paths": ["gen/a.md"]}]}`
	s.Require().NoError(os.WriteFile(path, []byte(older), 0o644))
}

func (s *PreFoldOwedSuite) TestReadsSchemaVersionOneAsV1alpha1() {
	got, err := OwedRegenerations(s.T().Context(), s.dir)
	s.Require().NoError(err)
	s.Equal([]OwedRegeneration{{Project: ".", Target: "generate", Paths: []string{"gen/a.md"}}}, got)
}

func (s *PreFoldOwedSuite) TestRewriteWritesOnlyTheAPIVersion() {
	_, err := RecordOwedRegeneration(s.T().Context(), s.dir, OwedRegeneration{Project: "docs", Target: "generate", Paths: []string{"x"}})
	s.Require().NoError(err)
	b, err := os.ReadFile(s.path)
	s.Require().NoError(err)
	s.Contains(string(b), `"api_version": "magus.owed/v1alpha1"`)
	s.NotContains(string(b), "schema_version")
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
