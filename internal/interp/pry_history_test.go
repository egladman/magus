package interp

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendAndLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hist")

	h, err := Open(path, 5)
	require.NoError(t, err)

	h.Append("one")
	h.Append("two")
	h.Append("two") // duplicate of previous → skipped
	h.Append("three")

	lines := h.Lines()
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"one", "two", "three"}, lines)
}

func TestRecall(t *testing.T) {
	dir := t.TempDir()
	h, err := Open(filepath.Join(dir, "hist"), 0)
	require.NoError(t, err)
	h.Append("first")
	h.Append("second")
	h.Append("third")

	assert.Equal(t, "third", h.Recall(1))
	assert.Equal(t, "first", h.Recall(3))
	assert.Empty(t, h.Recall(99))
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hist")

	h, _ := Open(path, 0)
	h.Append("alpha")
	h.Append("beta")

	// New History opened against the same file should pick the lines back up.
	h2, err := Open(path, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "beta"}, h2.Lines())
}

func TestCapOverflowTrims(t *testing.T) {
	dir := t.TempDir()
	h, _ := Open(filepath.Join(dir, "hist"), 3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		h.Append(s)
	}
	assert.Equal(t, []string{"c", "d", "e"}, h.Lines())
}
