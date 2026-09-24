//go:build !windows

// cross-cutting: real magus processes signalled from outside, across broker.go, server.go and detach.go through signals.go

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// lifecycleBound is how long a test waits for a child to reach a state it signals. It
// bounds a failure; nothing waits it out on success.
const lifecycleBound = 20 * time.Second

// magusBin is the test binary under the name `magus`, so testscript.Main runs the real
// CLI in it. A hard link rather than a symlink: on Linux os.Executable resolves a symlink,
// and a magus that re-execs itself (a detached run, a broker it starts) would start the
// test binary under its own name and run the tests instead.
func magusBin(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	bin := filepath.Join(t.TempDir(), "magus")
	if err := os.Link(self, bin); err == nil {
		return bin
	}
	src, err := os.Open(self)
	require.NoError(t, err)
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY, 0o755)
	require.NoError(t, err)
	_, err = io.Copy(dst, src)
	require.NoError(t, err)
	require.NoError(t, dst.Close())
	return bin
}

// child is a real magus process a test signals.
type child struct {
	cmd    *exec.Cmd
	exited chan error
}

// startChild runs the magus CLI with args in its own process, stderr into a file the
// test can read while it runs.
func startChild(t *testing.T, stderrPath string, args ...string) *child {
	t.Helper()
	cmd := exec.Command(magusBin(t), args...)
	cmd.Dir = t.TempDir()
	if stderrPath != "" {
		f, err := os.Create(stderrPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = f.Close() })
		cmd.Stderr = f
	}
	require.NoError(t, cmd.Start())
	c := &child{cmd: cmd, exited: make(chan error, 1)}
	go func() { c.exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-c.exited
	})
	return c
}

func (c *child) signal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	require.NoError(t, c.cmd.Process.Signal(sig))
}

// awaitExit returns the child's exit, failing the test if it keeps running.
func (c *child) awaitExit(t *testing.T, why string) error {
	t.Helper()
	select {
	case err := <-c.exited:
		c.exited <- err // for the cleanup's receive
		return err
	case <-time.After(lifecycleBound):
		t.Fatal(why)
		return nil
	}
}

func (c *child) running() bool {
	select {
	case err := <-c.exited:
		c.exited <- err
		return false
	default:
		return true
	}
}

func fileContains(path, want string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), want)
}

// brokerEnv isolates a real broker: its socket in a private runtime dir, its log and the
// services journal under a private state dir.
func brokerEnv(t *testing.T) (stateDir string) {
	t.Helper()
	privateSockDir(t)
	stateDir = t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("MAGUS_BROKER", "")
	t.Setenv("MAGUS_SHUTDOWN_GRACE", "")
	return stateDir
}

// startBroker runs `magus broker` with args and waits until it answers.
func startBroker(t *testing.T, stderrPath string, args ...string) *child {
	t.Helper()
	c := startChild(t, stderrPath, append([]string{"broker"}, args...)...)
	addr := broker.DefaultAddr()
	require.Eventually(t, func() bool { return broker.Live(t.Context(), addr) || !c.running() },
		lifecycleBound, 10*time.Millisecond, "the broker never bound its socket")
	require.True(t, c.running(), "the broker exited before it served")
	st, err := broker.QueryStatus(t.Context(), addr)
	require.NoError(t, err)
	require.Equal(t, c.cmd.Process.Pid, st.PID, "the broker answering is the one under test")
	return c
}

// holdClaim takes a claim on the broker over its own connection, which holds it until
// the returned client closes.
func holdClaim(t *testing.T) *broker.Client {
	t.Helper()
	c, err := broker.Dial(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	v, err := c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test", Slots: 1})
	require.NoError(t, err)
	require.True(t, v.Granted)
	return c
}

func awaitBrokerDraining(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		st, err := broker.QueryStatus(t.Context(), broker.DefaultAddr())
		return err == nil && st.Draining
	}, lifecycleBound, 10*time.Millisecond, "the broker never started draining")
}

