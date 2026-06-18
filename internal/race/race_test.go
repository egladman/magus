package race

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRuntime_NonNil(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	require.NotNil(t, rt)
}

func TestRuntimeContext_RoundTrip(t *testing.T) {
	rt := NewRuntime(t.TempDir())
	ctx := WithRuntime(context.Background(), rt)
	got := RuntimeFromContext(ctx)
	assert.Same(t, rt, got)
}

func TestRuntimeFromContext_Empty(t *testing.T) {
	assert.Nil(t, RuntimeFromContext(context.Background()))
}
