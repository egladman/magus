package dropin

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadSortsAndFiltersByExtension(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"zulu.json", "alpha.json", "notes.txt", ".tmp-123"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, f), []byte(f), 0o600))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub.json"), 0o700))

	got, err := Read(dir, "json")
	require.NoError(t, err)
	require.Len(t, got, 2, "only *.json files, and never a directory")
	assert.Equal(t, "alpha", got[0].Name)
	assert.Equal(t, "zulu", got[1].Name)
	assert.Equal(t, []byte("alpha.json"), got[0].Data)
}

// TestReadMissingDirIsEmptyNotAnError: nothing configured is a normal state.
func TestReadMissingDirIsEmptyNotAnError(t *testing.T) {
	got, err := Read(filepath.Join(t.TempDir(), "absent"), "json")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestReadReportsAnUnreadableEntry: skipping one would present a smaller
// configuration as though it were the whole one.
func TestReadReportsAnUnreadableEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "locked.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	_, err := Read(dir, "json")
	require.ErrorContains(t, err, "locked.json")
}

func TestPublishIsAtomicAndUnique(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Publish(dir, "one", "json", []byte(`{"a":1}`), 0o600))

	got, err := Read(dir, "json")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, `{"a":1}`, string(got[0].Data))

	info, err := os.Stat(got[0].Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	err = Publish(dir, "one", "json", []byte(`{"a":2}`), 0o600)
	require.ErrorIs(t, err, fs.ErrExist, "a taken name must not be silently overwritten")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "a failed publish must leave no temp file behind")
}

// TestPublishNeverExposesAPartialEntry is the property the temp-then-link dance
// exists for: a reader concurrent with a writer sees the entry whole or not at all.
func TestPublishNeverExposesAPartialEntry(t *testing.T) {
	dir := t.TempDir()
	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	reads := make([]error, n)
	for i := range n {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			errs[i] = Publish(dir, string(rune('a'+i)), "json", []byte(`{"payload":"a value long enough that a partial write would be visible"}`), 0o600)
		}(i)
		go func(i int) {
			defer wg.Done()
			entries, err := Read(dir, "json")
			if err != nil {
				reads[i] = err
				return
			}
			for _, e := range entries {
				if len(e.Data) == 0 {
					reads[i] = fs.ErrInvalid
				}
			}
		}(i)
	}
	wg.Wait()
	for i := range n {
		require.NoError(t, errs[i], "publish %d", i)
		require.NoError(t, reads[i], "read %d saw a partial entry", i)
	}

	got, err := Read(dir, "json")
	require.NoError(t, err)
	assert.Len(t, got, n, "every entry survived")
}

func TestValidNameRejectsPathComponents(t *testing.T) {
	for _, name := range []string{"", "../escape", "a/b", ".hidden", "has space", "sub/../x"} {
		require.Error(t, ValidName(name), "accepted %q", name)
	}
	for _, name := range []string{"claude-code", "a_b.c", "Node24"} {
		require.NoError(t, ValidName(name), "rejected %q", name)
	}
}
