package std

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/types"
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
		{"insight does not warn", "insight", false},
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
// the contextual project dir, matching proc.exec's dir, so a magusfile can send a nested
// invocation to a sibling directory without reaching for proc.exec on the magus binary -
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
		err := MagusRaise(context.Background(), "ACME1001", "registry rejected the manifest", raiseOpts(nil, "https://acme.dev/codes/ACME1001"))
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
		err := MagusRaise(context.Background(), "ACME1002", "no url", raiseOpts(nil, ""))
		var de *diagnostics.Error
		require.ErrorAs(t, err, &de)
		assert.Equal(t, map[string]string{"code": "ACME1002", "message": "no url"}, de.BuzzError(),
			"an absent url must be absent, not an empty field a caller has to test for")
	})

	// The whole point of refusing MGS: a workspace code must never render like magus's
	// own, because explain and the docs URL map resolve MGS against a closed catalog.
	for _, code := range []string{"MGS9999", "mgs1001", "Mgs0001"} {
		t.Run("refuses the magus namespace: "+code, func(t *testing.T) {
			err := MagusRaise(context.Background(), code, "impersonating magus", nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "MGS namespace")
			var de *diagnostics.Error
			assert.NotErrorIs(t, err, diagnostics.ErrSentinel, "the refusal is a usage error, not a minted diagnostic")
			assert.False(t, errors.As(err, &de), "refusing must not produce the very diagnostic it rejected")
		})
	}

	t.Run("requires a code and a message", func(t *testing.T) {
		assert.ErrorContains(t, MagusRaise(context.Background(), "", "msg", raiseOpts(nil, "")), "needs a code")
		assert.ErrorContains(t, MagusRaise(context.Background(), "ACME1001", "", raiseOpts(nil, "")), "needs a message")
	})
}

// TestMagusRaiseCause covers wrapping: the cause has to stay reachable for errors.Is and
// stay VISIBLE in the rendered message, which Wrapf alone does not do.
func TestMagusRaiseCause(t *testing.T) {
	t.Run("splices a string cause and unwraps to it", func(t *testing.T) {
		err := MagusRaise(context.Background(), "ACME1001", "push failed", raiseOpts("exec docker: exit 1", ""))
		assert.EqualError(t, err, "[ACME1001] push failed: exec docker: exit 1")
		assert.ErrorContains(t, errors.Unwrap(err), "exec docker: exit 1")
	})

	// The realistic shape: an inner catch hands back the map BuzzError built, and the
	// inner code must keep matching once the outer one wraps it.
	t.Run("keeps a coded cause matchable underneath the new code", func(t *testing.T) {
		inner := map[string]any{"code": "MGS2001", "message": "spell not found", "url": "https://x/MGS2001"}
		err := MagusRaise(context.Background(), "ACME1001", "build failed", raiseOpts(inner, ""))
		assert.EqualError(t, err, "[ACME1001] build failed: [MGS2001] spell not found")
		assert.ErrorIs(t, err, diagnostics.Code("MGS2001"),
			"the inner code must still match after wrapping, which is the point of a cause")
	})

	t.Run("an absent cause changes nothing", func(t *testing.T) {
		for _, empty := range []any{nil, "", map[string]any{}} {
			err := MagusRaise(context.Background(), "ACME1001", "plain", raiseOpts(empty, ""))
			assert.EqualError(t, err, "[ACME1001] plain")
		}
	})
}

// raiseOpts builds the opts map the way a magusfile would, keeping the tests readable
// now that cause and url are keys rather than positions.
func raiseOpts(cause any, url string) map[string]any {
	o := map[string]any{}
	if cause != nil {
		o["cause"] = cause
	}
	if url != "" {
		o["url"] = url
	}
	if len(o) == 0 {
		return nil
	}
	return o
}

// fakeAnalyzer is a workspace that can answer the insight lenses, recording the
// options it was handed so a test can prove they arrived.
type fakeAnalyzer struct {
	types.WorkspaceRepository
	got       types.InsightOptions
	failTrend bool
}

func (f *fakeAnalyzer) Hotspots(_ context.Context, o types.InsightOptions) (types.HotspotOutput, error) {
	f.got = o
	return types.HotspotOutput{Definition: "hot"}, nil
}
func (f *fakeAnalyzer) Affinity(context.Context, types.InsightOptions) (types.AffinityOutput, error) {
	return types.AffinityOutput{}, nil
}
func (f *fakeAnalyzer) Ownership(context.Context, types.InsightOptions) (types.OwnershipOutput, error) {
	return types.OwnershipOutput{}, nil
}
func (f *fakeAnalyzer) Trend(context.Context, types.InsightOptions) (types.TrendOutput, error) {
	if f.failTrend {
		return types.TrendOutput{}, errors.New("no history")
	}
	return types.TrendOutput{}, nil
}
func (f *fakeAnalyzer) Volatility(context.Context) (types.VolatilityReport, error) {
	return types.VolatilityReport{}, errors.New("no run history")
}
func (f *fakeAnalyzer) Unreferenced(context.Context) (types.UnreferencedOutput, error) {
	return types.UnreferencedOutput{}, errors.New("no symbol index")
}

