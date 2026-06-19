package std

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFsReadWriteLines(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")

	require.NoError(t, FsWriteLines(ctx, p, []string{"a", "b", "c"}))

	// Written with a trailing newline; read back without a spurious empty line.
	got, err := FsReadLines(ctx, p)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, got)

	// Round-trips a newline-terminated file unchanged.
	require.NoError(t, FsWriteLines(ctx, p, got))
	again, err := FsReadLines(ctx, p)
	require.NoError(t, err)
	assert.Equal(t, got, again)

	// Empty list writes an empty file, which reads back as an empty list (not [""]).
	require.NoError(t, FsWriteLines(ctx, p, nil))
	empty, err := FsReadLines(ctx, p)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
