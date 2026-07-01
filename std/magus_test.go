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
// toward the dedicated method when args name a subcommand that has one, and stays
// quiet otherwise. The nested exec itself is allowed to fail — the warning is
// emitted before exec, so we only assert on the captured log.
func TestMagusCmdWarnsForTypedSubcommands(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantWarn bool
	}{
		{"describe warns", []string{"describe", "graph"}, true},
		{"run warns", []string{"run", "build"}, true},
		{"insight warns", []string{"insight", "report"}, true},
		{"doctor warns", []string{"doctor"}, true},
		{"status does not warn", []string{"status"}, false},
		{"affected does not warn", []string{"affected", "ci"}, false},
		{"empty args does not warn", []string{}, false},
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
			warnIfTypedSubcommand(context.Background(), tc.args)

			got := strings.Contains(buf.String(), "subcommand with a dedicated method")
			assert.Equal(t, tc.wantWarn, got, "warn mismatch (log=%q)", buf.String())
		})
	}
}
