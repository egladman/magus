package origin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOriginRoundTrip(t *testing.T) {
	o := Origin{Agent: "claude-desktop/0.7.2"}
	ctx := WithContext(context.Background(), o)
	got, ok := FromContext(ctx)
	require.True(t, ok, "FromContext returned ok=false after WithContext")
	assert.Equal(t, o, got)
}

func TestOriginFromContext_EmptyContext(t *testing.T) {
	_, ok := FromContext(context.Background())
	assert.False(t, ok, "FromContext on plain context should return ok=false")
}

func TestOriginFromContext_InnerShadowsOuter(t *testing.T) {
	outer := WithContext(context.Background(), Origin{Agent: "outer"})
	inner := WithContext(outer, Origin{Agent: "inner"})
	got, ok := FromContext(inner)
	require.True(t, ok)
	assert.Equal(t, Origin{Agent: "inner"}, got)
}
