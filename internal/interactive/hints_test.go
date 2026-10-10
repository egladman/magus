package interactive

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/stretchr/testify/assert"
)

// captureHints points slog's default at a text handler on a buffer for the test.
func captureHints(t *testing.T) *bytes.Buffer {
	t.Helper()
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

// Hint dedupes by text across the whole process, so each test names its own message.
func TestHintLogsAHintRecordOnce(t *testing.T) {
	buf := captureHints(t)
	msg := "pass --force to replace it: " + t.Name()
	Hint(t.Context(), msg, attr.Component("spell"))
	Hint(t.Context(), msg)
	assert.Equal(t, "level=INFO msg=\""+msg+"\" component=spell notice=hint\n", buf.String())
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
