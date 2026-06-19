package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvRequire(t *testing.T) {
	ctx := context.Background()

	t.Setenv("MAGUS_TEST_REQUIRE", "value")
	v, err := EnvRequire(ctx, "MAGUS_TEST_REQUIRE")
	require.NoError(t, err)
	assert.Equal(t, "value", v)

	// Set-but-empty satisfies the requirement (returns the empty value).
	t.Setenv("MAGUS_TEST_REQUIRE_EMPTY", "")
	v, err = EnvRequire(ctx, "MAGUS_TEST_REQUIRE_EMPTY")
	require.NoError(t, err)
	assert.Equal(t, "", v)

	// Unset raises.
	_, err = EnvRequire(ctx, "MAGUS_TEST_REQUIRE_MISSING_XYZ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not set")
}
