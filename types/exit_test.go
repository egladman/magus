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

// TestNormalizeExitCode pins the truncation guard: os.Exit keeps the low 8 bits, so an
// unclamped os.exit(256) reported success and `magus run x && deploy` deployed.
func TestNormalizeExitCode(t *testing.T) {
	for code, want := range map[int]int{
		0: 0, 1: 1, 2: 2, 255: 255,
		256:  1,   // truncates to 0; a failure must not read as success
		512:  1,   // same
		257:  1,   // truncates to a legal 1 already
		300:  44,  // 300 & 0xff
		-1:   255, // negatives fold the way a shell folds them
		-256: 1,
	} {
		assert.Equal(t, want, NormalizeExitCode(code), "NormalizeExitCode(%d)", code)
	}
}
