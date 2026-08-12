//go:build !wasm

package std

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The whole point of this module's design: a prompt must be impossible to reach
// when nothing can answer it. Under `go test` stdin is not a terminal, so these
// exercise the real non-interactive path rather than a simulated one.

func TestTermPickRaisesWithoutATerminal(t *testing.T) {
	_, err := TermPick(context.Background(), []string{"a", "b"}, "pick one", "", 0, 0)
	require.Error(t, err, "pick must never block waiting for input that cannot arrive")
	// The message has to name the guard, not just the condition: someone hitting
	// this in CI needs to know what to write.
	assert.Contains(t, err.Error(), "isInteractive")
	assert.Contains(t, err.Error(), "terminals")
}

func TestTermPickRejectsAnEmptyList(t *testing.T) {
	_, err := TermPick(context.Background(), nil, "", "", 0, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no items")
}

func TestTermPickIsSkippedInRecordMode(t *testing.T) {
	// A dry run must never block on a human, and must not raise either - it
	// reports a plausible answer the way fs.temp_dir names a path it did not
	// create.
	idx, err := TermPick(types.WithTrace(context.Background()), []string{"a", "b"}, "", "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, idx)

	// Even with no items, because the record pass is not asserting a real choice.
	idx, err = TermPick(types.WithTrace(context.Background()), nil, "", "", 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, idx)
}

func TestTermIsInteractiveIsFalseUnderTest(t *testing.T) {
	got, err := TermIsInteractive(context.Background())
	require.NoError(t, err)
	assert.False(t, got, "the test harness is not a terminal, so this must report false")
}

func TestTermColorizeIsPassThroughWithoutATerminal(t *testing.T) {
	// Escape codes leaking into a CI log is the bug this prevents, so the
	// pass-through is the behaviour under test, not an implementation detail.
	got, err := TermColorize(context.Background(), "hello", string(types.TermRed))
	require.NoError(t, err)
	assert.Equal(t, "hello", got, "no terminal means no escape codes")

	// An unset style is pass-through regardless, so a computed style needs no branch.
	got, err = TermColorize(context.Background(), "hello", "")
	require.NoError(t, err)
	assert.Equal(t, "hello", got)
}

func TestTermSizeIsZeroWithoutATerminal(t *testing.T) {
	// Zero rather than an error: "there is no size" is an ordinary answer for a
	// pipe, and a caller laying out a line should not need a try/catch to ask.
	got, err := TermSizeOf(context.Background())
	require.NoError(t, err)
	assert.Equal(t, types.TermSize{}, got)
}

func TestTermClearScreenIsANoOpWithoutATerminal(t *testing.T) {
	// So a watch loop needs no guard around its repaint.
	require.NoError(t, TermClearScreen(context.Background()))
}

func TestTermStyleCasesAreTheRenderedCodes(t *testing.T) {
	// The enum names magus's own palette rather than inventing one; these are the
	// SGR codes internal/interactive/tty already emits.
	assert.Equal(t, types.TermStyle("1"), types.TermBold)
	assert.Equal(t, types.TermStyle("2;32"), types.TermDimGreen)
	assert.Equal(t, types.TermStyle("1;32"), types.TermBrightGreen)
	assert.True(t, types.TermRed.Valid())
	assert.False(t, types.TermStyle("nope").Valid())
	// The zero value is valid and means "no styling".
	assert.True(t, types.TermStyle("").Valid())
}
