package types

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitCapture(t *testing.T) {
	ctx, read := WithExitCapture(context.Background())
	_, ok := read()
	require.False(t, ok, "capture empty before CaptureExit")

	CaptureExit(ctx, 5)
	code, ok := read()
	assert.True(t, ok)
	assert.Equal(t, 5, code)

	// CaptureExit on a ctx without a capture is a no-op (must not panic).
	assert.NotPanics(t, func() { CaptureExit(context.Background(), 9) })
}

func TestExitError(t *testing.T) {
	var err error = ExitError{Code: 3}
	assert.Equal(t, "exit 3", err.Error())

	// Must be recoverable via errors.As so the CLI/daemon can read the code
	// after it propagates wrapped up from a target.
	wrapped := errors.Join(errors.New("magusfile: target ci"), ExitError{Code: 2})
	var ex ExitError
	require.ErrorAs(t, wrapped, &ex)
	assert.Equal(t, 2, ex.Code)
}