// TestBrokerHangupReopensItsLog is what a log rotator relies on: move the file aside,
// send SIGHUP, and the broker writes a new one instead of exiting or writing on into the
// file that was moved.
func TestBrokerHangupReopensItsLog(t *testing.T) {
	stateDir := brokerEnv(t)
	logPath := filepath.Join(stateDir, "magus", "broker.log")
	b := startBroker(t, "", "--log", logPath)
	require.Eventually(t, func() bool { return fileContains(logPath, "serving") },
		lifecycleBound, 10*time.Millisecond, "the broker's banner never reached --log")

	rotated := logPath + ".1"
	require.NoError(t, os.Rename(logPath, rotated))
	b.signal(t, syscall.SIGHUP)

	require.Eventually(t, func() bool { return fileContains(logPath, "reopened its log") },
		lifecycleBound, 10*time.Millisecond, "SIGHUP did not start a new log at --log")
	assert.False(t, fileContains(rotated, "reopened"), "nothing after the hangup lands in the rotated file")
	assert.True(t, b.running(), "SIGHUP does not stop the broker")
	st, err := broker.QueryStatus(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	assert.Equal(t, b.cmd.Process.Pid, st.PID)
}

// TestBrokerTermDrainsUntilItsHoldersFinish is the first SIGTERM: the broker turns new
// claims away with the reason, keeps serving the run holding it, and exits cleanly the
// moment that run lets go.
func TestBrokerTermDrainsUntilItsHoldersFinish(t *testing.T) {
	brokerEnv(t)
	b := startBroker(t, "")
	holder := holdClaim(t)

	b.signal(t, syscall.SIGTERM)
	awaitBrokerDraining(t)

	late, err := broker.Dial(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = late.Close() })
	_, err = late.Request(t.Context(), types.MachineClaim{Project: "api", Target: "build", Slots: 1})
	var be *broker.Error
	require.ErrorAs(t, err, &be)
	assert.Equal(t, broker.CodeDraining, be.Code)
	assert.Contains(t, be.Message, fmt.Sprintf("the broker (pid %d) is shutting down", b.cmd.Process.Pid))
	assert.True(t, b.running(), "a draining broker waits for the run holding it")

	require.NoError(t, holder.Close())
	assert.NoError(t, b.awaitExit(t, "the broker kept running after its last holder let go"),
		"a completed drain is a clean exit")
	assert.False(t, broker.Live(t.Context(), broker.DefaultAddr()))
}

// TestBrokerSecondTermStopsNow: a supervisor that will not wait sends another SIGTERM,
// and the broker exits with the claim still held.
func TestBrokerSecondTermStopsNow(t *testing.T) {
	brokerEnv(t)
	b := startBroker(t, "")
	holdClaim(t)

	b.signal(t, syscall.SIGTERM)
	awaitBrokerDraining(t)
	b.signal(t, syscall.SIGTERM)
	assert.NoError(t, b.awaitExit(t, "a second SIGTERM did not stop a draining broker"))
}

func TestBrokerInterruptStopsNow(t *testing.T) {
	brokerEnv(t)
	b := startBroker(t, "")
	holdClaim(t)

	b.signal(t, syscall.SIGINT)
	assert.NoError(t, b.awaitExit(t, "SIGINT did not stop a broker holding a claim"))
}

