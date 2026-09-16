package sessions

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunAdaptersReportsEachOneWhole(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]AdapterResult{
			{Host: "first", Output: "loaded 3"},
			{Host: "second", Output: "loaded 4"},
		},
		RunAdapters(t.Context(), t.TempDir(), []Adapter{
			{Host: "first", Argv: []string{"echo", "loaded 3"}},
			{Host: "second", Argv: []string{"echo", "loaded 4"}},
		}),
		"order follows declaration, and a clean run carries no error")
}

func TestRunAdaptersRunsFromTheWorkspaceRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	results := RunAdapters(t.Context(), root, []Adapter{{Host: "h", Argv: []string{"pwd"}}})

	// Not a whole-struct compare: macOS symlinks /var to /private/var, so the value under
	// test is "did it run in root", which the base name answers and the full path does not.
	require.Len(t, results, 1)
	assert.Equal(t, AdapterResult{Host: "h", Output: results[0].Output}, results[0])
	assert.Equal(t, filepath.Base(root), filepath.Base(results[0].Output))
}

// TestRunAdaptersReportsAFailureRatherThanReturningAnError pins the contract the caller
// depends on: a graph build carries on around a broken adapter.
func TestRunAdaptersReportsAFailureRatherThanReturningAnError(t *testing.T) {
	t.Parallel()

	results := RunAdapters(t.Context(), t.TempDir(), []Adapter{
		{Host: "broken", Argv: []string{"sh", "-c", "echo nope >&2; exit 3"}},
		{Host: "fine", Argv: []string{"echo", "ok"}},
	})

	require.Len(t, results, 2, "a failing adapter must not stop the ones after it")
	require.Error(t, results[0].Err)
	assert.Equal(t,
		[]AdapterResult{
			{Host: "broken", Err: results[0].Err, Output: "nope"},
			{Host: "fine", Output: "ok"},
		},
		results,
		"stderr is kept: it is where an adapter says why it could not run")
}

func TestRunAdaptersRunsNothingWithoutSomethingToRun(t *testing.T) {
	t.Parallel()

	marker := filepath.Join(t.TempDir(), "ran")
	touch := Adapter{Host: "h", Argv: []string{"touch", marker}}

	assert.Nil(t, RunAdapters(t.Context(), t.TempDir(), nil),
		"a workspace that declared no adapter has nothing to report")
	assert.Nil(t, RunAdapters(t.Context(), "", []Adapter{touch}),
		"with no workspace root there is no directory to run an adapter in")
	assert.Empty(t, RunAdapters(t.Context(), t.TempDir(), []Adapter{{Host: "blank", Argv: []string{"  "}}}),
		"a blank program is a half-written config entry, not a command")

	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "no adapter should have run in any of those cases")
}

// TestRunAdaptersStopsWithTheContext pins that a caller's cancellation reaches the
// adapter. A graph build interrupted at the keyboard must not leave a transcript scan
// running.
func TestRunAdaptersStopsWithTheContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	assert.Empty(t, RunAdapters(ctx, t.TempDir(), []Adapter{{Host: "slow", Argv: []string{"sleep", "30"}}}),
		"a cancelled run does not start adapters it has not reached")
}

// TestAnAdapterIsNotAShellLine pins what the argv shape buys. A declared value that would
// be a pipeline under a shell is one program's arguments here, so a committed config
// cannot become a second command on a machine that merely pulled the branch.
func TestAnAdapterIsNotAShellLine(t *testing.T) {
	t.Parallel()

	results := RunAdapters(t.Context(), t.TempDir(), []Adapter{
		{Host: "literal", Argv: []string{"echo", "a; touch /tmp/pwn | sh $(whoami)"}},
	})

	assert.Equal(t,
		[]AdapterResult{{Host: "literal", Output: "a; touch /tmp/pwn | sh $(whoami)"}},
		results,
		"the metacharacters are arguments to echo, never operators")
}

// TestARunawayAdapterDoesNotHangTheCaller is the one that matters for the daemon.
//
// Writing output into a buffer makes os/exec allocate a pipe, and Wait blocks until every
// holder of the write end closes it, while the deadline signals only the direct child. So
// an adapter that leaves a background process holding that pipe hung the caller forever,
// and in the daemon that wedged a run slot, which the maintenance scheduler reads as busy
// for the rest of the process's life.
func TestARunawayAdapterDoesNotHangTheCaller(t *testing.T) {
	t.Parallel()

	// The child exits at once; the grandchild keeps the inherited pipe open well past any
	// deadline this test would wait for.
	started := time.Now()
	results := RunAdapters(t.Context(), t.TempDir(), []Adapter{
		{Host: "runaway", Argv: []string{"sh", "-c", "sleep 300 & echo started"}},
	})

	assert.Less(t, time.Since(started), adapterTimeout,
		"the caller returns on WaitDelay, not on the grandchild closing the pipe")
	require.Len(t, results, 1)
	// REPORTED as a failure, deliberately. Abandoning the pipe is what unblocks the
	// caller, and an adapter that left a process holding it did not finish cleanly, so
	// saying it succeeded would hide the thing worth fixing in the adapter.
	require.ErrorIs(t, results[0].Err, exec.ErrWaitDelay)
	assert.Equal(t,
		[]AdapterResult{{Host: "runaway", Err: results[0].Err, Output: "started"}},
		results,
		"what the adapter managed to say before it was abandoned is still kept")
}

// TestAdapterOutputIsCapped pins that a chatty adapter cannot cost the caller its memory
// or the daemon its log. The TAIL survives, because an adapter's summary is its last line.
func TestAdapterOutputIsCapped(t *testing.T) {
	t.Parallel()

	results := RunAdapters(t.Context(), t.TempDir(), []Adapter{
		{Host: "chatty", Argv: []string{"awk", `BEGIN{for(i=0;i<20000;i++) print "noise noise noise"; print "loaded 7"}`}},
	})

	require.Len(t, results, 1)
	require.NoError(t, results[0].Err)
	assert.Less(t, len(results[0].Output), adapterOutputLimit+128)
	assert.True(t, strings.HasSuffix(results[0].Output, "loaded 7"),
		"the summary line is the part worth keeping")
	assert.True(t, strings.HasPrefix(results[0].Output, "[earlier output dropped]"),
		"and the reader is told the head is gone rather than left to trust a partial log")
}
