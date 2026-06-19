package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractNotices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.log")
	body := strings.Join([]string{
		"compiling foo",
		"magus:notice: deployed api v1.2.3",
		"  magus:notice:   indented and padded   ",
		"not important: regular line",
		"prefixed magus:notice: not at line start",
		"magus:notice:",
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	got := extractNotices(path)
	assert.Equal(t, []string{
		"deployed api v1.2.3",
		"indented and padded",
		"", // bare marker yields an empty message, still in order
	}, got)
}

func TestExtractNoticesMissingFile(t *testing.T) {
	assert.Nil(t, extractNotices(filepath.Join(t.TempDir(), "nope.log")))
}

func TestTailLines(t *testing.T) {
	data := []byte("l1\nl2\nl3\nl4\nl5\n")

	tail, omitted := tailLines(data, 2)
	assert.Equal(t, "l4\nl5\n", string(tail))
	assert.Equal(t, 3, omitted)

	tail, omitted = tailLines(data, 5)
	assert.Equal(t, string(data), string(tail))
	assert.Equal(t, 0, omitted)

	tail, omitted = tailLines(data, 99)
	assert.Equal(t, string(data), string(tail))
	assert.Equal(t, 0, omitted)

	tail, omitted = tailLines(data, 0)
	assert.Equal(t, string(data), string(tail))
	assert.Equal(t, 0, omitted)
}

func TestTailLinesNoTrailingNewline(t *testing.T) {
	data := []byte("a\nb\nc")
	tail, omitted := tailLines(data, 2)
	assert.Equal(t, "b\nc", string(tail))
	assert.Equal(t, 1, omitted)
}
