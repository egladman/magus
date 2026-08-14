package tty

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// No same-named source file, deliberately: this asserts one property across
// every type in the package at once, which is the point - a new entry point
// that forgets its gate fails here rather than in somebody's CI log.
//
// TestNothingIsWrittenToANonTerminal is the backstop for this whole package.
//
// Every type here exists to drive a terminal, and every one of them is supposed
// to be inert without one. Each has its own gate and its own test; this asserts
// the property across the whole surface at once, so a new entry point that
// forgets to check fails HERE rather than in somebody's CI log.
//
// A bytes.Buffer has no Fd(), which is the strongest form of "not a terminal" -
// stronger than a probe that says no, because no amount of probe confusion can
// make one appear.
func TestNothingIsWrittenToANonTerminal(t *testing.T) {
	t.Parallel()

	for name, drive := range map[string]func(t *testing.T, w *bytes.Buffer){
		"Zone lease": func(t *testing.T, w *bytes.Buffer) {
			z := NewZone(w, terminal(80, 24))
			l := z.Acquire(4)
			_, err := l.Set([]Line{{Text: "status", Style: SGRDim}})
			require.NoError(t, err)
			require.NoError(t, l.Release())
			require.NoError(t, z.Close())
		},
		"Zone with a probe that lies": func(t *testing.T, w *bytes.Buffer) {
			// Even an all-terminals probe cannot conjure a descriptor.
			z := NewZone(w, terminal(200, 60))
			l := z.Acquire(6)
			_, err := l.Set([]Line{{Text: "x"}})
			require.NoError(t, err)
		},
		"Notifier": func(t *testing.T, w *bytes.Buffer) {
			n := NewNotifier(NewZone(w, terminal(80, 24)), 3)
			require.NoError(t, n.Notify("built", SGRGreen, time.Second))
			require.NoError(t, n.Pin("lock", "waiting", SGRYellow))
			require.NoError(t, n.Clear("lock"))
			require.NoError(t, n.Close())
		},
		"region": func(t *testing.T, w *bytes.Buffer) {
			r := newRegion(w, 5, terminal(80, 24))
			require.NoError(t, r.reserve())
			require.NoError(t, r.render([]Line{{Text: "boom", Style: SGRBoldRed}}))
			require.NoError(t, r.release())
		},
		"ClearScreen and margin reset": func(t *testing.T, w *bytes.Buffer) {
			require.NoError(t, ResetScrollMargins(w, terminal(80, 24)))
			require.NoError(t, ResetMouseTracking(w, terminal(80, 24)))
		},
		"EraseLines": func(t *testing.T, w *bytes.Buffer) {
			// The one exception, and it is honest about it: EraseLines has no
			// probe and is only ever called by the picker, which has already
			// established it owns a terminal. Asserted so the exception is
			// deliberate rather than discovered.
			require.NoError(t, EraseLines(w, 0))
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			drive(t, &buf)
			assert.Empty(t, buf.String(), "no bytes may reach a writer with no descriptor")
		})
	}
}

// TestOpenInputRefusesEveryNonTerminalCombination pins the other half: input
// capture needs BOTH ends, because a session that can paint but never hear a
// key hangs, and one that hears keys it cannot answer is worse.
func TestOpenInputRefusesEveryNonTerminalCombination(t *testing.T) {
	t.Parallel()
	var plain bytes.Buffer
	var termish ttyBuf

	// Display is not a terminal.
	_, err := OpenInput(os.Stdin, &plain, terminal(80, 24))
	assert.ErrorIs(t, err, ErrNotATerminal)

	// Neither end is.
	_, err = OpenInput(os.Stdin, &termish, notATerminal())
	assert.ErrorIs(t, err, ErrNotATerminal)

	assert.Empty(t, plain.String(), "a refusal must not leave tracking enabled")
	assert.Empty(t, termish.String())
}

// TestHyperlinkIsNeverEmittedOffATerminal guards the newest escape-emitting
// path, which is the one most likely to leak: a hyperlink is invisible when it
// works, so a missing gate shows up only as garbage in somebody's log.
func TestHyperlinkIsNeverEmittedOffATerminal(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("TERM", "xterm-256color")
	assert.False(t, WantsHyperlinks(&buf, terminal(80, 24)),
		"a writer with no descriptor gets no link, however capable TERM claims to be")
}

// TestNothingIsRenderedOnADumbTerminal is the emacs shell-mode case, and it is
// the one a descriptor check alone gets wrong.
//
// TERM=dumb declares a terminal that understands no escape sequences, but the
// pty behind emacs shell-mode IS a terminal - so every "is this a tty" check
// says yes, and the cursor addressing and scroll margins go out to something
// that renders them as literal garbage.
func TestNothingIsRenderedOnADumbTerminal(t *testing.T) {
	t.Setenv("TERM", "dumb")
	var buf ttyBuf // has an Fd, and the probe calls it a terminal

	assert.False(t, CanRender(&buf, terminal(80, 24)))
	assert.False(t, WantsColor(&buf, terminal(80, 24)), "this is what the docs always claimed")
	assert.False(t, WantsHyperlinks(&buf, terminal(80, 24)))

	assert.False(t, newRegion(&buf, 5, terminal(80, 24)).isEnabled())

	z := NewZone(&buf, terminal(80, 24))
	l := z.Acquire(5)
	assert.False(t, l.Enabled())
	_, err := l.Set([]Line{{Text: "status"}})
	require.NoError(t, err)

	n := NewNotifier(z, 3)
	require.NoError(t, n.Notify("built", SGRGreen, time.Second))
	require.NoError(t, n.Pin("lock", "waiting", SGRYellow))

	_, err = OpenInput(os.Stdin, &buf, terminal(80, 24))
	assert.ErrorIs(t, err, ErrNotATerminal, "and no raw mode or mouse tracking either")

	assert.Empty(t, buf.String(), "not one byte may reach a terminal that cannot render it")
}

// TestACapableTerminalStillRenders is the control: the gate above must not have
// turned everything off everywhere.
func TestACapableTerminalStillRenders(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	var buf ttyBuf
	assert.True(t, CanRender(&buf, terminal(80, 24)))

	z := NewZone(&buf, terminal(80, 24))
	l := z.Acquire(5)
	require.True(t, l.Enabled())
	rendered, err := l.Set([]Line{{Text: "status", Style: SGRDim}})
	require.NoError(t, err)
	assert.True(t, rendered)
	assert.Contains(t, buf.String(), "status")
}
