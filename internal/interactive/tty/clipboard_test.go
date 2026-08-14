package tty

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCopyEmitsExactlyWhatWasAskedFor is the property the whole feature rests
// on: what lands on the clipboard is the text, not the terminal's picture of it.
// No frame, no padding, no divider, no escape sequences.
func TestCopyEmitsExactlyWhatWasAskedFor(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	want := "--- FAIL: TestThing\n    thing_test.go:42: boom\n"
	n, err := Copy(&b, want)
	require.NoError(t, err)
	assert.Equal(t, len(want), n)

	out := b.String()
	require.True(t, strings.HasPrefix(out, "\x1b]52;c;"), "an OSC 52 clipboard write")
	payload := strings.TrimSuffix(strings.TrimPrefix(out, "\x1b]52;c;"), "\x07")
	got, err := base64.StdEncoding.DecodeString(payload)
	require.NoError(t, err)
	assert.Equal(t, want, string(got), "byte for byte, with nothing the band added")
}

// TestCopyKeepsTheTailWhenOversized: terminals cap the sequence and typically
// drop the WHOLE thing when it is over, so a copy that silently did nothing
// would be worse than one that took the end - which is the part a failure puts
// its reason in.
func TestCopyKeepsTheTailWhenOversized(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	huge := strings.Repeat("x", clipboardLimit) + "THE-END"
	n, err := Copy(&b, huge)
	require.NoError(t, err)
	assert.Equal(t, clipboardLimit, n)

	payload := strings.TrimSuffix(strings.TrimPrefix(b.String(), "\x1b]52;c;"), "\x07")
	got, _ := base64.StdEncoding.DecodeString(payload)
	assert.True(t, strings.HasSuffix(string(got), "THE-END"), "the tail survives, not the head")
}

// TestCopyWritesNothingForNothing: an empty selection must not emit a sequence
// that would clear the reader's clipboard.
func TestCopyWritesNothingForNothing(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	n, err := Copy(&b, "")
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, b.String(), "an empty copy must not clobber what was there")
}
