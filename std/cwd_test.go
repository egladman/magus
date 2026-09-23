package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCwdHelpers covers the split between the two accessors: EffectiveCwd falls
// back to the process cwd so a host module always has a base, while
// CwdFromContext reports only a cwd a target actually established.
func TestCwdHelpers(t *testing.T) {
	dir := t.TempDir()
	ctx := WithCwd(context.Background(), dir)

	got, err := EffectiveCwd(ctx)
	require.NoError(t, err)
	assert.Equal(t, dir, got)

	base, ok := CwdFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, dir, base)

	base, ok = CwdFromContext(context.Background())
	assert.False(t, ok, "no target established a cwd, so there is none to report")
	assert.Empty(t, base)

	// An empty dir is a no-op rather than an erasure.
	assert.Equal(t, context.Background(), WithCwd(context.Background(), ""))

	process, err := EffectiveCwd(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, process, "without a context cwd the process cwd is the base")
}
