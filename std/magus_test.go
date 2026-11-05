package std

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// stdSuite is the package's one testify suite: add tests here as methods.
type stdSuite struct {
	suite.Suite
}

func TestStdSuite(t *testing.T) {
	suite.Run(t, new(stdSuite))
}

// SetupSuite registers the MGS catalog as magus's startup does, so a raise can tell a
// built-in code from the workspace's.
func (s *stdSuite) SetupSuite() {
	s.Require().NoError(diagnostics.RegisterCatalog(types.DiagnosticOwnerMagus, types.AllDiagnosticCodes()))
}

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

			// Test the pure decision half directly — calling MagusCommand would exec the
			// test binary (a fork-bomb risk), and the warning is what we care about.
			warnIfTypedSubcommand(context.Background(), tc.sub, tc.args)

			got := strings.Contains(buf.String(), "subcommand with a dedicated method")
			assert.Equal(t, tc.wantWarn, got, "warn mismatch (log=%q)", buf.String())
		})
	}
}

func TestRecursiveMagusEnvPreservesTheCapturedLease(t *testing.T) {
	ctx := types.WithJobLease(t.Context(), "fleet/captured")
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
		opts types.MagusCommandOptions
		want string
	}{
		{"no opts keeps the contextual dir", "/ws/api", types.MagusCommandOptions{}, "/ws/api"},
		{"empty dir keeps the contextual dir", "/ws/api", types.MagusCommandOptions{Dir: ""}, "/ws/api"},
		{"relative dir joins onto it", "/ws/api", types.MagusCommandOptions{Dir: "scripts"}, "/ws/api/scripts"},
		{"parent-relative dir joins too", "/ws/api", types.MagusCommandOptions{Dir: "../web"}, "/ws/web"},
		{"absolute dir wins outright", "/ws/api", types.MagusCommandOptions{Dir: "/elsewhere"}, "/elsewhere"},
		{"no contextual dir uses opts.dir as given", "", types.MagusCommandOptions{Dir: "scripts"}, "scripts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.cwd != "" {
				ctx = WithCurrentWorkingDirectory(ctx, tc.cwd)
			}
			assert.Equal(t, tc.want, resolveRunDir(ctx, tc.opts))
		})
	}
}

// TestNestedExecOptionsCarriesStdin pins the option that was silently dropped: a magusfile
// piping a registry token to `graph push` had it discarded, so every cd publish failed with
// "no token on stdin" against a magusfile that passed one.
func TestNestedExecOptionsCarriesStdin(t *testing.T) {
	ctx := WithCurrentWorkingDirectory(context.Background(), "/ws/api")

	fed := nestedExecOptions(ctx, types.MagusCommandOptions{Stdin: "ghp_token", Quiet: true}, nil)
	// A nested magus is always captured; the caller reads its output.
	assert.Equal(t, spawn.ExecOptions{Dir: "/ws/api", Capture: true, Quiet: true, Stdin: "ghp_token"}, fed)

	bare := nestedExecOptions(ctx, types.MagusCommandOptions{}, nil)
	assert.Empty(t, bare.Stdin, "no opt means the child inherits nothing on stdin")
}

// TestNestedResultHonorsAllowFailure pins magus.cmd/run/describe to proc.exec's
// contract: a non-zero exit raises, unless opts.allow_failure returns the result.
func TestNestedResultHonorsAllowFailure(t *testing.T) {
	full := []string{"status"}
	failed := spawn.ExecResult{Stdout: "partial\n", Stderr: "boom\n", Code: 3, Started: true}

	_, err := nestedResult("cmd", full, failed, nil, types.MagusCommandOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "magus.cmd: status exited with code 3")

	got, err := nestedResult("cmd", full, failed, nil, types.MagusCommandOptions{AllowFailure: true})
	require.NoError(t, err)
	assert.Equal(t, types.ExecResult{Stdout: "partial", Stderr: "boom", Code: 3, OK: false}, got)

	notStarted := spawn.ExecResult{Code: -1}
	_, err = nestedResult("cmd", full, notStarted, errors.New("no such file"), types.MagusCommandOptions{})
	require.ErrorContains(t, err, "no such file")
	got, err = nestedResult("cmd", full, notStarted, errors.New("no such file"), types.MagusCommandOptions{AllowFailure: true})
	require.NoError(t, err)
	assert.Equal(t, -1, got.Code)
	assert.False(t, got.OK)

	_, err = nestedResult("cmd", full, spawn.ExecResult{Code: -1}, types.ExecDenied, types.MagusCommandOptions{AllowFailure: true})
	assert.ErrorIs(t, err, types.ExecDenied, "a sandbox denial is never swallowed")

	ok, err := nestedResult("cmd", full, spawn.ExecResult{Stdout: "fine", Started: true}, nil, types.MagusCommandOptions{})
	require.NoError(t, err)
	assert.True(t, ok.OK)
}

