package bindings

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordRunner is a service.Runner that records starts without forking a process.
type recordRunner struct{ started int }

func (r *recordRunner) Start(context.Context, spells.Service) (service.Handle, error) {
	r.started++
	return struct{}{}, nil
}
func (r *recordRunner) Stop(context.Context, service.Handle) {}

func serviceOp() spells.Op {
	// bin "true" exits 0, so the non-supervised fall-through fork is harmless.
	return spells.Op{
		Kind:    spells.OpKindService,
		Command: spells.Command{Bin: "true"},
		Service: &spells.Service{Command: spells.Command{Bin: "true"}},
	}
}

// TestRunCommandResolvesCwdAgainstContext proves a command op with no explicit cwd
// runs in the context working directory (the project dir the magusfile runner sets via
// std.WithCwd), not the process cwd. This is what lets a spell op invoked from a
// subproject target - go["go-run"] from docs/ - resolve its relative paths correctly.
func TestRunCommandResolvesCwdAgainstContext(t *testing.T) {
	dir := t.TempDir()
	ctx := std.WithCwd(context.Background(), dir)

	op := spells.Op{Command: spells.Command{Bin: "sh", Args: []string{"-c", "echo hi > marker.txt"}}}
	_, err := runCommand(ctx, op, commandOpts{})
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, "marker.txt"),
		"an op with no explicit cwd must run in the context cwd, not the process cwd")
}

// TestRunCommandSupervisesServiceDependency proves runCommand routes a service op to
// the supervisor (not a foreground fork) when supervision is active - the case of a
// service reached via magus.needs.
func TestRunCommandSupervisesServiceDependency(t *testing.T) {
	rr := &recordRunner{}
	sess := service.NewSession(service.New(rr, 0), nil, nil)
	ctx := service.WithSupervision(service.WithSession(context.Background(), sess))

	_, err := runCommand(ctx, serviceOp(), commandOpts{})
	require.NoError(t, err)
	assert.Equal(t, 1, rr.started, "service dependency should be supervised, not forked")
}

// TestRunCommandForegroundsDirectService proves a service op with no active
// supervision falls through to a real fork (the directly-run case), and is not
// handed to the supervisor.
func TestRunCommandForegroundsDirectService(t *testing.T) {
	rr := &recordRunner{}
	sess := service.NewSession(service.New(rr, 0), nil, nil)
	// Session present but supervision NOT active: this is a directly-run service.
	ctx := service.WithSession(context.Background(), sess)

	_, err := runCommand(ctx, serviceOp(), commandOpts{})
	require.NoError(t, err) // `true` exits 0
	assert.Equal(t, 0, rr.started, "directly-run service must foreground, not be supervised")
}

// TestExecCommandReportsCancellationNotExitCode proves a child killed because the
// RUN was cancelled reports the cancellation, not a verdict on the tool. A killed
// process has no exit code of its own - ExitCode() is -1 - so synthesizing "exited
// 1" for it is indistinguishable from the tool genuinely failing, and is printed
// with a `reproduce:` line that does not reproduce. That is the shape that made a
// sibling project's failure surface as `docs content-generate: go exited 1` with no
// stderr, in a batch where docs alone passes.
func TestExecCommandReportsCancellationNotExitCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)

	_, err := execCommand(ctx, t.TempDir(), "sh", []string{"-c", "sleep 30"}, nil, "", true)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled,
		"a child killed by the run's cancellation must report the cancellation")
	assert.NotContains(t, err.Error(), "exited",
		"a killed child has no exit code of its own to report")
}

