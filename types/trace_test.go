package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithTraceRoundTrip(t *testing.T) {
	// A bare context is not tracing.
	assert.False(t, Tracing(context.Background()))

	// WithTrace marks it; Tracing reads it back.
	assert.True(t, Tracing(WithTrace(context.Background())))
}