// diagnosticsWorkspace is a workspace whose magus.yaml declares the WS prefix and a docs
// template, the declaration magus\diagnosticCode and magus\raise read.
type diagnosticsWorkspace struct {
	types.WorkspaceRepository
	root string
}

func (w diagnosticsWorkspace) Root() string { return w.root }

// raiseContext is a context whose workspace declares the WS prefix.
func (s *stdSuite) raiseContext() context.Context {
	root := s.T().TempDir()
	yaml := "diagnostics:\n  prefix: WS\n  docs: https://ws.example/codes/{code}/\n"
	s.Require().NoError(os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(yaml), 0o644))
	return types.WithWorkspace(s.T().Context(), diagnosticsWorkspace{root: root})
}

// wsCode is the DiagnosticCode object magus\diagnosticCode would mint for text.
func wsCode(text string) types.DiagnosticCodeRef {
	return types.DiagnosticCodeRef{Code: text, Origin: types.DiagnosticOriginWorkspace}
}

func (s *stdSuite) TestDiagnosticCodeMintsAWorkspaceCode() {
	got, err := MagusDiagnosticCode(s.raiseContext(), "WS1003")
	s.Require().NoError(err)
	s.Equal(wsCode("WS1003"), got)
}

func (s *stdSuite) TestDiagnosticCodeRefusesAnMGSCode() {
	_, err := MagusDiagnosticCode(s.raiseContext(), "MGS1021")
	s.ErrorContains(err, "MGS catalog magus ships")
}

func (s *stdSuite) TestDiagnosticCodeRefusesAForeignPrefix() {
	_, err := MagusDiagnosticCode(s.raiseContext(), "ACME1001")
	s.ErrorContains(err, "not under this workspace's prefix WS")
}

func (s *stdSuite) TestDiagnosticCodeOutsideAWorkspaceNamesTheKey() {
	_, err := MagusDiagnosticCode(s.T().Context(), "WS1003")
	s.ErrorContains(err, "diagnostics.prefix in magus.yaml")
}

func (s *stdSuite) TestRaiseCarriesCodeMessageAndTheWorkspacePage() {
	err := MagusRaise(s.raiseContext(), wsCode("WS1001"), "registry rejected the manifest", raiseOptions(nil, ""))
	var de *diagnostics.Error
	s.Require().ErrorAs(err, &de, "must be a diagnostics.Error so Buzz sees structured fields")
	s.Equal(map[string]string{
		"code":    "WS1001",
		"message": "registry rejected the manifest",
		"url":     "https://ws.example/codes/WS1001/",
	}, de.BuzzError())
}

func (s *stdSuite) TestRaiseURLOverridesTheWorkspacePage() {
	err := MagusRaise(s.raiseContext(), wsCode("WS1001"), "boom", raiseOptions(nil, "https://other.example/WS1001"))
	s.Equal("[WS1001] boom\n  see: https://other.example/WS1001", err.Error())
}

func (s *stdSuite) TestRaiseOmitsTheURLWhenNoPageIsDeclared() {
	root := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("diagnostics:\n  prefix: WS\n"), 0o644))
	ctx := types.WithWorkspace(s.T().Context(), diagnosticsWorkspace{root: root})
	var de *diagnostics.Error
	s.Require().ErrorAs(MagusRaise(ctx, wsCode("WS1002"), "no url", raiseOptions(nil, "")), &de)
	s.Equal(map[string]string{"code": "WS1002", "message": "no url"}, de.BuzzError(),
		"an absent url must be absent, not an empty field a caller has to test for")
}

func (s *stdSuite) TestRaiseRechecksAnMGSCodeInTheObject() {
	err := MagusRaise(s.raiseContext(), types.DiagnosticCodeRef{Code: "MGS9999"}, "impersonating magus", types.RaiseOptions{})
	s.ErrorContains(err, "MGS catalog magus ships")
	var de *diagnostics.Error
	s.False(errors.As(err, &de), "refusing must not produce the very diagnostic it rejected")
}

func (s *stdSuite) TestRaiseRequiresAMessage() {
	s.ErrorContains(MagusRaise(s.raiseContext(), wsCode("WS1001"), "", raiseOptions(nil, "")), "needs a message")
}

func (s *stdSuite) TestRaiseSplicesAStringCauseAndUnwrapsToIt() {
	err := MagusRaise(s.raiseContext(), wsCode("WS1001"), "push failed", raiseOptions("exec docker: exit 1", ""))
	s.ErrorContains(err, "[WS1001] push failed: exec docker: exit 1")
	s.ErrorContains(errors.Unwrap(err), "exec docker: exit 1")
}