// TestServerHangupReloadsAndTermStops runs a real foreground server: SIGHUP reloads it
// the way `magus server reload` does and leaves it serving, and SIGTERM stops it cleanly.
func TestServerHangupReloadsAndTermStops(t *testing.T) {
	stateDir := brokerEnv(t)
	t.Setenv("MAGUS_BROKER", "off")
	t.Setenv("MAGUS_MCP_ENABLED", "false")
	t.Setenv("MAGUS_SERVER_ADDRESS", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stderr := filepath.Join(stateDir, "server.stderr")
	s := startChild(t, stderr, "server", "--foreground")
	addr := proc.ServerDefaultAddr()
	require.Eventually(t, func() bool { return proc.SocketLive(t.Context(), addr) || !s.running() },
		lifecycleBound, 10*time.Millisecond, "the server never bound its socket")
	if !s.running() {
		logged, _ := os.ReadFile(stderr)
		t.Fatalf("the server exited before it served:\n%s", logged)
	}

	s.signal(t, syscall.SIGHUP)
	require.Eventually(t, func() bool { return fileContains(stderr, "reloaded configuration on SIGHUP") },
		lifecycleBound, 10*time.Millisecond, "SIGHUP did not reload the server")
	assert.True(t, s.running(), "SIGHUP does not stop the server")
	assert.True(t, proc.SocketLive(t.Context(), addr), "and it keeps serving")

	s.signal(t, syscall.SIGTERM)
	assert.NoError(t, s.awaitExit(t, "SIGTERM did not stop the server"), "a server stopped by SIGTERM exits 0")
	assert.False(t, proc.SocketLive(t.Context(), addr))
}

// TestDetachRunsAsItsOwnSessionWithABrokerAndNoServer runs `magus run --detach` for real.
// The detached run must be its own session leader, write to the log it named, hold its
// claim on the broker like any run, and neither need nor start a server. Its step blocks
// on a FIFO, so the test decides when it finishes rather than racing it.
func TestDetachRunsAsItsOwnSessionWithABrokerAndNoServer(t *testing.T) {
	stateDir := brokerEnv(t)
	t.Setenv("MAGUS_BROKER", "best-effort")
	t.Setenv("MAGUS_SERVER_ADDRESS", "")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Cleanup(func() {
		if c, err := broker.Dial(t.Context(), broker.DefaultAddr()); err == nil {
			_ = c.Shutdown(t.Context())
			_ = c.Close()
		}
	})

	ws := t.TempDir()
	gate := filepath.Join(t.TempDir(), "gate")
	require.NoError(t, unix.Mkfifo(gate, 0o600))
	done := filepath.Join(t.TempDir(), "done")
	require.NoError(t, os.WriteFile(filepath.Join(ws, "magusfile.buzz"), fmt.Appendf(nil, `import "magus";
import "proc";

export fun block(ctx: magus\Context, args: [str]) > void !> any {
    proc\exec("sh", ["-c", "cat %s > %s"]);
}
`, gate, done), 0o644))

	cmd := exec.Command(magusBin(t), "run", "--detach", "block", ".")
	cmd.Dir = ws
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	m := regexp.MustCompile(`detached as pid (\d+); its output goes to (\S+)`).FindSubmatch(out)
	require.NotNil(t, m, "the pid and log path are on stderr:\n%s", out)
	pid, err := strconv.Atoi(string(m[1]))
	require.NoError(t, err)
	logPath := string(m[2])
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	assert.Equal(t, filepath.Join(stateDir, "magus", "detached"), filepath.Dir(logPath))

	sid, err := unix.Getsid(pid)
	require.NoError(t, err)
	assert.Equal(t, pid, sid, "the detached run leads its own session")

	require.Eventually(t, func() bool {
		st, err := broker.QueryStatus(t.Context(), broker.DefaultAddr())
		if err != nil {
			return false
		}
		for _, h := range st.Capacity.Holders {
			if h.PID == pid {
				return true
			}
		}
		return false
	}, lifecycleBound, 10*time.Millisecond, "the detached run never held a claim on the broker")
	assert.False(t, proc.SocketLive(t.Context(), proc.ServerDefaultAddr()), "detaching started no server")

	require.NoError(t, os.WriteFile(gate, []byte("released\n"), 0o600))
	require.Eventually(t, func() bool { return fileContains(done, "released") },
		lifecycleBound, 10*time.Millisecond, "the detached run's step never finished")
	require.Eventually(t, func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) },
		lifecycleBound, 10*time.Millisecond, "the detached run never exited")
	assert.True(t, fileContains(logPath, "block"), "the run wrote its report to the log it named")
}
