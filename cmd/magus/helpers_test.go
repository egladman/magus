package main

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/gcpolicy"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// TestCanonicalTarget covers the short-alias expansions and the passthrough.
func TestCanonicalTarget(t *testing.T) {
	assert.Equal(t, "format", canonicalTarget("fmt"))
	assert.Equal(t, "generate", canonicalTarget("gen"))
	assert.Equal(t, "build", canonicalTarget("build"))
	assert.Equal(t, "", canonicalTarget(""))
}

// TestWithDefaultCharms covers the magus.yaml default_charms merge: defaults are
// applied to a run, per-run charms stack on top (exact dups dropped), and the
// --no-default-charms escape ignores the defaults.
func TestWithDefaultCharms(t *testing.T) {
	cases := []struct {
		name      string
		perRun    []string
		defaults  []string
		noDefault bool
		want      []string
	}{
		{"defaults applied to a bare run", nil, []string{"rw"}, false, []string{"rw"}},
		{"per-run stacks on defaults", []string{"debug"}, []string{"rw"}, false, []string{"rw", "debug"}},
		{"exact duplicate dropped", []string{"rw"}, []string{"rw"}, false, []string{"rw"}},
		{"no-default-charms ignores defaults", []string{"debug"}, []string{"rw"}, true, []string{"debug"}},
		{"no defaults is identity", []string{"debug"}, nil, false, []string{"debug"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, withDefaultCharms(c.perRun, c.defaults, c.noDefault))
		})
	}
}

// TestSplitOnDashDash pins the forwarding boundary: everything after "--" is the spell's
// verbatim argv, and an absent separator must not manufacture an empty tail.
func TestSplitOnDashDash(t *testing.T) {
	before, after := splitOnDashDash([]string{"test", "web"})
	assert.Equal(t, []string{"test", "web"}, before)
	assert.Nil(t, after)

	before, after = splitOnDashDash([]string{"test", "--", "-run", "TestX"})
	assert.Equal(t, []string{"test"}, before)
	assert.Equal(t, []string{"-run", "TestX"}, after)

	// A trailing separator forwards an empty argv, which is different from forwarding none.
	before, after = splitOnDashDash([]string{"test", "--"})
	assert.Equal(t, []string{"test"}, before)
	assert.Equal(t, []string{}, after)
}

func TestListTargetsPrintsOnlyPaths(t *testing.T) {
	out := captureStdout(t, func() {
		listTargets("affected", []types.Target{{Path: "web"}, {Path: "api"}}, "vcs")
	})
	assert.Equal(t, "web\napi\n", out, "the summary is a log line; stdout carries only the paths")

	assert.Empty(t, captureStdout(t, func() { listTargets("all", nil, "") }))

	out = captureStdout(t, func() {
		listTargets("cwd", []types.Target{{Path: "web"}}, "")
	})
	assert.Equal(t, "web\n", out)
}

// TestReportedRunErr covers the gate that stops a fan-out failure being printed twice: the
// cache's handler already rendered a per-project line, so the top-level handler must exit
// silently rather than reprint it.
func TestReportedRunErr(t *testing.T) {
	assert.False(t, reportedRunError(nil))
	assert.False(t, reportedRunError(errors.New("plain")))
	assert.True(t, reportedRunError(&types.SpellErrors{Project: "web"}))
	assert.True(t, reportedRunError(errors.Join(errors.New("other"), &types.SpellErrors{Project: "web"})))
	assert.True(t, reportedRunError(fmt.Errorf("run: %w", cache.ReportedError{Err: errors.New("step failed")})),
		"a step failure the cache logged is not printed again")
}

// AdmissionRefusalSuite pins that a refusal from cache.AdmitMachine is printed
// once: it runs outside any step, so no cache.error record says it.
type AdmissionRefusalSuite struct {
	suite.Suite
	logged bytes.Buffer
}

func TestAdmissionRefusalSuite(t *testing.T) { suite.Run(t, new(AdmissionRefusalSuite)) }

func (s *AdmissionRefusalSuite) SetupTest() {
	s.T().Cleanup(snapshotGlobals())
	testkit.Isolate(s.T())
	globalCfg.Broker = types.BrokerBestEffort
	globalCfg.BudgetWait = 10 * time.Millisecond
	s.logged.Reset()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.logged, nil)))
	s.T().Cleanup(func() { slog.SetDefault(prev) })
}