func (s *stdSuite) TestRaiseKeepsACodedCauseMatchable() {
	inner := map[string]any{"code": "MGS2001", "message": "spell not found", "url": "https://x/MGS2001"}
	err := MagusRaise(s.raiseContext(), wsCode("WS1001"), "build failed", raiseOptions(inner, ""))
	s.ErrorIs(err, diagnostics.Code("MGS2001"), "the inner code must still match after wrapping")
}

// raiseOptions builds the RaiseOptions record a magusfile would pass.
func raiseOptions(cause any, url string) types.RaiseOptions {
	return types.RaiseOptions{Cause: cause, URL: url}
}

// recordingAnalyzer is a workspace that can answer the insight lenses, recording the
// options it was handed so a test can prove they arrived.
type recordingAnalyzer struct {
	groupingLens types.Grouping
	types.WorkspaceRepository
	got             types.HistoryMetricsOptions
	failTrend       bool
	failConformance error
	conformanceLens types.Conformance
}

func (f *recordingAnalyzer) Hotspots(_ context.Context, o types.HistoryMetricsOptions) (types.Hotspots, error) {
	f.got = o
	return types.Hotspots{Definition: "hot"}, nil
}
func (f *recordingAnalyzer) Affinity(_ context.Context, o types.HistoryMetricsOptions) (types.Affinity, error) {
	f.got = o
	return types.Affinity{}, nil
}
func (f *recordingAnalyzer) Ownership(context.Context, types.HistoryMetricsOptions) (types.Ownership, error) {
	return types.Ownership{}, nil
}
func (f *recordingAnalyzer) Trend(context.Context, types.HistoryMetricsOptions) (types.Trend, error) {
	if f.failTrend {
		return types.Trend{}, errors.New("no history")
	}
	return types.Trend{}, nil
}
func (f *recordingAnalyzer) Volatility(context.Context) (types.Volatility, error) {
	return types.Volatility{}, errors.New("no run history")
}
func (f *recordingAnalyzer) Unreferenced(context.Context) (types.Unreferenced, error) {
	return types.Unreferenced{}, errors.New("no symbol index")
}

func (f *recordingAnalyzer) Duplication(context.Context) (types.Duplication, error) {
	return types.Duplication{}, errors.New("no symbol index")
}

func (f *recordingAnalyzer) Conformance(context.Context) (types.Conformance, error) {
	return f.conformanceLens, f.failConformance
}

func (f *recordingAnalyzer) ModelTierLens(context.Context) (types.ModelTiers, error) {
	return types.ModelTiers{}, errors.New("no run history")
}

func (f *recordingAnalyzer) GroupingLens(context.Context) (types.Grouping, error) {
	return f.groupingLens, nil
}

func TestAffinityIsServedInProcess(t *testing.T) {
	t.Parallel()

	a := &recordingAnalyzer{}
	ctx := types.WithWorkspace(t.Context(), a)

	got, err := MagusAffinity(ctx, types.HistoryMetricsOptions{})
	require.NoError(t, err)
	assert.Equal(t, types.Affinity{}, got)
	assert.Equal(t, 500, a.got.Commits)
}

// cannedToolReporter is a workspace that reports its tools, so magus\describe.tool() can be shown
// to answer in-process rather than forking a nested magus.
type cannedToolReporter struct {
	types.WorkspaceRepository
	report types.ToolReport
}

