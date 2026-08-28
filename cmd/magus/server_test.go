package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServerStopTerminatesLiveDaemon drives the real serverStop handler against a live
// in-process proc server, pinning the fix for the silent no-op stop: stop must resolve the
// server, send the shutdown, and verify the socket has actually gone quiet before returning
// success. A full `server start` daemon cannot be driven from a unit test (its auto-background
// path re-execs the binary), so this exercises the discovery+verify logic that stop owns.
func TestServerStopTerminatesLiveDaemon(t *testing.T) {
	// Let proc pick a random socket under SockDir; a t.TempDir() path can exceed the unix
	// socket path length limit on macOS.
	srv, err := proc.New(proc.Options{
		Handler: func(context.Context, []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())
	addr := srv.Addr()
	require.True(t, proc.SocketLive(context.Background(), addr), "server should be live before stop")

	// The explicit --socket bypasses config/discovery so stop targets exactly this server.
	err = serverStop(context.Background(), []string{"--socket", addr})
	require.NoError(t, err, "stop against a live daemon must succeed")

	assert.False(t, proc.SocketLive(context.Background(), addr), "stop must actually terminate the daemon")
	select {
	case <-srv.Done():
	default:
		t.Fatal("stop returned without the server having been closed")
	}
}

// TestServerStopNoDaemonExitsNonzero pins the other half of the fix: stop against nothing must
// not exit 0 silently. It returns a non-zero exit (errSilent) rather than pretending success.
func TestServerStopNoDaemonExitsNonzero(t *testing.T) {
	addr := "unix://" + proc.SockDir() + "/magus-absent-test.sock"
	err := serverStop(context.Background(), []string{"--socket", addr})
	require.Error(t, err, "stop against a dead socket must report failure")

	var silent errSilent
	require.ErrorAs(t, err, &silent)
	assert.NotZero(t, silent.exitCode, "stopping nothing must exit non-zero")
}

func TestIsServerStartHelpSkipsTheSubcommand(t *testing.T) {
	assert.True(t, isServerStartHelp([]string{"start", "-h"}))
	assert.True(t, isServerStartHelp([]string{"start", "--foreground", "--help"}))
	assert.True(t, isServerStartHelp([]string{"start", "help"}))
	assert.False(t, isServerStartHelp([]string{"start"}))
	assert.False(t, isServerStartHelp([]string{"start", "--foreground"}))
	assert.False(t, isServerStartHelp([]string{"help"}), "the subcommand itself is not a help flag")
}

func TestWantsForeground(t *testing.T) {
	assert.True(t, wantsForeground([]string{"start", "--foreground"}))
	assert.True(t, wantsForeground([]string{"start", "-foreground"}))
	// A pre-parse scanner takes only the bare spellings; an =value form is not a bool.
	assert.False(t, wantsForeground([]string{"start", "--foreground=true"}))
	assert.False(t, wantsForeground([]string{"start"}))
}

func TestConsoleURLsDegradeToEmpty(t *testing.T) {
	prev := globalCfg.Console.Enabled
	t.Cleanup(func() { globalCfg.Console.Enabled = prev })

	off := false
	globalCfg.Console.Enabled = &off
	assert.Equal(t, "", consoleWatchURL())
	assert.Equal(t, "", consoleDiffURL())
}

// The message exists because "already running" answered a question nobody asked. A second
// worktree's `server start` returns 0 having loaded nothing from that tree, and the console then
// shows the tree the daemon was started in - which reads as success.
func TestServingSuffixNamesTheLoadedWorkspaces(t *testing.T) {
	st := &proc.StatusReply{Workspaces: []proc.Workspace{
		{Root: "/repo/worktrees/b"},
		{Root: "/repo"},
	}}

	// Sorted, so two runs of the same daemon do not print the list two ways.
	assert.Equal(t, ", serving /repo, /repo/worktrees/b", servingSuffix(st))

	// A daemon that has loaded nothing yet says nothing rather than "serving " with an empty
	// list, which would read as a daemon that is serving something unnameable.
	assert.Empty(t, servingSuffix(&proc.StatusReply{}))
}

// TestEnsureConsoleDaemonReturnsWithoutSpawning pins the early return: a console that is
// already serving must not start a second daemon. Nothing else in the test suite reaches
// ensureConsoleDaemon, and the spawn path it guards is the one that leaves a process behind.
func TestEnsureConsoleDaemonReturnsWithoutSpawning(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // a bound bridge answers; 401 is reachable
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	globalCfg.MCP.Address = addr

	require.NoError(t, ensureConsoleDaemon(t.Context(), addr, t.TempDir()))
}

// TestDaemonChildEnvDropsTheInheritedSocket pins the scrub. A child that inherits
// MAGUS_DAEMON_SOCKET decides it is already adopted, binds no socket of its own, and then
// reports the parent's - leaving a daemon `server stop` cannot find.
func TestDaemonChildEnvDropsTheInheritedSocket(t *testing.T) {
	t.Setenv("MAGUS_DAEMON_SOCKET", "/tmp/magus-parent.sock")
	t.Setenv("MAGUS_KEEP_ME", "1")

	env := daemonChildEnv()

	for _, kv := range env {
		assert.False(t, strings.HasPrefix(kv, "MAGUS_DAEMON_SOCKET="), "child inherited %q", kv)
	}
	assert.Contains(t, env, "MAGUS_KEEP_ME=1", "the scrub must drop one variable, not the environment")
}
