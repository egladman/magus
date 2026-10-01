package std

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
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
		args     []string
		wantWarn bool
	}{
		{"describe of a typed noun warns", "describe", []string{"spell", "go"}, true},
		{"describe of a plural spelling warns", "describe", []string{"mcp-tools"}, true},
		{"describe of an untyped noun does not warn", "describe", []string{"job", "x"}, false},
		{"describe asked for its bytes does not warn", "describe", []string{"graph", "-o", "json"}, false},
		{"bare describe does not warn", "describe", nil, false},
		{"run warns", "run", nil, true},
		{"insight does not warn", "insight", nil, false},
		{"doctor warns", "doctor", nil, true},
		{"status does not warn", "status", nil, false},
		{"affected does not warn", "affected", nil, false},
		{"no subcommand does not warn", "", nil, false},
		// The subcommand is its own argument now, so a value that merely CONTAINS a
		// typed name is not one: only an exact match is the escape hatch being misused.
		{"a longer name that starts with one does not warn", "runner", nil, false},
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
			warnIfTypedSubcommand(context.Background(), tc.sub, tc.args)

			got := strings.Contains(buf.String(), "subcommand with a dedicated method")
			assert.Equal(t, tc.wantWarn, got, "warn mismatch (log=%q)", buf.String())
		})
	}
}

func TestRecursiveMagusEnvPreservesTheCapturedLease(t *testing.T) {
	t.Setenv("BAGGAGE", "")
	ctx := proc.WithLease(t.Context(), "fleet/captured")
	assert.Contains(t, recursiveMagusEnv(ctx), "BAGGAGE=magus.lease=fleet/captured")
}

// TestResolveRunDir covers where a nested magus runs. opts.dir is resolved relative to
// the contextual project dir, matching proc.exec's dir, so a magusfile can send a nested
// invocation to a sibling directory without reaching for proc.exec on the magus binary,
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

// TestNestedExecOptionsCarriesStdin pins the option that was silently dropped: a magusfile
// piping a registry token to `graph push` had it discarded, so every cd publish failed with
// "no token on stdin" against a magusfile that passed one.
func TestNestedExecOptionsCarriesStdin(t *testing.T) {
	ctx := WithCwd(context.Background(), "/ws/api")

	fed := nestedExecOptions(ctx, map[string]any{"stdin": "ghp_token", "quiet": true}, nil)
	assert.Equal(t, "ghp_token", fed.Stdin)
	assert.True(t, fed.Quiet)
	assert.Equal(t, "/ws/api", fed.Dir)
	assert.True(t, fed.Capture, "a nested magus is always captured; the caller reads its output")

	bare := nestedExecOptions(ctx, nil, nil)
	assert.Empty(t, bare.Stdin, "no opt means the child inherits nothing on stdin")

	wrong := nestedExecOptions(ctx, map[string]any{"stdin": 7}, nil)
	assert.Empty(t, wrong.Stdin, "a non-string stdin is ignored rather than rendered")
}

