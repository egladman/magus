package tty

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMakeRawFailsOnANonTerminal covers the path every caller actually
// hits in CI: no pty, so raw mode is refused. The contract that matters
// is that the error is wrapped and no restore closure is handed back to
// be deferred against a terminal that was never modified.
func TestMakeRawFailsOnANonTerminal(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "notatty")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	restore, err := MakeRaw(f.Fd())
	require.Error(t, err, "a regular file cannot enter raw mode")
	assert.Nil(t, restore, "no restore closure when the terminal was never changed")
	assert.Contains(t, err.Error(), "tty: enter raw mode", "the error names the operation")
}

// TestMakeRawRoundTripsOnARealTerminal only runs where the test binary
// actually has one. It is skipped in CI rather than deleted, because the
// round trip is the behaviour worth having recorded when someone runs
// the suite from a terminal.
func TestMakeRawRoundTripsOnARealTerminal(t *testing.T) {
	if !SystemProbe.IsTerminal(os.Stdin.Fd()) {
		t.Skip("stdin is not a terminal")
	}
	restore, err := MakeRaw(os.Stdin.Fd())
	require.NoError(t, err)
	require.NotNil(t, restore)
	assert.NoError(t, restore(), "the terminal must be restorable")
}