func TestInsightIsServedInProcess(t *testing.T) {
	t.Parallel()

	a := &fakeAnalyzer{}
	ctx := types.WithWorkspace(t.Context(), a)

	report, err := MagusInsightReport(ctx, nil)
	require.NoError(t, err, "a workspace that analyses answers here, with no subprocess")
	assert.Equal(t, "hot", report.Hotspots.Definition)
	assert.Equal(t, 500, a.got.Commits, "the window the removed subcommand defaulted to")
	assert.True(t, a.got.Files, "the report always carries the per-file ranking")
}

// TestInsightNeedsAWorkspace pins the cost of removing the subcommand: there is no
// longer a nested magus to fall back to, so a caller with no workspace on the
// context is told so rather than silently getting a different answer.
func TestInsightNeedsAWorkspace(t *testing.T) {
	t.Parallel()

	_, err := MagusInsightReport(t.Context(), nil)
	require.Error(t, err, "no workspace on the context is an error, not a fork")
}

// TestInsightRejectsAnUnknownOption keeps a mistyped key from being ignored:
// silently dropping `comits` would quietly answer the 500-commit question instead.
// Asserted through the exported entry point, which is the only way a caller reaches
// the decoder - an earlier version tested a flag parser production could not reach.
func TestInsightRejectsAnUnknownOption(t *testing.T) {
	t.Parallel()

	ctx := types.WithWorkspace(t.Context(), &fakeAnalyzer{})
	for _, opts := range []map[string]any{
		{"comits": 50.0},
		{"mermaidStyle": "safe"},
		{"workspace": true},
		{"commits": "not a number"},
		{"since": 90.0},
	} {
		_, err := MagusInsightReport(ctx, opts)
		assert.Error(t, err, "opts %v must be reported, not ignored", opts)
	}
}

// TestInsightMapsTheOptionsItTakes proves the window reaches the analyzer, rather
// than only checking the struct the decoder returned.
func TestInsightMapsTheOptionsItTakes(t *testing.T) {
	t.Parallel()

	a := &fakeAnalyzer{}
	ctx := types.WithWorkspace(t.Context(), a)
	// A Buzz number arrives as float64; an int is what a Go caller would pass.
	_, err := MagusInsightReport(ctx, map[string]any{"commits": 42.0, "since": "90d"})
	require.NoError(t, err)
	assert.Equal(t, 42, a.got.Commits)
	assert.Equal(t, "90d", a.got.Since)

	// int64 is what a Buzz integer literal actually arrives as; float64 above covers
	// a Buzz float. A decoder handling only float64 rejects `{commits = 50}` outright.
	_, err = MagusInsightReport(ctx, map[string]any{"commits": int64(50)})
	require.NoError(t, err, "a Buzz integer arrives as int64")
	assert.Equal(t, 50, a.got.Commits)
}

// TestInsightMarkdownRendersTheDocument covers the surface that replaced the
// subcommand's rendering: the same report, handed over as the page.
func TestInsightMarkdownRendersTheDocument(t *testing.T) {
	t.Parallel()

	ctx := types.WithWorkspace(t.Context(), &fakeAnalyzer{})
	doc, err := MagusInsightMarkdown(ctx, nil)
	require.NoError(t, err)
	assert.Contains(t, doc, "# Insight", "the document is the whole point of this method")
	// click/subgraph are what the portable subset drops; quadrantChart never reached
	// the combined report, so asserting its absence proved nothing.
	assert.NotContains(t, doc, "click ", "the portable Mermaid subset is the default now")
	assert.NotContains(t, doc, "subgraph ", "the portable Mermaid subset is the default now")
}

// TestInsightReportKeepsTheBestEffortSections pins the rule the CLI kept: a lens
// whose source is missing omits its section rather than failing the whole report.
func TestInsightReportKeepsTheBestEffortSections(t *testing.T) {
	t.Parallel()

	a := &fakeAnalyzer{}
	report, err := buildInsightReport(t.Context(), a, types.InsightOptions{Commits: 500})
	require.NoError(t, err, "volatility and unreferenced failing must not fail the report")
	assert.Equal(t, "hot", report.Hotspots.Definition)

	// A required lens failing IS an error: the four VCS lenses are the report.
	a.failTrend = true
	_, err = buildInsightReport(t.Context(), a, types.InsightOptions{})
	require.Error(t, err)
}