// TestNestedResultHonorsAllowFailure pins magus.cmd/run/describe to proc.exec's
// contract: a non-zero exit raises, unless opts.allow_failure returns the result.
func TestNestedResultHonorsAllowFailure(t *testing.T) {
	full := []string{"status"}
	failed := run.ExecResult{Stdout: "partial\n", Stderr: "boom\n", Code: 3, Started: true}

	_, err := nestedResult("cmd", full, failed, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "magus.cmd: status exited with code 3")

	got, err := nestedResult("cmd", full, failed, nil, map[string]any{"allow_failure": true})
	require.NoError(t, err)
	assert.Equal(t, types.ExecResult{Stdout: "partial", Stderr: "boom", Code: 3, OK: false}, got)

	notStarted := run.ExecResult{Code: -1}
	_, err = nestedResult("cmd", full, notStarted, errors.New("no such file"), nil)
	require.ErrorContains(t, err, "no such file")
	got, err = nestedResult("cmd", full, notStarted, errors.New("no such file"), map[string]any{"allow_failure": true})
	require.NoError(t, err)
	assert.Equal(t, -1, got.Code)
	assert.False(t, got.OK)

	_, err = nestedResult("cmd", full, run.ExecResult{Code: -1}, types.ExecDenied, map[string]any{"allow_failure": true})
	assert.ErrorIs(t, err, types.ExecDenied, "a sandbox denial is never swallowed")

	ok, err := nestedResult("cmd", full, run.ExecResult{Stdout: "fine", Started: true}, nil, nil)
	require.NoError(t, err)
	assert.True(t, ok.OK)
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

func (f *fakeAnalyzer) Duplication(context.Context) (types.DuplicationOutput, error) {
	return types.DuplicationOutput{}, errors.New("no symbol index")
}

func TestInsightIsServedInProcess(t *testing.T) {
	t.Parallel()

	a := &fakeAnalyzer{}
	ctx := types.WithWorkspace(t.Context(), a)

	report, err := MagusInsight(ctx, nil)
	require.NoError(t, err, "a workspace that analyses answers here, with no subprocess")
	assert.Equal(t, "hot", report.Hotspots.Definition)
	assert.Equal(t, 500, a.got.Commits, "the window the removed subcommand defaulted to")
	assert.True(t, a.got.Files, "the report always carries the per-file ranking")
}

// fakeToolReporter is a workspace that reports its tools, so magus\describe.tool() can be shown
// to answer in-process rather than forking a nested magus.
type fakeToolReporter struct {
	types.WorkspaceRepository
	report types.ToolReport
}

func (f *fakeToolReporter) Tools(context.Context, ...string) (types.ToolReport, error) {
	return f.report, nil
}

func TestMagusToolsIsServedInProcess(t *testing.T) {
	t.Parallel()

	want := types.ToolReport{
		Workspace: "/ws",
		Count:     1,
		Lifecycle: types.LifecycleStatus{Provider: "endoflife-date", State: types.LifecycleLive},
		Tools:     []types.ToolRow{{Project: ".", Bin: "go", Lifecycle: "go", Cycle: "1.26", Support: "supported"}},
	}
	got, err := MagusDescribeTool(types.WithWorkspace(t.Context(), &fakeToolReporter{report: want}))
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestInsightNeedsAWorkspace pins the cost of removing the subcommand: there is no
// longer a nested magus to fall back to, so a caller with no workspace on the
// context is told so rather than silently getting a different answer.
func TestInsightNeedsAWorkspace(t *testing.T) {
	t.Parallel()

	_, err := MagusInsight(t.Context(), nil)
	require.Error(t, err, "no workspace on the context is an error, not a fork")
}

// TestInsightRejectsAnUnknownOption keeps a mistyped key from being ignored:
// silently dropping `comits` would quietly answer the 500-commit question instead.
// Asserted through the exported entry point, which is the only way a caller reaches
// the decoder: an earlier version tested a flag parser production could not reach.
func TestInsightRejectsAnUnknownOption(t *testing.T) {
	t.Parallel()

	ctx := types.WithWorkspace(t.Context(), &fakeAnalyzer{})
	for _, opts := range []map[string]any{
		{"comits": 50.0},
		{"layoutStyle": "safe"},
		{"workspace": true},
		{"commits": "not a number"},
		{"since": 90.0},
	} {
		_, err := MagusInsight(ctx, opts)
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
	_, err := MagusInsight(ctx, map[string]any{"commits": 42.0, "since": "90d"})
	require.NoError(t, err)
	assert.Equal(t, 42, a.got.Commits)
	assert.Equal(t, "90d", a.got.Since)

	// int64 is what a Buzz integer literal actually arrives as; float64 above covers
	// a Buzz float. A decoder handling only float64 rejects `{commits = 50}` outright.
	_, err = MagusInsight(ctx, map[string]any{"commits": int64(50)})
	require.NoError(t, err, "a Buzz integer arrives as int64")
	assert.Equal(t, 50, a.got.Commits)
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

// fakeLedgerWorkspace is a workspace that can back a job Store: it
// carries the cache directory jobStoreFromContext needs beyond
// types.WorkspaceRepository's own Root.
type fakeLedgerWorkspace struct {
	types.WorkspaceRepository
	cacheDir string
	root     string
}

func (f *fakeLedgerWorkspace) CacheDir() string { return f.cacheDir }
func (f *fakeLedgerWorkspace) Root() string     { return f.root }

// Not parallel: the job store resolves the per-repository state directory, and
// jobStoreFromContext offers no seam to redirect it, so the environment is the only
// thing keeping these rows out of the developer's own store.
func TestLedgerIsServedInProcess(t *testing.T) {
	testkit.Isolate(t)

	ctx := types.WithWorkspace(t.Context(), &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})

	_, err := MagusPutJob(ctx, "u1", map[string]any{"criteria": "ship it", "state": "running"})
	require.NoError(t, err)

	report, err := MagusListJob(ctx)
	require.NoError(t, err, "a workspace with a cache directory answers here, with no subprocess")
	require.Len(t, report.Jobs, 1)
	assert.Equal(t, "u1", report.Jobs[0].ID)
	assert.Equal(t, types.StateRunning, report.Jobs[0].State)
}

// A guard rule runs under the rows the guard pinned: list answers from them even with a
// workspace on the context whose store holds something else, and every write refuses.
func TestJobListAnswersFromAPinnedSnapshot(t *testing.T) {
	testkit.Isolate(t)
	ws := types.WithWorkspace(t.Context(), &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})
	_, err := MagusPutJob(ws, "on-disk", map[string]any{"criteria": "not what the guard read"})
	require.NoError(t, err)

	pinned := types.Job{ID: "orchestrator/guard-facts", Criteria: "the seam", State: types.StateRunning, WritePaths: []string{"internal/guard/**"}}
	cases := []struct {
		name    string
		snap    job.Snapshot
		want    types.JobList
		wantErr string
	}{
		{
			name: "the guard's rows",
			snap: job.Snapshot{Rows: []types.Job{pinned}},
			want: types.NewJobList([]types.Job{pinned}),
		},
		{name: "an empty store", snap: job.Snapshot{}, want: types.NewJobList(nil)},
		{
			name:    "a store the guard could not read",
			snap:    job.Snapshot{Err: errors.New("jobs.json: unexpected end of JSON input")},
			wantErr: "unexpected end of JSON input",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := job.WithSnapshot(ws, tc.snap)
			got, err := MagusListJob(ctx)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	ctx := job.WithSnapshot(ws, job.Snapshot{Rows: []types.Job{pinned}})
	_, err = MagusPutJob(ctx, pinned.ID, map[string]any{"state": "pass"})
	assert.ErrorContains(t, err, "read-only")
	_, _, err = MagusRegisterJob(ctx, pinned.ID, "abc123")
	assert.ErrorContains(t, err, "read-only")
	_, err = MagusClearJob(ctx)
	assert.ErrorContains(t, err, "read-only")
	report, err := MagusListJob(ws)
	require.NoError(t, err)
	require.Len(t, report.Jobs, 1)
	assert.Equal(t, "on-disk", report.Jobs[0].ID, "nothing was written through the snapshot")
}

// magus\job.list measures overlaps through the function `magus ls jobs` calls, so the
// typed call's footprints are exactly job.MeasureOverlaps over the same rows.
func TestJobListMeasuresOverlapFootprints(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	testkit.Isolate(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	root := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.go diff=golang\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "api", "x.go"), []byte("package api\n\nfunc X() {\n\treturn\n}\n"), 0o644))
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("commit", "-q", "-m", "seed")
	rev := git("rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(root, "api", "x.go"), []byte("package api\n\nfunc X() {\n\tpanic(1)\n}\n"), 0o644))

	ctx := types.WithWorkspace(t.Context(), &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: root})
	for id, paths := range map[string][]any{"a": {"api/*.go"}, "b": {"api/x.go"}} {
		_, err := MagusPutJob(ctx, id, map[string]any{"checkpoint": rev, "write_paths": paths, "check": "test .", "state": "running"})
		require.NoError(t, err)
		_, _, err = MagusRegisterJob(ctx, id, rev)
		require.NoError(t, err)
	}

	report, err := MagusListJob(ctx)
	require.NoError(t, err)
	require.Len(t, report.Overlaps, 1)
	require.NotNil(t, report.Overlaps[0].Footprint, "the typed call measures footprints like `magus ls jobs`")
	assert.Equal(t, types.FootprintShared, report.Overlaps[0].Footprint.Verdict, report.Overlaps[0].Footprint.Reason)
	assert.Equal(t, job.MeasureOverlaps(ctx, root, report.Jobs, types.NewJobList(report.Jobs).Overlaps), report.Overlaps)
}

// TestLedgerNeedsAWorkspace mirrors TestInsightNeedsAWorkspace: there is no `magus
// job` CLI subcommand to fall back to, so a caller with no workspace on the context
// is told so.
func TestLedgerNeedsAWorkspace(t *testing.T) {
	t.Parallel()

	_, err := MagusListJob(t.Context())
	require.Error(t, err)
	_, err = MagusPutJob(t.Context(), "u1", nil)
	require.Error(t, err)
	_, err = MagusClearJob(t.Context())
	require.Error(t, err)
}

// TestLedgerNeedsACacheDir covers the workspace that answers the reading verbs
// (ls, affected, ...) but was never given a cache directory: a test double, not the
// real *magus.Magus.
func TestLedgerNeedsACacheDir(t *testing.T) {
	t.Parallel()

	ctx := types.WithWorkspace(t.Context(), &fakeAnalyzer{})
	_, err := MagusListJob(ctx)
	assert.ErrorContains(t, err, "cache directory")
}

// TestPutLedgerMergesRatherThanReplaces proves the Buzz binding shares
// internal/job.ParseMerge with magus\job.put: a later put naming only
// `state` must not erase the goal an earlier put declared.
// Not parallel, and the env is why: see TestLedgerIsServedInProcess above. A root of
// "" hashes to the same state directory in every checkout, so these rows would land in
// the developer's own store and the clear below would drop what it found there.
func TestPutLedgerMergesRatherThanReplaces(t *testing.T) {
	testkit.Isolate(t)

	ctx := types.WithWorkspace(t.Context(), &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})

	_, err := MagusPutJob(ctx, "u1", map[string]any{"criteria": "the declared goal", "write_paths": "internal/job", "check": "test ."})
	require.NoError(t, err)

	got, err := MagusPutJob(ctx, "u1", map[string]any{"state": "pass"})
	require.NoError(t, err)
	assert.Equal(t, types.StatePass, got.State)
	assert.Equal(t, "the declared goal", got.Criteria, "the state advance must not erase the row")
	assert.Equal(t, []string{"internal/job"}, got.WritePaths)
	require.NotNil(t, got.Check, "the state advance must not erase the check")
}

// TestPutLedgerRejectsAnUnknownState proves a mistyped state is reported, not
// silently ignored; internal/job.ParseMerge is what enforces this, and this pins that
// the Buzz binding does not swallow its error.
func TestPutLedgerRejectsAnUnknownState(t *testing.T) {
	testkit.Isolate(t)

	ctx := types.WithWorkspace(t.Context(), &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})
	_, err := MagusPutJob(ctx, "u1", map[string]any{"state": "passed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_return")
}

func TestClearLedgerReportsHowManyRowsItDropped(t *testing.T) {
	testkit.Isolate(t)

	ctx := types.WithWorkspace(t.Context(), &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})
	_, err := MagusPutJob(ctx, "u1", nil)
	require.NoError(t, err)
	_, err = MagusPutJob(ctx, "u2", nil)
	require.NoError(t, err)

	cleared, err := MagusClearJob(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, cleared)

	report, err := MagusListJob(ctx)
	require.NoError(t, err)
	assert.Empty(t, report.Jobs)
}