func (s *AdmissionRefusalSuite) TestAForkRefusalIsPrinted() {
	_, err := admitForkValidation(s.T().Context(), &fullBudget{}, s.T().TempDir())
	s.Equal(cache.ExitCodeMachineBusy, mapExitCode(err))
	s.Contains(s.logged.String(), "not starting (root) job-fork")
}

func (s *AdmissionRefusalSuite) TestAGraphBuildRefusalIsPrinted() {
	_, err := admitGraphBuild(s.T().Context(), &fullBudget{}, s.T().TempDir())
	s.Equal(cache.ExitCodeMachineBusy, mapExitCode(err))
	s.Contains(s.logged.String(), "not starting (root) graph-build")
}

func (s *AdmissionRefusalSuite) TestASessionBriefRefusalIsPrinted() {
	_, err := admitSessionBrief(s.T().Context(), &fullBudget{}, s.T().TempDir())
	s.Equal(cache.ExitCodeMachineBusy, mapExitCode(err))
	s.Contains(s.logged.String(), "not starting (root) session-brief")
}

func (s *AdmissionRefusalSuite) TestAPausedJobRefusalIsPrinted() {
	err := types.ExitError{Code: cache.ExitCodeMachineBusy, Err: errors.New("magus job exec: job x is paused")}
	s.Equal(cache.ExitCodeMachineBusy, mapExitCode(err))
	s.Contains(s.logged.String(), "job x is paused")
}

func (s *AdmissionRefusalSuite) TestABareExitStaysSilent() {
	s.Equal(3, mapExitCode(types.ExitError{Code: 3}))
	s.Empty(s.logged.String())
}

// briefRefusalRootVar names the workspace TestBriefRefusalHelperProcess runs
// the CLI in; set, it marks that process as the helper.
const briefRefusalRootVar = "MAGUS_TEST_BRIEF_REFUSAL_ROOT"

func (s *AdmissionRefusalSuite) TestTheCLIPrintsTheBriefRefusal() {
	w := testkit.NewWorkspace(s.T())
	w.Magusfile("import \"magus\";\n")
	exe, err := os.Executable()
	s.Require().NoError(err)

	cmd := exec.Command(exe, "-test.run", "^TestBriefRefusalHelperProcess$")
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", briefRefusalRootVar, w.Root()))
	out, runErr := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	s.Require().ErrorAs(runErr, &exitErr, string(out))
	s.Equal(cache.ExitCodeBrokerUnavailable, exitErr.ExitCode(), string(out))
	s.Contains(string(out), "session-brief")
}

func TestUsagefCarriesTheMisuseMessage(t *testing.T) {
	err := exit.Usagef("magus diff: %d is too many", 3)
	assert.EqualError(t, err, "magus diff: 3 is too many")
	assert.Equal(t, exit.Usage, mapExitCode(err))
}

// reportedExit is what errors.As finds in an exit.Reported failure.
type reportedExit interface {
	AlreadyReported() bool
	ExitCode() int
}

// TestHintCanonicalSpellingTeachesCharmOnce covers the one place a run is told it spelled
// a charm non-canonically. Every other path resolves the spelling silently by design (a
// spell arm, RunCI and the cache key all see the canonical form), so if this line does not
// print, nothing tells anyone which spelling magus will report back.
//
// Once per invocation, not once per target: `magus run format:UPDATE a b c` parses the
// same suffix per project, and a hint that repeats per project is one people filter out.
func TestHintCanonicalSpellingTeachesCharmOnce(t *testing.T) {
	target, err := types.ParseTarget("format:UPDATE")
	require.NoError(t, err)

	ctx := interactive.WithHints(t.Context())
	got := notices(t, func() {
		hintCanonicalSpelling(ctx, target)
		hintCanonicalSpelling(ctx, target)
	})
	assert.Equal(t, 1, strings.Count(got, "hint:"), "the charm must teach once, not once per call: %q", got)
	assert.Contains(t, got, `charm "UPDATE" is canonically "update"`)
}

