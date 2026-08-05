package std

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMagusCmdWarnsForTypedSubcommands verifies the escape hatch nudges authors
// toward the dedicated method when the subcommand has one, and stays quiet otherwise. The nested exec itself is allowed to fail — the warning is
// emitted before exec, so we only assert on the captured log.
func TestMagusCmdWarnsForTypedSubcommands(t *testing.T) {
	cases := []struct {
		name     string
		sub      string
		wantWarn bool
	}{
		{"describe warns", "describe", true},
		{"run warns", "run", true},
		{"insight warns", "insight", true},
		{"doctor warns", "doctor", true},
		{"status does not warn", "status", false},
		{"affected does not warn", "affected", false},
		{"no subcommand does not warn", "", false},
		// The subcommand is its own argument now, so a value that merely CONTAINS a
		// typed name is not one: only an exact match is the escape hatch being misused.
		{"a longer name that starts with one does not warn", "runner", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// slog.SetDefault mutates global state — subtests cannot run in parallel.
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			// Test the pure decision half directly — calling MagusCmd would exec the
			// test binary (a fork-bomb risk), and the warning is what we care about.
			warnIfTypedSubcommand(context.Background(), tc.sub)

			got := strings.Contains(buf.String(), "subcommand with a dedicated method")
			assert.Equal(t, tc.wantWarn, got, "warn mismatch (log=%q)", buf.String())
		})
	}
}

// TestResolveRunDir covers where a nested magus runs. opts.dir is resolved relative to
// the contextual project dir, matching os.exec's dir, so a magusfile can send a nested
// invocation to a sibling directory without reaching for os.exec on the magus binary -
// the thing magus warns about, and which it could not previously offer an alternative to.
func TestResolveRunDir(t *testing.T) {
	cases := []struct {
		name string
		cwd  string
		opts map[string]any
		want string
	}{
		{"no opts keeps the contextual dir", "/ws/api", nil, "/ws/api"},
		{"empty dir keeps the contextual dir", "/ws/api", map[string]any{"dir": ""}, "/ws/api"},
		{"relative dir joins onto it", "/ws/api", map[string]any{"dir": "scripts"}, "/ws/api/scripts"},
		{"parent-relative dir joins too", "/ws/api", map[string]any{"dir": "../web"}, "/ws/web"},
		{"absolute dir wins outright", "/ws/api", map[string]any{"dir": "/elsewhere"}, "/elsewhere"},
		{"no contextual dir uses opts.dir as given", "", map[string]any{"dir": "scripts"}, "scripts"},
		{"a non-string dir is ignored", "/ws/api", map[string]any{"dir": 7}, "/ws/api"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.cwd != "" {
				ctx = WithCwd(ctx, tc.cwd)
			}
			assert.Equal(t, tc.want, resolveRunDir(ctx, tc.opts))
		})
	}
}