func TestJobStoreRetainsTheLeaseCapturedOnTheBuzzContext(t *testing.T) {
	testkit.Isolate(t)
	workspace := &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()}
	ctx := proc.WithLease(types.WithWorkspace(t.Context(), workspace), "fleet/captured")

	store, err := jobStoreFromContext(ctx, "job.put")
	require.NoError(t, err)
	assert.Equal(t, "fleet/captured", store.Actor().Lease)
}

// The captured lease is the process's claim, so a checkout's binding outranks it here
// exactly as it does for `magus job` and the guard.
func TestJobStorePrefersTheCheckoutsBindingOverTheCapturedClaim(t *testing.T) {
	testkit.Isolate(t)
	workspace := &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()}
	require.NoError(t, job.NewStore(job.Location{CacheDir: workspace.cacheDir}).Bind(job.Caller{}, "fleet/bound"))
	ctx := proc.WithLease(types.WithWorkspace(t.Context(), workspace), "fleet/captured")

	store, err := jobStoreFromContext(ctx, "job.put")
	require.NoError(t, err)
	assert.Equal(t, "fleet/bound", store.Actor().Lease)
}

// TestLedgerBindingAndStoreAgree pins that magus\job.put and the store are two doors
// onto the same file: a row put through the binding is visible through the store,
// and internal/job.Store's own path derivation (CacheDir/ledger/leases.json)
// is what makes that true without either side naming the other.
func TestLedgerBindingAndStoreAgree(t *testing.T) {
	stateBase := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateBase)

	cacheDir, root := t.TempDir(), t.TempDir()
	ctx := types.WithWorkspace(t.Context(), &fakeLedgerWorkspace{cacheDir: cacheDir, root: root})

	_, err := MagusPutJob(ctx, "u1", map[string]any{"criteria": "shared row"})
	require.NoError(t, err)

	store := job.NewStore(job.Location{StateBase: stateBase, CacheDir: cacheDir, Root: root})
	leases, err := store.List()
	require.NoError(t, err)
	require.Len(t, leases, 1)
	assert.Equal(t, "shared row", leases[0].Criteria)
}

