package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEffectiveClaimsRoundTrip_Nil(t *testing.T) {
	ctx := WithEffectiveClaims(context.Background(), nil)
	assert.Empty(t, EffectiveClaimsFromContext(ctx))
}

func TestEffectiveClaimsRoundTrip_Single(t *testing.T) {
	ctx := WithEffectiveClaims(context.Background(), []string{"go"})
	assert.Equal(t, []string{"go"}, EffectiveClaimsFromContext(ctx))
}

func TestEffectiveClaimsRoundTrip_Multiple(t *testing.T) {
	ctx := WithEffectiveClaims(context.Background(), []string{"go", "python", "node"})
	assert.Equal(t, []string{"go", "python", "node"}, EffectiveClaimsFromContext(ctx))
}

func TestEffectiveClaimsFromContext_MissingReturnsNil(t *testing.T) {
	assert.Nil(t, EffectiveClaimsFromContext(context.Background()))
}

func TestEffectiveClaimsFromContext_InnerContextWins(t *testing.T) {
	outer := WithEffectiveClaims(context.Background(), []string{"go"})
	inner := WithEffectiveClaims(outer, []string{"python"})
	assert.Equal(t, []string{"python"}, EffectiveClaimsFromContext(inner))
}