func (f *cannedToolReporter) Tools(context.Context, ...string) (types.ToolReport, error) {
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
	got, err := MagusDescribeTool(types.WithWorkspace(t.Context(), &cannedToolReporter{report: want}))
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestAffinityNeedsAWorkspace(t *testing.T) {
	t.Parallel()
	_, err := MagusAffinity(t.Context(), types.HistoryMetricsOptions{})
	require.Error(t, err)
}

func TestAffinityMapsTheOptionsItTakes(t *testing.T) {
	t.Parallel()
	a := &recordingAnalyzer{}
	ctx := types.WithWorkspace(t.Context(), a)
	_, err := MagusAffinity(ctx, types.HistoryMetricsOptions{Commits: 42, Since: "90d"})
	require.NoError(t, err)
	assert.Equal(t, types.HistoryMetricsOptions{Commits: 42, Since: "90d"}, a.got)

	_, err = MagusAffinity(ctx, types.HistoryMetricsOptions{Commits: -1})
	require.ErrorContains(t, err, "commits must not be negative")
}

// ledgerWorkspace is a workspace that can back a job Store: it
// carries the cache directory jobStoreFromContext needs beyond
// types.WorkspaceRepository's own Root.
type ledgerWorkspace struct {
	types.WorkspaceRepository
	cacheDir string
	root     string
}

func (f *ledgerWorkspace) CacheDir() string { return f.cacheDir }
func (f *ledgerWorkspace) Root() string     { return f.root }

// Not parallel: the job store resolves the per-repository state directory, and
// jobStoreFromContext offers no seam to redirect it, so the environment is the only
// thing keeping these rows out of the developer's own store.
func TestLedgerIsServedInProcess(t *testing.T) {
	testkit.Isolate(t)

	ctx := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})

	_, err := MagusPutJob(ctx, "u1", types.JobPut{Criteria: new("ship it"), State: new(types.StateRunning), ReadOnly: new(true)})
	require.NoError(t, err)

	report, err := MagusListJob(ctx)
	require.NoError(t, err, "a workspace with a cache directory answers here, with no subprocess")
	require.Len(t, report.Jobs, 1)
	assert.Equal(t, "u1", report.Jobs[0].ID) //nolint:fieldwise // a stored job row carries store-stamped schema, origin, checkout root and timestamps this test does not construct
	assert.Equal(t, types.StateRunning, report.Jobs[0].State)
}

// A guard rule runs under the rows the guard pinned: list answers from them even with a
// workspace on the context whose store holds something else, and every write refuses.
func TestJobListAnswersFromAPinnedSnapshot(t *testing.T) {
	testkit.Isolate(t)
	ws := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})
	_, err := MagusPutJob(ws, "on-disk", types.JobPut{Criteria: new("not what the guard read"), ReadOnly: new(true)})
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
	_, err = MagusPutJob(ctx, pinned.ID, types.JobPut{State: new(types.StatePass)})
	assert.ErrorContains(t, err, "read-only")
	_, _, err = MagusRegisterJob(ctx, pinned.ID)
	assert.ErrorContains(t, err, "read-only")
	_, err = MagusClearJob(ctx)
	assert.ErrorContains(t, err, "read-only")
	report, err := MagusListJob(ws)
	require.NoError(t, err)
	require.Len(t, report.Jobs, 1)
	assert.Equal(t, "on-disk", report.Jobs[0].ID, "nothing was written through the snapshot")
}

// magus\workspace().jobs() measures overlaps as `magus ls jobs` does.
// So its footprints are exactly job.MeasureOverlaps over the same rows.
func TestJobListMeasuresOverlapFootprints(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	testkit.Isolate(t)
	repo := testkit.NewRepository(t, map[string]string{
		".gitattributes": "*.go diff=golang\n",
		"api/x.go":       "package api\n\nfunc X() {\n\treturn\n}\n",
	})
	root, rev := repo.Root(), repo.Head()
	repo.Write("api/x.go", "package api\n\nfunc X() {\n\tpanic(1)\n}\n")

	ctx := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: t.TempDir(), root: root})
	check, err := types.ParseLeaseCheck("test .")
	require.NoError(t, err)
	for id, paths := range map[string][]string{"a": {"api/*.go"}, "b": {"api/x.go"}} {
		_, err := MagusPutJob(ctx, id, types.JobPut{Checkpoint: new(rev), WritePaths: new(paths), Check: new(check), State: new(types.StateRunning)})
		require.NoError(t, err)
		_, _, err = MagusRegisterJob(ctx, id)
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
	_, err = MagusPutJob(t.Context(), "u1", types.JobPut{})
	require.Error(t, err)
	_, err = MagusClearJob(t.Context())
	require.Error(t, err)
}

// A stop rule runs under the rows the guard pinned.
func TestJobUnmergedAnswersFromThePinnedRows(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "r", State: types.StatePass, ReadOnly: true, Result: &types.JobResult{FollowUps: []string{"later"}}}
	ctx := job.WithSnapshot(t.Context(), job.Snapshot{Rows: []types.Job{row}})

	got, err := MagusUnmergedJob(ctx, t.TempDir())

	require.NoError(t, err)
	require.Len(t, got.Rows, 1)
	assert.Equal(t, "later", got.Rows[0].Item)
}

func TestJobUnmergedRaisesAPinnedReadError(t *testing.T) {
	t.Parallel()

	ctx := job.WithSnapshot(t.Context(), job.Snapshot{Err: errors.New("unreadable")})

	_, err := MagusUnmergedJob(ctx, t.TempDir())

	require.ErrorContains(t, err, "unreadable")
}

