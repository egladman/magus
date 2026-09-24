package origin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientRoundTrip(t *testing.T) {
	c := Client{Name: "claude-desktop/0.7.2"}
	ctx := WithContext(context.Background(), c)
	got, ok := FromContext(ctx)
	require.True(t, ok, "FromContext returned ok=false after WithContext")
	assert.Equal(t, c, got)
}

func TestClientFromContext_EmptyContext(t *testing.T) {
	_, ok := FromContext(context.Background())
	assert.False(t, ok, "FromContext on plain context should return ok=false")
}

func TestClientFromContext_InnerShadowsOuter(t *testing.T) {
	outer := WithContext(context.Background(), Client{Name: "outer"})
	inner := WithContext(outer, Client{Name: "inner"})
	got, ok := FromContext(inner)
	require.True(t, ok)
	assert.Equal(t, Client{Name: "inner"}, got)
}