func TestJobResultFromMapUsesTheVersionedStrictDecoder(t *testing.T) {
	t.Parallel()

	result, err := jobResultFromMap(map[string]any{
		"schema_version": types.JobResultSchemaVersion,
		"changed_paths":  []any{"internal/job/lifecycle.go"},
		"validation": map[string]any{
			"command":    "magus run test .",
			"output_ref": "out123",
		},
		"unresolved_risks": []any{},
	})
	require.NoError(t, err)
	assert.Equal(t, types.JobResultSchemaVersion, result.Version)
	assert.Equal(t, "out123", result.Validation.OutputRef)

	_, err = jobResultFromMap(map[string]any{
		"schema_version":   types.JobResultSchemaVersion,
		"changed_paths":    []any{},
		"validation":       map[string]any{},
		"unresolved_risks": []any{},
		"claimed_pass":     true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claimed_pass", "a Buzz map cannot smuggle a field the verifier ignores")
}

// fakeGraphWorkspace is a workspace holding a hand-built knowledge graph and an output
// store, which is all the graph members and magus\output read off it.
type fakeGraphWorkspace struct {
	types.WorkspaceRepository
	g        *knowledge.Graph
	cacheDir string
	projects []*types.Project
}

func (f *fakeGraphWorkspace) All() []*types.Project { return f.projects }

func (f *fakeGraphWorkspace) KnowledgeGraph(context.Context, bool) (*knowledge.Graph, error) {
	return f.g, nil
}
func (f *fakeGraphWorkspace) KnowledgeGraphWithSymbols(context.Context) (*knowledge.Graph, error) {
	return f.g, nil
}
func (f *fakeGraphWorkspace) KnowledgeGraphWithSymbolsForRef(context.Context, string) (*knowledge.Graph, error) {
	return f.g, nil
}
func (f *fakeGraphWorkspace) SymbolGaps(context.Context) ([]types.KnowledgeSymbolGap, bool) {
	return nil, true
}
func (f *fakeGraphWorkspace) CacheDir() string { return f.cacheDir }

const graphSymbol = "symbol:example.com/x Foo#"

func graphContext(t *testing.T) context.Context {
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "project:pkg/a", Kind: types.KindProject, Label: "pkg/a"})
	g.AddNode(types.KnowledgeNode{ID: "project:pkg/b", Kind: types.KindProject, Label: "pkg/b"})
	for _, name := range []string{"build", "test"} {
		id := "target:pkg/a:" + name
		g.AddNode(types.KnowledgeNode{ID: id, Kind: types.KindTarget, Label: name})
		g.AddEdge(types.KnowledgeEdge{Source: "project:pkg/a", Target: id, Relation: types.RelationContains, Confidence: types.ConfidenceExtracted, Score: 1})
	}
	g.AddNode(types.KnowledgeNode{ID: graphSymbol, Kind: types.KindSymbol, Label: "Foo"})
	for _, f := range []string{"a", "b", "c"} {
		g.AddEdge(types.KnowledgeEdge{
			Source: "file:pkg/" + f + ".go", Target: graphSymbol,
			Relation: types.RelationReferences, Confidence: types.ConfidenceExtracted, Score: 1,
			Provenance: "scip count=1 lines=3",
		})
	}
	return types.WithWorkspace(t.Context(), &fakeGraphWorkspace{g: g, cacheDir: t.TempDir()})
}