// TestExecCommandReportsRealExitCode is the other half: a tool that fails on its own
// still reports its own exit code, so the cancellation check above cannot swallow a
// genuine failure.
func TestExecCommandReportsRealExitCode(t *testing.T) {
	_, err := execCommand(context.Background(), t.TempDir(), "sh", []string{"-c", "exit 3"}, nil, "", true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sh exited 3")
}

// TestExecCommandReportsNotStartedErrorNotExitCode proves a process that never
// started (binary missing from PATH) reports run.Exec's classified error, not a
// fabricated "exited 1". A process with no PID has no exit code of its own, and
// synthesizing one discards the MGS3003 tool-not-on-PATH diagnostic that explains
// WHY it failed - the same shape TestExecCommandReportsCancellationNotExitCode
// pins for the cancellation case.
func TestExecCommandReportsNotStartedErrorNotExitCode(t *testing.T) {
	_, err := execCommand(context.Background(), t.TempDir(), "magus-does-not-exist-on-path", nil, nil, "", true)

	require.Error(t, err)
	assert.ErrorIs(t, err, exec.ErrNotFound,
		"a start failure must propagate run.Exec's classified error, not a fabricated exit code")
	assert.NotContains(t, err.Error(), "exited",
		"a process that never started has no exit code of its own to report")
}

// TestAdviceFor pins the failure-classification rule a command op declares. Declaration
// order is the precedence, so a spell author reads the outcome off the file.
func TestAdviceFor(t *testing.T) {
	hints := []spells.Hint{
		{Contains: "denied: requested access", Advise: "specific"},
		{Contains: "denied", Advise: "general"},
	}
	t.Run("first declared match wins", func(t *testing.T) {
		assert.Equal(t, "specific", adviceFor(hints, "error: denied: requested access to the resource"),
			"declaration order is the precedence, not match length")
	})
	t.Run("falls through to a later rule", func(t *testing.T) {
		assert.Equal(t, "general", adviceFor(hints, "push denied by policy"))
	})
	t.Run("no match is silent", func(t *testing.T) {
		assert.Empty(t, adviceFor(hints, "compilation failed"), "an unrecognized failure must not invent advice")
	})
	t.Run("an empty Contains never fires", func(t *testing.T) {
		// strings.Contains(x, "") is TRUE, so an unguarded empty rule would advise on
		// every failure of the command. decode rejects it; this covers a value built in Go.
		assert.Empty(t, adviceFor([]spells.Hint{{Advise: "no contains set"}, {Contains: "x"}}, "anything x"))
	})
	// The bug this signature exists to prevent: stdout ending mid-word and stderr
	// beginning mid-word must not be joined into a match that appeared in neither.
	t.Run("a match cannot straddle two streams", func(t *testing.T) {
		h := []spells.Hint{{Contains: "unauthorized", Advise: "should not fire"}}
		assert.Empty(t, adviceFor(h, "...ends with unauth", "orized: denied..."),
			"sources are matched independently, never concatenated")
	})
	t.Run("either stream alone can match", func(t *testing.T) {
		h := []spells.Hint{{Contains: "unauthorized", Advise: "fires"}}
		assert.Equal(t, "fires", adviceFor(h, "unauthorized", ""), "stdout")
		assert.Equal(t, "fires", adviceFor(h, "", "unauthorized"), "stderr")
	})
}

// TestOutputTailKeepsTheEnd covers the bounded buffer advice is matched against. The tail is
// what matters: a tool's explanation of why it failed is the last thing it prints.
func TestOutputTailKeepsTheEnd(t *testing.T) {
	tail := newOutputTail(8)
	n, err := tail.Write([]byte("0123456789"))
	require.NoError(t, err)
	assert.Equal(t, 10, n, "must report the full write so a MultiWriter does not short-circuit the real stream")
	assert.Equal(t, "23456789", tail.String(), "keeps the LAST limit bytes")

	tail.Write([]byte("abc"))
	assert.Equal(t, "56789abc", tail.String(), "still bounded after a second write")

	small := newOutputTail(64)
	small.Write([]byte("short"))
	assert.Equal(t, "short", small.String(), "under the limit is kept whole")
}

// TestNewOutputTailFloorsLimit: a zero limit would make Write discard everything, so advice
// would silently never fire.
func TestNewOutputTailFloorsLimit(t *testing.T) {
	tail := newOutputTail(0)
	tail.Write([]byte("kept"))
	assert.Equal(t, "kept", tail.String())
}

// hintOp is a command that always fails with text a declared hint matches.
func hintOp() spells.Op {
	return spells.Op{Command: spells.Command{
		Bin:   "sh",
		Args:  []string{"-c", "echo 'denied: authentication required' >&2; exit 1"},
		Hints: []spells.Hint{{Contains: "authentication required", Advise: "run docker login"}},
	}}
}

// TestRunCommandAdvisesOnRealFailure covers the WIRING - the tee, the fire condition, and
// the sink - rather than adviceFor in isolation. Every defect the review found lived in
// these lines and none of them were reachable from a unit test of the matcher.
func TestRunCommandAdvisesOnRealFailure(t *testing.T) {
	run6 := func(ctx context.Context, op spells.Op) string {
		var stderr bytes.Buffer
		ctx = std.WithCwd(ctx, t.TempDir())
		// The advice must ride the run's OWN writers: that is what puts it in the
		// persisted log and the output ref rather than only on a terminal.
		ctx = run.WithOutputWriters(ctx, io.Discard, &stderr)
		_, _ = runCommand(ctx, op, commandOpts{})
		return stderr.String()
	}

	t.Run("a real non-zero exit advises through the run writers", func(t *testing.T) {
		got := run6(context.Background(), hintOp())
		assert.Contains(t, got, "run docker login", "advice must reach the run's stderr writer")
		assert.Contains(t, got, "authentication required", "the tool's own error still streams")
	})

	t.Run("a cancelled run advises nothing", func(t *testing.T) {
		// The critical case: run.Exec joins ctx.Err() into its error, so gating on err
		// alone told a user who pressed Ctrl-C to go and authenticate.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assert.NotContains(t, run6(ctx, hintOp()), "run docker login",
			"a cancelled run must not be diagnosed as an authentication failure")
	})

	t.Run("a success advises nothing", func(t *testing.T) {
		ok := spells.Op{Command: spells.Command{
			Bin:   "sh",
			Args:  []string{"-c", "echo 'authentication required'"},
			Hints: hintOp().Hints,
		}}
		assert.NotContains(t, run6(context.Background(), ok), "run docker login",
			"matching text on a PASSING command must not advise")
	})

	t.Run("an op declaring no hints installs no tee", func(t *testing.T) {
		bare := spells.Op{Command: spells.Command{Bin: "sh", Args: []string{"-c", "echo hi >&2; exit 1"}}}
		assert.NotContains(t, run6(context.Background(), bare), "hint:")
	})
}

