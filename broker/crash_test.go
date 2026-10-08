package broker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crasherEnv makes the test binary a watched client of the broker at the address it
// names; crashModeEnv says how it then ends.
const (
	crasherEnv   = "MAGUS_BROKER_TEST_CRASHER"
	crashModeEnv = "MAGUS_BROKER_TEST_CRASH_MODE"
)

const testHint = "HINT: this is a defect in magus\n"

func crashAfterWatch(addr string) int {
	if _, err := Dial(context.Background(), addr, WithCrashHint(testHint)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	switch os.Getenv(crashModeEnv) {
	case "panic":
		// Off the main goroutine, where a recover in main would never see it.
		go func() { panic("boom") }()
		select {}
	case "fatal":
		// A fatal runtime error: no deferred function runs, so only the runtime's own
		// crash output can report it.
		debug.SetMaxStack(1 << 16)
		var recurse func(int) int
		recurse = func(n int) int { return recurse(n+1) + 1 }
		recurse(0)
	}
	return 0
}

func skipWithoutFilePassing(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skipf("the broker passes no file descriptors on %s", runtime.GOOS)
	}
}

// runWatched runs a watched client to its end and returns its stderr. cmd.Run returns
// only once every holder of that stderr has closed it, the broker's copy included.
func runWatched(t *testing.T, addr, mode string) (string, time.Duration, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), crasherEnv+"="+addr, crashModeEnv+"="+mode)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	return stderr.String(), time.Since(start), err
}

func TestCrashHintFollowsTheTraceAndTheReportIsSaved(t *testing.T) {
	skipWithoutFilePassing(t)
	// The runtime prints a fatal error's first line to stderr before it starts copying to
	// the crash output, so the saved report of one begins at its stacks.
	for _, tc := range []struct{ mode, trace, saved string }{
		{"panic", "panic: boom", "panic: boom"},
		{"fatal", "fatal error: stack overflow", "runtime.throw"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			addr := testAddr(t)
			dir := t.TempDir()
			serve(t, addr, WithCrashDir(dir))

			out, _, err := runWatched(t, addr, tc.mode)
			require.Error(t, err, "the process still crashes: nothing recovers it")
			trace := strings.Index(out, tc.trace)
			hint := strings.Index(out, testHint)
			require.GreaterOrEqual(t, trace, 0, "Go's trace reaches stderr as always:\n%s", out)
			require.Greater(t, hint, trace, "the hint follows the trace:\n%s", out)
			assert.Contains(t, out, "The crash report is saved at "+dir)

			reports, err := filepath.Glob(filepath.Join(dir, "*.txt"))
			require.NoError(t, err)
			require.Len(t, reports, 1)
			saved, err := os.ReadFile(reports[0])
			require.NoError(t, err)
			assert.Contains(t, string(saved), tc.saved)
		})
	}
}

// A process that ends cleanly must not be held open by the broker's copy of its stderr:
// whatever reads that stderr waits for it to close.
func TestACleanExitLeavesNoHintAndClosesStderr(t *testing.T) {
	skipWithoutFilePassing(t)
	addr := testAddr(t)
	dir := t.TempDir()
	serve(t, addr, WithCrashDir(dir))

	out, took, err := runWatched(t, addr, "clean")
	require.NoError(t, err, out)
	assert.NotContains(t, out, testHint)
	assert.Less(t, took, 5*time.Second, "stderr closes when the process exits")
	reports, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	require.NoError(t, err)
	assert.Empty(t, reports)
}

// Serve returns while a watched process lives on: the watch must not hold it up.
func TestServeReturnsWithAWatchedProcessStillRunning(t *testing.T) {
	skipWithoutFilePassing(t)
	addr := testAddr(t)
	stop, _ := serve(t, addr)
	c, err := Dial(t.Context(), addr)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = w.Close() }()
	stderr, err := os.Open(os.DevNull)
	require.NoError(t, err)
	defer func() { _ = stderr.Close() }()
	cn, err := c.connect(t.Context())
	require.NoError(t, err)
	require.NoError(t, cn.roundTripFiles(t.Context(), c.next(), typeCrashWatch, crashWatchRequest{Hint: testHint},
		[]*os.File{r, stderr}, typeCrashReply, nil, nil))
	_ = r.Close()

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve waited on a watch whose process is still running")
	}
}