// TestLedgerNeedsACacheDir covers the workspace that answers the reading verbs
// (ls, affected, ...) but was never given a cache directory: a test double, not the
// real *magus.Magus.
func TestLedgerNeedsACacheDir(t *testing.T) {
	t.Parallel()

	ctx := types.WithWorkspace(t.Context(), &recordingAnalyzer{})
	_, err := MagusListJob(ctx)
	assert.ErrorContains(t, err, "cache directory")
}

// decodedPut decodes a magus\workspace().putJob opts map as the binding does.
func decodedPut(t *testing.T, opts map[string]any) types.JobPut {
	t.Helper()
	put, err := job.DecodePut(opts)
	require.NoError(t, err)
	return put
}

// TestPutLedgerMergesRatherThanReplaces proves a decoded putJob map merges.
// A later put naming only `state` must not erase an earlier put's goal.
// Not parallel, and the env is why: see TestLedgerIsServedInProcess above. A root of
// "" hashes to the same state directory in every checkout, so these rows would land in
// the developer's own store and the clear below would drop what it found there.
func TestPutLedgerMergesRatherThanReplaces(t *testing.T) {
	testkit.Isolate(t)

	ctx := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})

	_, err := MagusPutJob(ctx, "u1", decodedPut(t, map[string]any{"criteria": "the declared goal", "write_paths": "internal/job", "check": "test ."}))
	require.NoError(t, err)

	got, err := MagusPutJob(ctx, "u1", decodedPut(t, map[string]any{"state": "pass"}))
	require.NoError(t, err)
	assert.Equal(t, types.StatePass, got.State) //nolint:fieldwise // a stored job row carries store-stamped schema, origin, checkout root and timestamps this test does not construct
	assert.Equal(t, "the declared goal", got.Criteria, "the state advance must not erase the row")
	assert.Equal(t, []string{"internal/job"}, got.WritePaths)
	require.NotNil(t, got.Check, "the state advance must not erase the check")
}

// TestPutLedgerRejectsAnUnknownState proves a mistyped state is reported, not
// silently ignored; internal/job.PutMerge is what enforces this, and this pins that
// the Go door does not swallow its error.
func TestPutLedgerRejectsAnUnknownState(t *testing.T) {
	testkit.Isolate(t)

	ctx := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})
	_, err := MagusPutJob(ctx, "u1", types.JobPut{State: new(types.JobState("passed"))})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_return")
}

func TestClearLedgerReportsHowManyRowsItDropped(t *testing.T) {
	testkit.Isolate(t)

	ctx := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})
	readOnly := types.JobPut{ReadOnly: new(true)}
	_, err := MagusPutJob(ctx, "u1", readOnly)
	require.NoError(t, err)
	_, err = MagusPutJob(ctx, "u2", readOnly)
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
	workspace := &ledgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()}
	ctx := types.WithJobLease(types.WithWorkspace(t.Context(), workspace), "fleet/captured")

	store, err := jobStoreFromContext(ctx, "job.put")
	require.NoError(t, err)
	assert.Equal(t, "fleet/captured", store.Actor().Lease)
}

// The captured lease is the process's claim, so a checkout's binding outranks it here
// exactly as it does for `magus job` and the guard.
func TestJobStorePrefersTheCheckoutsBindingOverTheCapturedClaim(t *testing.T) {
	testkit.Isolate(t)
	workspace := &ledgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()}
	stateBase, err := xdg.StateDir()
	require.NoError(t, err)
	require.NoError(t, job.NewStore(job.Location{StateBase: stateBase, CacheDir: workspace.cacheDir}).Bind(job.Caller{}, "fleet/bound"))
	ctx := types.WithJobLease(types.WithWorkspace(t.Context(), workspace), "fleet/captured")

	store, err := jobStoreFromContext(ctx, "job.put")
	require.NoError(t, err)
	assert.Equal(t, "fleet/bound", store.Actor().Lease)
}

// TestLedgerBindingAndStoreAgree pins that putJob and the store share one file.
// A row put through magus\workspace().putJob shows in the store.
// internal/job.Store's own path derivation makes that true.
// Neither side names the other.
func TestLedgerBindingAndStoreAgree(t *testing.T) {
	stateBase := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateBase)

	cacheDir, root := t.TempDir(), t.TempDir()
	ctx := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: cacheDir, root: root})

	_, err := MagusPutJob(ctx, "u1", types.JobPut{Criteria: new("shared row"), ReadOnly: new(true)})
	require.NoError(t, err)

	store := job.NewStore(job.Location{StateBase: stateBase, CacheDir: cacheDir, Root: root})
	leases, err := store.List()
	require.NoError(t, err)
	require.Len(t, leases, 1)
	assert.Equal(t, "shared row", leases[0].Criteria)
}