func TestGraphMembersAreServedInProcess(t *testing.T) {
	t.Parallel()
	ctx := graphContext(t)

	q, err := MagusQuery(ctx, "kind=target", map[string]any{"limit": int64(1)})
	require.NoError(t, err)
	assert.Equal(t, 2, q.MatchCount, "the total survives the window")
	assert.Len(t, q.Matches, 1)
	assert.Equal(t, types.VerdictFound, q.Answer.Verdict)

	x, err := MagusExplain(ctx, "target:pkg/a:build")
	require.NoError(t, err)
	assert.Equal(t, "target:pkg/a:build", x.Node.ID)

	p, err := MagusPath(ctx, "target:pkg/a:build", "target:pkg/a:test", nil)
	require.NoError(t, err)
	assert.True(t, p.Found)

	unlinked, err := MagusPath(ctx, "project:pkg/a", "project:pkg/b", nil)
	require.NoError(t, err, "a resolved pair with no connection is an answer")
	assert.False(t, unlinked.Found)

	s, err := MagusStats(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, 5, s.NodeCount)
}

func TestGraphMembersRaiseOnWhatDoesNotResolve(t *testing.T) {
	t.Parallel()
	ctx := graphContext(t)

	_, err := MagusExplain(ctx, "target:pkg/z:nope")
	require.Error(t, err)
	_, err = MagusPath(ctx, "target:pkg/a:build", "target:pkg/z:nope", nil)
	require.Error(t, err)
	_, err = MagusQuery(ctx, " ", nil)
	require.Error(t, err)
}