// logInvocationOp is a Command that appends one line per invocation to a log
// file - every arg of that invocation, space-separated - so a test can tell
// how many times it ran and with what argv, without a real tool on PATH.
func logInvocationOp(sources []string, each bool) spells.Op {
	return spells.Op{Command: spells.Command{
		Bin:         "sh",
		Args:        []string{"-c", `echo "$@" >> "$LOGFILE"`, "sh"},
		Sources:     sources,
		SourcesEach: each,
	}}
}

// writeSourceFixture lays out a small tree under dir: two source files at the
// root, one nested under a subdirectory, and one under thirdparty/ - a name
// NOT in the core project.IgnoreDirs default, so pruning it proves the
// exclusion came from a declared ignore dir (commandOpts.ignoreDirs), not the
// walk's always-on defaults. Returns the sorted list of the three files an
// unfiltered **/*.txt glob is expected to match, relative to dir.
func writeSourceFixture(t *testing.T, dir string) []string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "thirdparty"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "c.txt"), []byte("c"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "thirdparty", "skip.txt"), []byte("s"), 0o644))
	return []string{"a.txt", "b.txt", filepath.Join("sub", "c.txt")}
}

// logLines reads path's lines, or nil if it does not exist - the empty-match
// case must never create it.
func logLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	s := strings.TrimRight(string(data), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// TestRunCommandSourcesBatchesAllMatches proves the default (SourcesEach
// unset) mode invokes Bin ONCE with every matched file appended to argv - the
// `xargs` (no -n1) shape shellcheck relies on.
func TestRunCommandSourcesBatchesAllMatches(t *testing.T) {
	dir := t.TempDir()
	files := writeSourceFixture(t, dir)
	ctx := std.WithCwd(context.Background(), dir)
	logFile := filepath.Join(dir, "calls.log")

	op := logInvocationOp([]string{"**/*.txt"}, false)
	_, err := runCommand(ctx, op, commandOpts{env: map[string]string{"LOGFILE": logFile}, ignoreDirs: []string{"thirdparty"}})
	require.NoError(t, err)

	lines := logLines(t, logFile)
	require.Len(t, lines, 1, "batched mode must run Bin exactly once")
	assert.Equal(t, strings.Join(files, " "), lines[0], "every matched file must be on the one invocation, in sorted order")
}

// TestRunCommandSourcesChunksPastTheBatchLimit proves batched mode SPLITS once the
// match set exceeds sourcesBatchLimit, rather than handing the OS one oversized argv.
// The other batch test uses a small fixture, so it only ever exercised the
// single-invocation path: the chunking loop this asserts had no coverage at all, and a
// regression to "one invocation, every file" would have passed the whole suite while
// blowing ARG_MAX on any real repo.
func TestRunCommandSourcesChunksPastTheBatchLimit(t *testing.T) {
	dir := t.TempDir()
	// One and a half batches: enough to prove it splits AND that the tail is a short
	// batch rather than being dropped or padded.
	const n = sourcesBatchLimit + sourcesBatchLimit/2
	for i := range n {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), nil, 0o644))
	}
	ctx := std.WithCwd(context.Background(), dir)
	logFile := filepath.Join(dir, "calls.log")

	op := logInvocationOp([]string{"*.txt"}, false)
	_, err := runCommand(ctx, op, commandOpts{env: map[string]string{"LOGFILE": logFile}})
	require.NoError(t, err)

	lines := logLines(t, logFile)
	require.Len(t, lines, 2, "%d files must split into two batches at a limit of %d", n, sourcesBatchLimit)
	assert.Len(t, strings.Fields(lines[0]), sourcesBatchLimit, "first batch is full")
	assert.Len(t, strings.Fields(lines[1]), n-sourcesBatchLimit, "second batch carries the remainder")

	// No file may be lost or repeated across the split.
	var got []string
	for _, l := range lines {
		got = append(got, strings.Fields(l)...)
	}
	assert.Len(t, got, n, "every matched file reaches exactly one invocation")
}