// builtGraphWorkspace is a workspace holding a hand-built knowledge graph and an output
// store, which is all the graph members and magus\output read off it.
type builtGraphWorkspace struct {
	types.WorkspaceRepository
	g        *knowledge.Graph
	cacheDir string
	projects []*types.Project
}

func (f *builtGraphWorkspace) All() []*types.Project { return f.projects }

func (f *builtGraphWorkspace) KnowledgeGraph(context.Context, types.GraphRequest) (*knowledge.Graph, error) {
	return f.g, nil
}
func (f *builtGraphWorkspace) SymbolGaps(context.Context) ([]types.KnowledgeSymbolGap, bool) {
	return nil, true
}
func (f *builtGraphWorkspace) CacheDir() string { return f.cacheDir }

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
			Source: fmt.Sprintf("file:pkg/%s.go", f), Target: graphSymbol,
			Relation: types.RelationReferences, Confidence: types.ConfidenceExtracted, Score: 1,
			Provenance: "scip count=1 lines=3",
		})
	}
	return types.WithWorkspace(t.Context(), &builtGraphWorkspace{g: g, cacheDir: t.TempDir()})
}

func TestGraphMembersAreServedInProcess(t *testing.T) {
	t.Parallel()
	ctx := graphContext(t)

	q, err := MagusQuery(ctx, "kind=target", types.KnowledgeQueryOptions{Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, q.MatchCount, "the total survives the window")
	assert.Len(t, q.Matches, 1)
	assert.Equal(t, types.VerdictFound, q.Answer.Verdict)

	x, err := MagusExplain(ctx, "target:pkg/a:build")
	require.NoError(t, err)
	assert.Equal(t, "target:pkg/a:build", x.Node.ID)

	p, err := MagusPath(ctx, "target:pkg/a:build", "target:pkg/a:test", types.KnowledgePathOptions{})
	require.NoError(t, err)
	assert.True(t, p.Found)

	unlinked, err := MagusPath(ctx, "project:pkg/a", "project:pkg/b", types.KnowledgePathOptions{})
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
	_, err = MagusPath(ctx, "target:pkg/a:build", "target:pkg/z:nope", types.KnowledgePathOptions{})
	require.Error(t, err)
	_, err = MagusQuery(ctx, " ", types.KnowledgeQueryOptions{})
	require.Error(t, err)
}

func TestRefsWindowsSitesAndKeepsTheTotals(t *testing.T) {
	t.Parallel()
	ctx := graphContext(t)

	r, err := MagusRefs(ctx, graphSymbol, types.KnowledgeRefsOptions{Offset: 1, Limit: 1})
	require.NoError(t, err)
	// The counts keep describing the whole set; only Refs is windowed. The answer's coverage is
	// the fixture's, not this test's to pin.
	assert.Equal(t, types.KnowledgeRefsOutput{
		Definition:    types.KnowledgeRefsDefinition,
		APIVersion: types.KnowledgeAPIVersion,
		Symbol:        graphSymbol,
		Label:         "Foo",
		FileCount:     3,
		RefCount:      3,
		Refs:          []types.KnowledgeRefSite{{File: "pkg/b.go", Count: 1, Lines: []int{3}}},
		Answer:        r.Answer,
	}, r)

	absent, err := MagusRefs(ctx, "symbol:example.com/x Missing#", types.KnowledgeRefsOptions{})
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
	ctx := types.WithWorkspace(t.Context(), &builtGraphWorkspace{g: g, cacheDir: t.TempDir()})
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

	_, err := MagusRefs(ctx, graphSymbol, types.KnowledgeRefsOptions{Offset: -1})
	require.ErrorContains(t, err, "offset must not be negative")
	_, err = MagusQuery(ctx, "kind=target", types.KnowledgeQueryOptions{Budget: -1})
	require.ErrorContains(t, err, "budget must not be negative")
}

func TestWorkspaceMembersNeedAWorkspace(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	calls := map[string]func() error{
		"query":        func() error { _, err := MagusQuery(ctx, "x", types.KnowledgeQueryOptions{}); return err },
		"explain":      func() error { _, err := MagusExplain(ctx, "x"); return err },
		"path":         func() error { _, err := MagusPath(ctx, "x", "y", types.KnowledgePathOptions{}); return err },
		"refs":         func() error { _, err := MagusRefs(ctx, "x", types.KnowledgeRefsOptions{}); return err },
		"stats":        func() error { _, err := MagusStats(ctx, ""); return err },
		"importGraph":  func() error { _, err := MagusImportGraph(ctx); return err },
		"dir":          func() error { _, err := MagusDir(ctx, "x"); return err },
		"dirs":         func() error { _, err := MagusDirs(ctx, "**", types.DirsOptions{}); return err },
		"layer":        func() error { _, err := MagusLayer(ctx, "x"); return err },
		"neighborhood": func() error { _, err := MagusNeighborhood(ctx, "x", types.KnowledgeNeighborhoodOptions{}); return err },
		"output":       func() error { _, err := MagusOutput(ctx, "out1a2b3c"); return err },
		"change":       func() error { _, err := MagusChangeOpen(ctx, "", types.ChangeOptions{}); return err },
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

// A guard rule has no workspace on its context; the guard pins the judged checkout's store.
func TestOutputReadsTheStoreAGuardPinned(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	ctx := types.WithOutputCacheDir(t.Context(), cacheDir)

	desc, err := cache.NewOutputStore(cacheDir).Persist(ctx, strings.Repeat("cd", 32),
		[]byte("FAIL\n"), cache.OutputDescriptor{Project: "pkg/a", Target: "test", Failed: true})
	require.NoError(t, err)

	got, err := MagusOutput(ctx, desc.Ref)
	require.NoError(t, err)
	assert.Equal(t, types.OutputRecord{Ref: desc.Ref, Project: "pkg/a", Target: "test", Failed: true, Output: "FAIL\n"}, got)

	_, err = MagusOutput(ctx, "out0000000000")
	require.ErrorContains(t, err, "no stored output")
	_, err = MagusQuery(ctx, "x", types.KnowledgeQueryOptions{})
	require.ErrorIs(t, err, types.MagusfileOnlyMember, "the pin opens the output store and no other member")
}

// With no store, "no passed full test" claims a read nobody made.
func (s *stdSuite) TestForkFactsRefusesPinnedRowsWithNoPinnedStore() {
	ctx := job.WithSnapshot(s.T().Context(), job.Snapshot{})
	_, err := MagusForkFactsJob(ctx, s.T().TempDir())
	s.Require().ErrorIs(err, types.MagusfileOnlyMember)
}

func (s *stdSuite) TestForkFactsRefusesAWorkspaceWithNoCacheDirectory() {
	ctx := job.WithSnapshot(types.WithWorkspace(s.T().Context(), checkoutWorkspace{}), job.Snapshot{})
	_, err := MagusForkFactsJob(ctx, s.T().TempDir())
	s.Require().ErrorContains(err, "this workspace has no cache directory")
}

func (s *stdSuite) TestForkFactsReadsThePinnedStore() {
	cacheDir := s.T().TempDir()
	ctx := job.WithSnapshot(types.WithOutputCacheDir(s.T().Context(), cacheDir), job.Snapshot{})
	desc, err := cache.NewOutputStore(cacheDir).Persist(ctx, strings.Repeat("ef", 32),
		[]byte("ok\n"), cache.OutputDescriptor{Project: ".", Target: "test", Revision: "abc"})
	s.Require().NoError(err)

	facts, err := MagusForkFactsJob(ctx, s.T().TempDir())
	s.Require().NoError(err)
	s.Equal(desc.Ref, facts.LastTestRef)
}

// checkoutWorkspace is a workspace rooted at this package's own checkout.
type checkoutWorkspace struct {
	types.WorkspaceRepository
}

func (checkoutWorkspace) Root() string                 { return "." }
func (checkoutWorkspace) VCSOptions() types.VCSOptions { return types.VCSOptions{} }

// A Change records the tree the way `magus vcs checkpoint` resolves it, so
// the two cannot describe it differently.
func TestChangeRecordsTheCheckpointTheCLIPrints(t *testing.T) {
	t.Parallel()
	ctx := types.WithWorkspace(t.Context(), checkoutWorkspace{})

	c, gotErr := MagusChangeOpen(ctx, "", types.ChangeOptions{})

	res, err := vcs.Resolve(ctx, ".", "", types.VCSOptions{})
	require.NoError(t, err)
	want, wantErr := vcs.Checkpoint(ctx, ".", res, false)
	assert.Equal(t, want, c.At)
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

// withJobGrader installs g for the test and restores whatever was installed. The
// grader is process-wide, so a test that uses it does not run in parallel.
func withJobGrader(t *testing.T, g job.CheckGrader) {
	t.Helper()
	previous := job.SetCheckGrader(g)
	t.Cleanup(func() { job.SetCheckGrader(previous) })
}

// magus\workspace().putJob forks by merge, and the multi-agent skill uses it.
// So it is held to the guard's verdict on the check, as `magus job fork` is.
// The installed grader refuses a denied check, with nothing written.
func TestMagusPutJobGradesTheCheckLikeFork(t *testing.T) {
	testkit.Isolate(t)
	var roots []string
	withJobGrader(t, func(_ context.Context, root string, row types.Job) job.CheckVerdict {
		roots = append(roots, root)
		if row.Check.Target != "go-build" {
			return job.CheckVerdict{}
		}
		return job.CheckVerdict{Denied: true, Rule: "worker-builds-magus", Reason: "a worker does not build magus"}
	})

	root := t.TempDir()
	ctx := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: t.TempDir(), root: root})
	put := func(check string) error {
		c, err := types.ParseLeaseCheck(check)
		require.NoError(t, err)
		_, err = MagusPutJob(ctx, "w", types.JobPut{WritePaths: new([]string{"run.go"}), Check: &c})
		return err
	}

	err := put("go-build .")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "would be denied to its worker by the guard (worker-builds-magus)")
	assert.Contains(t, err.Error(), "`go::go-vet .`")
	assert.Equal(t, []string{root}, roots, "the grader judges the workspace the put runs in")

	stored, err := jobStoreFromContext(ctx, "job.list")
	require.NoError(t, err)
	rows, err := stored.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused put writes nothing")

	require.NoError(t, put("go::go-vet ."))
	rows, err = stored.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestMagusPutJobRaisesEveryProblemAtOnce(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "src"), 0o755))
	ctx := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: t.TempDir(), root: root})

	_, err := MagusPutJob(ctx, "u1", types.JobPut{WritePaths: new([]string{"src"})})

	require.Error(t, err)
	assert.ErrorIs(t, err, types.WritePathIsDirectory, "the directory is named")
	assert.ErrorIs(t, err, types.JobUngraded, "so is the missing grade, in the same error")
}

