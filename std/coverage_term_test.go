//go:build !wasm

package std

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTermWantsColorIsFalseWithoutATerminal: the test binary's stderr is a pipe,
// so styled output is off and colorize is pass-through. A caller reads this only
// to make a WIDER rendering choice - a box-drawing table versus a plain one.
func TestTermWantsColorIsFalseWithoutATerminal(t *testing.T) {
	got, err := TermWantsColor(context.Background())
	require.NoError(t, err)
	assert.False(t, got)
}

// TestTermNotifyNeverRaises is the whole contract: a notification is a VIEW, so
// every way it can fail to be shown - no terminal, no room, an empty message, a
// recording pass - is a silent drop. Making a magusfile guard it would be a tax
// paid at every call site for a condition no author can act on.
func TestTermNotifyNeverRaises(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		ctx     context.Context
		message string
		level   string
		ttlMs   int
	}{
		{name: "default ttl", ctx: ctx, message: "built", level: "info"},
		{name: "explicit ttl", ctx: ctx, message: "built", level: "warn", ttlMs: 250},
		{name: "a negative ttl pins the notification", ctx: ctx, message: "built", level: "error", ttlMs: -1},
		{name: "an unknown level is not an error", ctx: ctx, message: "built", level: "shouting"},
		{name: "an empty message is a no-op", ctx: ctx},
		{name: "a recording pass paints nothing", ctx: types.WithTrace(ctx), message: "built", level: "info"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.NoError(t, TermNotify(tc.ctx, tc.message, tc.level, tc.ttlMs))
		})
	}
}

// TestNotifyStyle maps a severity onto the palette magus already renders with.
// LogLevel rather than a term-specific enum: a notification's severity is the
// same question log.at asks, and two spellings of "warn" would be one too many.
func TestNotifyStyle(t *testing.T) {
	for _, tc := range []struct {
		level types.LogLevel
		want  tty.SGR
	}{
		{types.LogError, tty.SGRRed},
		{types.LogWarn, tty.SGRYellow},
		{types.LogTrace, tty.SGRDim},
		{types.LogDebug, tty.SGRDim},
		{types.LogInfo, ""},
		{"", ""},
		{"shouting", ""},
	} {
		assert.Equalf(t, tc.want, notifyStyle(tc.level), "notifyStyle(%q)", string(tc.level))
	}
}