// TestRunCommandSourcesEachInvokesPerFile proves SourcesEach = true invokes
// Bin once per matched file (the `xargs -n1` shape buzz --check/--test/magus
// buzz rely on).
func TestRunCommandSourcesEachInvokesPerFile(t *testing.T) {
	dir := t.TempDir()
	files := writeSourceFixture(t, dir)
	ctx := std.WithCwd(context.Background(), dir)
	logFile := filepath.Join(dir, "calls.log")

	op := logInvocationOp([]string{"**/*.txt"}, true)
	_, err := runCommand(ctx, op, commandOpts{env: map[string]string{"LOGFILE": logFile}, ignoreDirs: []string{"thirdparty"}})
	require.NoError(t, err)

	lines := logLines(t, logFile)
	require.Len(t, lines, len(files), "each mode must run Bin once per matched file")
	assert.Equal(t, files, lines, "one file per invocation, in sorted order")
}

// TestRunCommandSourcesEmptyMatchIsNoop proves a Sources glob matching nothing
// runs Bin ZERO times and reports success - the engine-side `xargs -r` - rather
// than invoking it with no files or failing.
func TestRunCommandSourcesEmptyMatchIsNoop(t *testing.T) {
	dir := t.TempDir()
	writeSourceFixture(t, dir)
	ctx := std.WithCwd(context.Background(), dir)
	logFile := filepath.Join(dir, "calls.log")

	op := logInvocationOp([]string{"**/*.doesnotexist"}, false)
	res, err := runCommand(ctx, op, commandOpts{env: map[string]string{"LOGFILE": logFile}})
	require.NoError(t, err)
	assert.Equal(t, run.ExecResult{}, res, "an empty match must report the same zero result as a skipped step")
	assert.NoFileExists(t, logFile, "Bin must never have been invoked")
}

// TestRunCommandSourcesHonorsIgnoreDirs proves a declared ignore dir (threaded
// through commandOpts.ignoreDirs, as dispatchOp threads a spell's own
// mgs_listIgnoreDirs) is pruned from the Sources walk WITHOUT the op's own
// glob naming it - the mechanism spells/bash/spell.buzz's shellcheck op relies
// on for "worktrees"/"node_modules" instead of hardcoding a find prune.
func TestRunCommandSourcesHonorsIgnoreDirs(t *testing.T) {
	dir := t.TempDir()
	writeSourceFixture(t, dir)
	ctx := std.WithCwd(context.Background(), dir)
	logFile := filepath.Join(dir, "calls.log")

	op := logInvocationOp([]string{"**/*.txt"}, false)
	_, err := runCommand(ctx, op, commandOpts{env: map[string]string{"LOGFILE": logFile}, ignoreDirs: []string{"thirdparty"}})
	require.NoError(t, err)

	lines := logLines(t, logFile)
	require.Len(t, lines, 1)
	assert.NotContains(t, lines[0], "skip.txt", "a file under a declared ignore dir must never reach argv")

	// Without the declared ignore dir, the same file DOES survive the walk -
	// proving the exclusion above came from opts.ignoreDirs, not the glob.
	require.NoError(t, os.Remove(logFile))
	_, err = runCommand(ctx, op, commandOpts{env: map[string]string{"LOGFILE": logFile}})
	require.NoError(t, err)
	lines = logLines(t, logFile)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "skip.txt", "without the declared ignore dir the file is walked")
}