func TestMagusJobSchemaIsTheDeclarationSchema(t *testing.T) {
	t.Parallel()

	got, err := MagusJobSchema(t.Context())
	require.NoError(t, err)

	var want map[string]any
	require.NoError(t, json.Unmarshal([]byte(job.DeclarationSchema), &want))
	assert.Equal(t, want, got)
	assert.Contains(t, got, "properties")
}

type ratedWorkspace struct {
	ledgerWorkspace
	tiers *types.ModelTierSet
	rows  []types.ModelPricing
}

func (w *ratedWorkspace) ModelTiers(context.Context) *types.ModelTierSet { return w.tiers }
func (w *ratedWorkspace) ModelTierMatrix() types.ModelTierMatrix         { return nil }
func (w *ratedWorkspace) CheckSpawn(_, _ types.ModelTier) error          { return nil }
func (w *ratedWorkspace) ModelRows(context.Context) func() []types.ModelPricing {
	return func() []types.ModelPricing { return w.rows }
}

func TestJobPutResolvesAModelNamedByAHarnessModelRow(t *testing.T) {
	testkit.Isolate(t)
	set, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum: "ModelTier", Site: "magusfile.buzz:3", Cases: []string{"economy", "standard", "principal"}, Default: 1,
		Bounds: []types.ModelTierBounds{{}, {}, {}},
		Skills: []types.SkillVariant{types.SkillVariantFull, types.SkillVariantShort, types.SkillVariantShort},
	})
	require.NoError(t, err)
	ws := &ratedWorkspace{
		ledgerWorkspace: ledgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()},
		tiers:           set,
		rows:            []types.ModelPricing{{ID: "claude-opus-9", Alias: "opus", Tier: "principal"}},
	}

	got, err := MagusPutJob(types.WithWorkspace(t.Context(), ws), "w1", types.JobPut{Criteria: new("ship it"), Model: new("opus"), ReadOnly: new(true)})
	require.NoError(t, err)
	require.NotNil(t, got.ModelTier)
	tier := *got.ModelTier
	tier.Reason = ""
	assert.Equal(t, types.JobModelTier{Name: "principal", Rank: 2, Model: "opus", Skills: tier.Skills, Source: tier.Source, Site: tier.Site}, tier)
}

// magus\cmd's nested magus gets the target layer; its caller's exec does not.
func (s *stdSuite) TestNestedMagusCarriesTheTargetLayerItsCallerDoesNot() {
	granted := filepath.Join(s.T().TempDir(), "f")
	p := sandbox.BuildPolicy(sandbox.PolicyOptions{Workspace: s.T().TempDir()}).WithNestedTargets(map[string]spells.Sandbox{
		".:test": {Allow: []spells.SandboxAllow{{Path: filepath.Dir(granted), Mode: spells.SandboxAccessRO}}},
	})
	ctx := sandbox.WithPolicy(s.T().Context(), p)

	nested := nestedMagusContext(ctx)
	s.Require().NoError(sandbox.PolicyFromContext(nested).CheckRead(nested, granted), "the nested magus")
	s.Error(sandbox.PolicyFromContext(ctx).CheckRead(ctx, granted), "the script's own exec")
}