func TestRefsWindowsSitesAndKeepsTheTotals(t *testing.T) {
	t.Parallel()
	ctx := graphContext(t)

	r, err := MagusRefs(ctx, graphSymbol, map[string]any{"offset": int64(1), "limit": int64(1)})
	require.NoError(t, err)
	assert.Equal(t, 3, r.FileCount)
	assert.Equal(t, []types.KnowledgeRefSite{{File: "pkg/b.go", Count: 1, Lines: []int{3}}}, r.Refs)

	absent, err := MagusRefs(ctx, "symbol:example.com/x Missing#", nil)
	require.NoError(t, err, "a symbol nothing defines is an answer, not a raise")
	assert.Equal(t, types.VerdictAbsent, absent.Answer.Verdict)
}

func TestImportGraphHostCallReportsWhetherAnIndexWasRead(t *testing.T) {
	t.Parallel()

	noPackages, err := MagusImportGraph(graphContext(t))
	require.NoError(t, err)
	assert.Equal(t, types.ImportGraph{Indexed: true, Packages: map[string][]string{}, Languages: map[string]string{}}, noPackages,
		"graphContext holds a symbol but no dir imports another")

	g := knowledge.NewGraph()
	ctx := types.WithWorkspace(t.Context(), &fakeGraphWorkspace{g: g, cacheDir: t.TempDir()})
	empty, err := MagusImportGraph(ctx)
	require.NoError(t, err)
	assert.Equal(t, types.ImportGraph{Indexed: false, Packages: map[string][]string{}, Languages: map[string]string{}}, empty)

	g.AddNode(types.KnowledgeNode{ID: "symbol:x", Kind: types.KindSymbol, Label: "x"})
	for _, dir := range []string{"pkg/a", "pkg/b"} {
		g.AddNode(types.KnowledgeNode{ID: "dir:" + dir, Kind: types.KindDir, Label: dir, Source: dir, Attrs: map[string]string{types.AttrLanguage: "go"}})
	}
	g.AddEdge(types.KnowledgeEdge{Source: "dir:pkg/a", Target: "dir:pkg/b", Relation: types.RelationImports, Confidence: types.ConfidenceExtracted, Score: 1})
	got, err := MagusImportGraph(ctx)
	require.NoError(t, err)
	assert.Equal(t, types.ImportGraph{
		Indexed:   true,
		Packages:  map[string][]string{"pkg/a": {"pkg/b"}},
		Languages: map[string]string{"pkg/a": "go", "pkg/b": "go"},
	}, got)
}

// A mistyped option would otherwise answer a different question than the one asked.
func TestGraphOptionsAreStrict(t *testing.T) {
	t.Parallel()
	ctx := graphContext(t)

	_, err := MagusQuery(ctx, "kind=target", map[string]any{"limt": int64(5)})
	require.ErrorContains(t, err, `unknown option "limt"`)
	_, err = MagusRefs(ctx, graphSymbol, map[string]any{"offset": int64(-1)})
	require.ErrorContains(t, err, "must not be negative")
	_, err = MagusQuery(ctx, "kind=target", map[string]any{"budget": "10"})
	require.ErrorContains(t, err, "must be a number")
}