// TestResolveRunnerRefs pins the $NAME token rule directly: a token shaped
// like a reference resolves against refs or errors naming the op and the
// reference; anything else - a normal arg, a $ that is not a whole bare
// identifier - passes through untouched.
func TestResolveRunnerRefs(t *testing.T) {
	refs := map[string]string{"MAGUS": "/path/to/magus", "MAGUS_LEVEL": "1"}

	t.Run("a known reference resolves in bin and args", func(t *testing.T) {
		bin, args, err := resolveRunnerRefs("magus-buzz", "$MAGUS", []string{"buzz", "$MAGUS_LEVEL"}, refs)
		require.NoError(t, err)
		assert.Equal(t, "/path/to/magus", bin)
		assert.Equal(t, []string{"buzz", "1"}, args)
	})
	t.Run("an unknown reference is a hard error naming the op and the token", func(t *testing.T) {
		_, _, err := resolveRunnerRefs("magus-buzz", "shellcheck", []string{"$NOT_A_REAL_REF"}, refs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"magus-buzz"`)
		assert.Contains(t, err.Error(), "$NOT_A_REAL_REF")
	})
	t.Run("a token embedding a reference is left alone, not interpolated", func(t *testing.T) {
		bin, args, err := resolveRunnerRefs("", "shellcheck", []string{"--output=$MAGUS", "${MAGUS}"}, refs)
		require.NoError(t, err)
		assert.Equal(t, "shellcheck", bin)
		assert.Equal(t, []string{"--output=$MAGUS", "${MAGUS}"}, args, "only a WHOLE bare $NAME token is a reference")
	})
	t.Run("an ordinary arg is untouched", func(t *testing.T) {
		_, args, err := resolveRunnerRefs("", "shellcheck", []string{"-c", "**/*.sh"}, refs)
		require.NoError(t, err)
		assert.Equal(t, []string{"-c", "**/*.sh"}, args)
	})
}

// TestRunCommandResolvesMagusRef is the integration proof that runCommand
// itself feeds run.SelfVars into resolveRunnerRefs: an op referencing
// $MAGUS_LEVEL in Args (the same shape magus-buzz uses for $MAGUS) receives the
// runner's own value, with no shell involved.
func TestRunCommandResolvesMagusRef(t *testing.T) {
	dir := t.TempDir()
	ctx := std.WithCwd(context.Background(), dir)

	op := spells.Op{Command: spells.Command{
		Bin:  "sh",
		Args: []string{"-c", `printf '%s' "$1" > level.txt`, "sh", "$MAGUS_LEVEL"},
	}}
	_, err := runCommand(ctx, op, commandOpts{})
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "level.txt"))
	require.NoError(t, err)
	// Not hardcoded to "1": this test process's own MAGUS_LEVEL is whatever
	// invoked it (0 when run directly, higher under a nested magus run - see
	// run.CurrentLevel), and the child is always one past that.
	want := strconv.Itoa(run.CurrentLevel() + 1)
	assert.Equal(t, want, string(got), "MAGUS_LEVEL is this process's level plus one for the child")
}

// TestRunCommandUnresolvedRefFailsBeforeExec proves an op referencing a $NAME
// the runner does not provide fails with a diagnosis naming the reference,
// rather than either exec'ing a literal "$NOT_A_REAL_REF" or silently dropping
// it as an empty argument.
func TestRunCommandUnresolvedRefFailsBeforeExec(t *testing.T) {
	ctx := std.WithCwd(context.Background(), t.TempDir())
	op := spells.Op{Command: spells.Command{Bin: "sh", Args: []string{"-c", "echo $1", "sh", "$NOT_A_REAL_REF"}}}

	_, err := runCommand(ctx, op, commandOpts{op: "example"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"example"`)
	assert.Contains(t, err.Error(), "$NOT_A_REAL_REF")
}
