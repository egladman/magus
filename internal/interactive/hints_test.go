package interactive

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/stretchr/testify/assert"
)

// resetEmitted gives the test its own dedupe set.
func resetEmitted(t *testing.T) {
	t.Helper()
	emittedMu.Lock()
	prev := emitted
	emitted = make(map[string]struct{})
	emittedMu.Unlock()
	t.Cleanup(func() {
		emittedMu.Lock()
		emitted = prev
		emittedMu.Unlock()
	})
}

// captureHints logs the default slog handler to a buffer for the test.
func captureHints(t *testing.T) *bytes.Buffer {
	t.Helper()
	resetEmitted(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// Hint dedupes by text, and a repeated run reuses the name.
func TestHintLogsAHintRecordOnce(t *testing.T) {
	buf := captureHints(t)
	msg := "pass --force to replace it: " + t.Name()
	Hint(t.Context(), msg, attr.Component("spell"))
	Hint(t.Context(), msg)
	assert.Equal(t, fmt.Sprintf("level=INFO msg=\"%s\" component=spell notice=hint\n", msg), buf.String())
}

func TestHintLogsNothingWithHintsDisabled(t *testing.T) {
	buf := captureHints(t)
	SetHintsEnabled(false)
	t.Cleanup(func() { SetHintsEnabled(true) })
	msg := "try `magus run` instead: " + t.Name()
	Hint(t.Context(), msg)
	assert.Empty(t, buf.String())

	SetHintsEnabled(true)
	Hint(t.Context(), msg)
	assert.Contains(t, buf.String(), "notice=hint", "a hint withheld while disabled is still shown once enabled")
}

// A handler that refuses info leaves the showing unspent for a later display.
func TestHintKeepsItsShowingWhenTheHandlerRefusesIt(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError})))
	msg := "read that failure with magus query output: " + t.Name()
	Hint(t.Context(), msg)

	buf := captureHints(t)
	Hint(t.Context(), msg)
	assert.Contains(t, buf.String(), "notice=hint")
}
