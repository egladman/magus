package std

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUUIDVersions pins the two identifier shapes apart. The version nibble at
// index 14 is what a caller reading a stored id uses to tell a random id from a
// time-ordered one, so it is the fact worth asserting rather than the length.
func TestUUIDVersions(t *testing.T) {
	ctx := context.Background()

	v4, err := UUIDv4(ctx)
	require.NoError(t, err)
	assert.Len(t, v4, 36)
	assert.Equal(t, byte('4'), v4[14], "v4 is the random version")

	v7, err := UUIDv7(ctx)
	require.NoError(t, err)
	assert.Len(t, v7, 36)
	assert.Equal(t, byte('7'), v7[14], "v7 is the time-ordered version")

	other, err := UUIDv4(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, v4, other, "two ids must never collide")
}

// TestUUIDv7SortsByCreationTime is the property that makes v7 a usable run id:
// two ids minted a millisecond apart compare in the order they were minted.
func TestUUIDv7SortsByCreationTime(t *testing.T) {
	ctx := context.Background()
	first, err := UUIDv7(ctx)
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	second, err := UUIDv7(ctx)
	require.NoError(t, err)
	assert.Less(t, first, second, "a later v7 must sort after an earlier one")
}

func TestUUIDRandomHex(t *testing.T) {
	ctx := context.Background()

	got, err := UUIDRandomHex(ctx, 8)
	require.NoError(t, err)
	assert.Len(t, got, 16, "n bytes render as 2n hex characters")
	assert.Equal(t, strings.ToLower(got), got, "the hex is lowercase")
	_, decodeErr := hex.DecodeString(got)
	assert.NoError(t, decodeErr)

	other, err := UUIDRandomHex(ctx, 8)
	require.NoError(t, err)
	assert.NotEqual(t, got, other)

	for _, n := range []int{0, -1} {
		_, err := UUIDRandomHex(ctx, n)
		require.Errorf(t, err, "randomHex(%d)", n)
		assert.Contains(t, err.Error(), "n must be positive")
	}
}

func TestUUIDRandomToken(t *testing.T) {
	ctx := context.Background()

	got, err := UUIDRandomToken(ctx, 16)
	require.NoError(t, err)
	assert.NotContains(t, got, "=", "the token is unpadded so it is safe in a URL")
	raw, decodeErr := base64.RawURLEncoding.DecodeString(got)
	require.NoError(t, decodeErr)
	assert.Len(t, raw, 16)

	for _, n := range []int{0, -1} {
		_, err := UUIDRandomToken(ctx, n)
		require.Errorf(t, err, "randomToken(%d)", n)
		assert.Contains(t, err.Error(), "n must be positive")
	}
}
