package cache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLimiterFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, LimiterFromContext(ctx), "empty context: LimiterFromContext should be nil")
}

func TestFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, FromContext(ctx), "empty context: FromContext should be nil")

	cacheDir := t.TempDir()
	c, err := Open(t.Context(), cacheDir, WithLocalWrite(false))
	require.NoError(t, err)
	ctx = NewContext(ctx, c)
	assert.Equal(t, c, FromContext(ctx))
}
