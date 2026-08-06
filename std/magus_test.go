package std

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestMagusRaise covers the contract a magusfile author depends on: the code and url
// survive onto the caught value as fields, and the MGS namespace is closed to them.
func TestMagusRaise(t *testing.T) {
	t.Run("carries code, message and url onto the caught value", func(t *testing.T) {
		err := MagusRaise(context.Background(), "ACME1001", "registry rejected the manifest", nil, "https://acme.dev/codes/ACME1001")
		require.Error(t, err)

		var de *diagnostics.Error
		require.ErrorAs(t, err, &de, "must be a diagnostics.Error so Buzz sees structured fields")
		assert.Equal(t, map[string]string{
			"code":    "ACME1001",
			"message": "registry rejected the manifest",
			"url":     "https://acme.dev/codes/ACME1001",
		}, de.BuzzError())
		assert.Equal(t, "[ACME1001] registry rejected the manifest\n  see: https://acme.dev/codes/ACME1001", de.Error())
	})

	t.Run("omits url when none was supplied", func(t *testing.T) {
		err := MagusRaise(context.Background(), "ACME1002", "no url", nil, "")
		var de *diagnostics.Error
		require.ErrorAs(t, err, &de)
		assert.Equal(t, map[string]string{"code": "ACME1002", "message": "no url"}, de.BuzzError(),
			"an absent url must be absent, not an empty field a caller has to test for")
	})

	// The whole point of refusing MGS: a workspace code must never render like magus's
	// own, because explain and the docs URL map resolve MGS against a closed catalog.
	for _, code := range []string{"MGS9999", "mgs1001", "Mgs0001"} {
		t.Run("refuses the magus namespace: "+code, func(t *testing.T) {
			err := MagusRaise(context.Background(), code, "impersonating magus", nil, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "MGS namespace")
			var de *diagnostics.Error
			assert.NotErrorIs(t, err, diagnostics.ErrSentinel, "the refusal is a usage error, not a minted diagnostic")
			assert.False(t, errors.As(err, &de), "refusing must not produce the very diagnostic it rejected")
		})
	}

	t.Run("requires a code and a message", func(t *testing.T) {
		assert.ErrorContains(t, MagusRaise(context.Background(), "", "msg", nil, ""), "needs a code")
		assert.ErrorContains(t, MagusRaise(context.Background(), "ACME1001", "", nil, ""), "needs a message")
	})
}

// TestMagusRaiseCause covers wrapping: the cause has to stay reachable for errors.Is and
// stay VISIBLE in the rendered message, which Wrapf alone does not do.
func TestMagusRaiseCause(t *testing.T) {
	t.Run("splices a string cause and unwraps to it", func(t *testing.T) {
		err := MagusRaise(context.Background(), "ACME1001", "push failed", "exec docker: exit 1", "")
		assert.EqualError(t, err, "[ACME1001] push failed: exec docker: exit 1")
		assert.ErrorContains(t, errors.Unwrap(err), "exec docker: exit 1")
	})

	// The realistic shape: an inner catch hands back the map BuzzError built, and the
	// inner code must keep matching once the outer one wraps it.
	t.Run("keeps a coded cause matchable underneath the new code", func(t *testing.T) {
		inner := map[string]any{"code": "MGS2001", "message": "spell not found", "url": "https://x/MGS2001"}
		err := MagusRaise(context.Background(), "ACME1001", "build failed", inner, "")
		assert.EqualError(t, err, "[ACME1001] build failed: [MGS2001] spell not found")
		assert.ErrorIs(t, err, diagnostics.Code("MGS2001"),
			"the inner code must still match after wrapping, which is the point of a cause")
	})

	t.Run("an absent cause changes nothing", func(t *testing.T) {
		for _, empty := range []any{nil, "", map[string]any{}} {
			err := MagusRaise(context.Background(), "ACME1001", "plain", empty, "")
			assert.EqualError(t, err, "[ACME1001] plain")
		}
	})
}
