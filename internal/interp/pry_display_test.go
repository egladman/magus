package interp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestColorEnabledForFile_Nil(t *testing.T) {
	assert.False(t, ColorEnabledForFile(nil))
}

func TestPrintSourceContext_NonexistentFile(t *testing.T) {
	var sb strings.Builder
	PrintSourceContext(&sb, "/no/such/file/xyz.go", 1, 2, false)
	assert.Contains(t, sb.String(), "cannot read source")
}

func TestPrintSourceContext_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	var sb strings.Builder
	PrintSourceContext(&sb, path, 3, 1, false)
	out := sb.String()
	assert.Contains(t, out, "line2")
	assert.Contains(t, out, "line3")
	assert.Contains(t, out, "line4")
}

func TestPrintSourceContext_ClampsToFileBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	require.NoError(t, os.WriteFile(path, []byte("a\nb\n"), 0o644))
	var sb strings.Builder
	// Radius extends past both ends; output must clamp without panicking.
	PrintSourceContext(&sb, path, 1, 10, false)
	out := sb.String()
	assert.Contains(t, out, "a")
	assert.Contains(t, out, "b")
}
