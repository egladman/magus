package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCwdHelpers covers the split between the two accessors: EffectiveCurrentWorkingDirectory falls
// back to the process cwd so a host module always has a base, while
// CurrentWorkingDirectoryFromContext reports only a cwd a target actually established.
func TestCwdHelpers(t *testing.T) {
	dir := t.TempDir()
	ctx := WithCurrentWorkingDirectory(context.Background(), dir)

	got, err := EffectiveCurrentWorkingDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, dir, got)

	base, ok := CurrentWorkingDirectoryFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, dir, base)

	base, ok = CurrentWorkingDirectoryFromContext(context.Background())
	assert.False(t, ok, "no target established a cwd, so there is none to report")
	assert.Empty(t, base)

	// An empty dir is a no-op rather than an erasure.
	assert.Equal(t, context.Background(), WithCurrentWorkingDirectory(context.Background(), ""))

	process, err := EffectiveCurrentWorkingDirectory(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, process, "without a context cwd the process cwd is the base")
}