// TestRelaxGCConcurrentCallersShareOneRestore reproduces the race a plain save/restore
// pair had between loadMagus and inspectWorkspace: both can first-load in parallel
// goroutines, and each capturing its own "prior" GOGC lets one's restore stomp the
// other's still-active relaxation, or leave GOGC raised forever. Refcounting must keep
// GOGC raised for as long as any holder is out, and restore it exactly once, only when
// the last one releases, regardless of acquire order.
func TestRelaxGCConcurrentCallersShareOneRestore(t *testing.T) {
	if orig, ok := os.LookupEnv("GOGC"); ok {
		require.NoError(t, os.Unsetenv("GOGC"))
		t.Cleanup(func() { _ = os.Setenv("GOGC", orig) })
	}

	const before = 111 // distinctive, so a restore-to-the-wrong-value cannot pass by coincidence
	debug.SetGCPercent(before)
	t.Cleanup(func() { debug.SetGCPercent(before) })

	const holders = 8
	var wg sync.WaitGroup
	releases := make([]func(), holders)
	for i := range holders {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			releases[i] = relaxGC()
		}(i)
	}
	wg.Wait()

	// Peeking is itself a SetGCPercent call, so set back to what every holder expects
	// raised: a no-op if the fix holds, a real (and revealing) change if it does not.
	got := debug.SetGCPercent(loadGCPercent)
	assert.Equal(t, loadGCPercent, got, "GOGC must stay raised while any holder has not released")

	for _, release := range releases[:holders-1] {
		release()
	}
	got = debug.SetGCPercent(loadGCPercent)
	assert.Equal(t, loadGCPercent, got, "GOGC must stay raised until the LAST holder releases")

	releases[holders-1]()
	got = debug.SetGCPercent(before)
	assert.Equal(t, before, got, "GOGC must be restored to its pre-relax value once every holder released")
}

var gcPercentAtInit = func() int {
	percent := debug.SetGCPercent(100)
	debug.SetGCPercent(percent)
	return percent
}()

func TestGCPolicyInitRanBeforeMain(t *testing.T) {
	if _, raised := gcpolicy.Prior(); !raised {
		t.Skip("an explicit GOGC kept gcpolicy's init from raising it")
	}
	assert.Equal(t, gcpolicy.Percent, gcPercentAtInit)
}

func TestRelaxStartupGCRestoresAfterTheWindow(t *testing.T) {
	if orig, ok := os.LookupEnv("GOGC"); ok {
		require.NoError(t, os.Unsetenv("GOGC"))
		t.Cleanup(func() { _ = os.Setenv("GOGC", orig) })
	}
	initPrior, raised := gcpolicy.Prior()
	afterInit := gcpolicy.Percent
	if raised {
		afterInit = initPrior
	}
	cases := []struct {
		name        string
		before      int
		wantRestore int
	}{
		{name: "caller set GOGC", before: 111, wantRestore: 111},
		{name: "gcpolicy init raised GOGC", before: gcpolicy.Percent, wantRestore: afterInit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			debug.SetGCPercent(tc.before)
			t.Cleanup(func() { debug.SetGCPercent(tc.before) })

			// Held under gcRelaxMu so the timer's restore cannot land between the two calls.
			peek := func() (percent, holders int) {
				gcRelaxMu.Lock()
				defer gcRelaxMu.Unlock()
				percent = debug.SetGCPercent(tc.before)
				debug.SetGCPercent(percent)
				return percent, gcRelaxCount
			}

			relaxStartupGC(50 * time.Millisecond)
			percent, holders := peek()
			assert.Equal(t, loadGCPercent, percent)
			assert.Equal(t, 1, holders)

			require.Eventually(t, func() bool {
				_, holders := peek()
				return holders == 0
			}, 5*time.Second, 5*time.Millisecond)
			percent, _ = peek()
			assert.Equal(t, tc.wantRestore, percent)
		})
	}
	assert.Equal(t, time.Second, startupGCWindow)
}

func TestOpenedRootAcceptsEverySpellingOfItsWorkspace(t *testing.T) {
	t.Parallel()
	workspace := func() string {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), nil, 0o644))
		return dir
	}
	a, b := workspace(), workspace()
	require.NoError(t, os.Mkdir(filepath.Join(a, "sub"), 0o755))
	opened := openedRoot{override: "", root: a}

	for _, override := range []string{"", a, filepath.Join(a, "sub")} {
		require.NoError(t, opened.check("loadMagus", override), "%q names the loaded workspace", override)
	}
	assert.PanicsWithValue(t, fmt.Sprintf("loadMagus: asked for the workspace at %s after loading %s", b, a),
		func() { _ = opened.check("loadMagus", b) }, "a second workspace in one process is a bug")
	assert.Error(t, opened.check("loadMagus", filepath.Join(t.TempDir(), "gone")),
		"an override that no longer resolves is the caller's error, not a panic")
	require.NoError(t, openedRoot{override: ""}.check("loadMagus", b),
		"a load that failed to resolve keeps its own error")
}