func TestWorkspaceMembersNeedAWorkspace(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	calls := map[string]func() error{
		"query":          func() error { _, err := MagusQuery(ctx, "x", nil); return err },
		"explain":        func() error { _, err := MagusExplain(ctx, "x"); return err },
		"path":           func() error { _, err := MagusPath(ctx, "x", "y", nil); return err },
		"refs":           func() error { _, err := MagusRefs(ctx, "x", nil); return err },
		"stats":          func() error { _, err := MagusStats(ctx, ""); return err },
		"importGraph":    func() error { _, err := MagusImportGraph(ctx); return err },
		"dir":            func() error { _, err := MagusDir(ctx, "x"); return err },
		"dirs":           func() error { _, err := MagusDirs(ctx, "**", nil); return err },
		"layer":          func() error { _, err := MagusLayer(ctx, "x"); return err },
		"neighborhood":   func() error { _, err := MagusNeighborhood(ctx, "x", nil); return err },
		"output":         func() error { _, err := MagusOutput(ctx, "out1a2b3c"); return err },
		"vcs.checkpoint": func() error { _, err := MagusVCSCheckpoint(ctx); return err },
	}
	for member, call := range calls {
		assert.ErrorIsf(t, call(), types.MagusfileOnlyMember, "magus\\%s", member)
	}
}

func TestOutputReadsTheCheckoutsStore(t *testing.T) {
	t.Parallel()
	ctx := graphContext(t)
	cacheDir := types.WorkspaceFromContext(ctx).(workspaceCacheDir).CacheDir()

	desc, err := cache.NewOutputStore(cacheDir).Persist(ctx, strings.Repeat("ab", 32),
		[]byte("ok\n"), cache.OutputDescriptor{Project: "pkg/a", Target: "build", DurationMs: 7})
	require.NoError(t, err)

	got, err := MagusOutput(ctx, desc.Ref)
	require.NoError(t, err)
	assert.Equal(t, types.OutputRecord{
		Ref: desc.Ref, Project: "pkg/a", Target: "build",
		Failed: false, DurationMs: 7, Output: "ok\n",
	}, got)

	_, err = MagusOutput(ctx, "build")
	require.ErrorContains(t, err, "not an output ref")
	_, err = MagusOutput(ctx, "out0000000000")
	require.ErrorContains(t, err, "no stored output")
}

// fakeCheckoutWorkspace is a workspace rooted at this package's own checkout.
type fakeCheckoutWorkspace struct {
	types.WorkspaceRepository
}

func (fakeCheckoutWorkspace) Root() string                 { return "." }
func (fakeCheckoutWorkspace) VCSOptions() types.VCSOptions { return types.VCSOptions{} }

// The member and `magus vcs checkpoint` resolve the same tree the same way, so the two
// cannot describe it differently.
func TestVCSCheckpointAgreesWithTheCLI(t *testing.T) {
	t.Parallel()
	ctx := types.WithWorkspace(t.Context(), fakeCheckoutWorkspace{})

	got, gotErr := MagusVCSCheckpoint(ctx)

	res, err := vcs.Resolve(ctx, ".", "", types.VCSOptions{})
	require.NoError(t, err)
	want, wantErr := vcs.Checkpoint(ctx, ".", res, false)
	assert.Equal(t, want, got)
	assert.Equal(t, wantErr, gotErr)
}

// TestTypedMagusSubcommandsNameRealMembers pins the hint magus.cmd prints: every
// subcommand it steers away from must have the typed member it steers toward.
func TestTypedMagusSubcommandsNameRealMembers(t *testing.T) {
	members := map[string]bool{}
	for _, m := range Magus.Methods {
		members[m.Name] = true
	}
	for sub := range typedMagusSubcommands {
		assert.True(t, members[sub], "magus.cmd(%q) points at magus.%s, which does not exist", sub, sub)
	}

	describe := map[string]bool{}
	for _, ns := range Magus.Namespaces {
		if ns.Name != "describe" {
			continue
		}
		for _, m := range ns.Methods {
			describe[CamelCase(m.Name)] = true
		}
	}
	for noun, method := range typedDescribeNouns {
		assert.True(t, describe[method], "magus.cmd(\"describe\", [%q]) points at magus\\describe.%s, which does not exist", noun, method)
	}
}
