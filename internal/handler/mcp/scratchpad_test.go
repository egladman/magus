package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScratchpadOpReadEmpty pins that reading a scratchpad that was never written is
// not an error: it returns an empty content string and a zero byte count.
func TestScratchpadOpReadEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scratch")
	res, err := scratchpadOp(dir, "read", "")
	require.NoError(t, err)
	assert.Equal(t, scratchpadResult{Op: "read"}, res)
	// The read op must not create the directory as a side effect.
	_, statErr := os.Stat(dir)
	assert.True(t, os.IsNotExist(statErr), "read must not create the scratch dir")
}

// TestScratchpadOpWriteReadBack drives write then read: write creates the file
// (0644) under a freshly created scratch dir, and read returns exactly what was
// written.
func TestScratchpadOpWriteReadBack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scratch")

	res, err := scratchpadOp(dir, "write", "first note")
	require.NoError(t, err)
	assert.Equal(t, scratchpadResult{Op: "write", Content: "first note", Bytes: 10}, res)

	info, err := os.Stat(filepath.Join(dir, "scratchpad.md"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	got, err := scratchpadOp(dir, "read", "")
	require.NoError(t, err)
	assert.Equal(t, scratchpadResult{Op: "read", Content: "first note", Bytes: 10}, got)
}

// TestScratchpadOpWriteOverwrites pins that write replaces, not extends.
func TestScratchpadOpWriteOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scratch")
	_, err := scratchpadOp(dir, "write", "one")
	require.NoError(t, err)
	res, err := scratchpadOp(dir, "write", "two")
	require.NoError(t, err)
	assert.Equal(t, "two", res.Content)
}

// TestScratchpadOpAppendNewlineHandling covers the separator rule: a newline joins
// old and new content only when the existing content is non-empty and lacks a
// trailing newline. Appending to nothing writes content verbatim.
func TestScratchpadOpAppendNewlineHandling(t *testing.T) {
	// Append to a missing file: content lands verbatim, no leading newline.
	dir := filepath.Join(t.TempDir(), "scratch")
	res, err := scratchpadOp(dir, "append", "a")
	require.NoError(t, err)
	assert.Equal(t, "a", res.Content)

	// Existing has no trailing newline: a separator newline is inserted.
	res, err = scratchpadOp(dir, "append", "b")
	require.NoError(t, err)
	assert.Equal(t, "a\nb", res.Content)
	assert.Equal(t, 3, res.Bytes)

	// Existing already ends in a newline: no extra separator is added.
	dir2 := filepath.Join(t.TempDir(), "scratch")
	_, err = scratchpadOp(dir2, "write", "line\n")
	require.NoError(t, err)
	res, err = scratchpadOp(dir2, "append", "next")
	require.NoError(t, err)
	assert.Equal(t, "line\nnext", res.Content)
}

// TestScratchpadOpClear removes the file and reports Cleared; a subsequent read is
// empty, and clearing a missing file is a no-op success.
func TestScratchpadOpClear(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scratch")
	_, err := scratchpadOp(dir, "write", "some content")
	require.NoError(t, err)

	res, err := scratchpadOp(dir, "clear", "")
	require.NoError(t, err)
	assert.Equal(t, scratchpadResult{Op: "clear", Cleared: true}, res)

	got, err := scratchpadOp(dir, "read", "")
	require.NoError(t, err)
	assert.Equal(t, scratchpadResult{Op: "read"}, got)

	// Clearing again (file already gone) still succeeds.
	res, err = scratchpadOp(dir, "clear", "")
	require.NoError(t, err)
	assert.Equal(t, scratchpadResult{Op: "clear", Cleared: true}, res)
}

// TestScratchpadOpUnknown pins the validation error for an unrecognized op, and that
// it fails before any directory is created.
func TestScratchpadOpUnknown(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scratch")
	_, err := scratchpadOp(dir, "delete", "x")
	assert.ErrorContains(t, err, "scratchpad op must be one of read, write, append, clear")
	_, statErr := os.Stat(dir)
	assert.True(t, os.IsNotExist(statErr), "an invalid op must not create the scratch dir")
}

// TestScratchpadToolName pins the tool's registered name, consistent with the other
// name tests in this package.
func TestScratchpadToolName(t *testing.T) {
	assert.Equal(t, "magus_scratchpad", (&scratchpadTool{}).Name())
}

// TestRegistryHasScratchpadDriver pins that magus_scratchpad is described in the
// Registry; registerTools panics if a descriptor lacks a driver, so this plus the
// allMCPTools entry keep the pair in sync.
func TestRegistryHasScratchpadDriver(t *testing.T) {
	var described bool
	for _, d := range Registry {
		if d.Name == "magus_scratchpad" {
			described = true
		}
	}
	assert.True(t, described, "magus_scratchpad missing from Registry")
}
