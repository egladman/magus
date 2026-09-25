package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/types"
)

// TestBrokerOpensNoInternetSocket runs the real `magus broker` and asks the kernel what
// it has open. The broker is started by builds, inside CI and containers and over ssh,
// and anything a build leaves listening on the network is a finding; the rule is that
// the broker entry point never reaches a TCP listener, and this holds it to that from
// the outside rather than by reading the code.
func TestBrokerOpensNoInternetSocket(t *testing.T) {
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		t.Skip("lsof is not installed, so the fd table cannot be read")
	}
	privateSockDir(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("MAGUS_BROKER", "")

	// testscript.Main runs the CLI when the binary is invoked as `magus`, so a link by
	// that name runs the real verb in a child process.
	bin := filepath.Join(t.TempDir(), "magus")
	self, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(self, bin))

	// A file, not a bytes.Buffer: exec copies into a buffer from its own goroutine.
	logPath := filepath.Join(t.TempDir(), "broker.log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = logFile.Close() })
	cmd := exec.Command(bin, "broker")
	cmd.Dir = t.TempDir()
	cmd.Stderr = logFile
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	addr := broker.DefaultAddr()
	if !assert.Eventually(t, func() bool { return broker.Live(t.Context(), addr) }, 10*time.Second, 20*time.Millisecond) {
		logged, _ := os.ReadFile(logPath)
		t.Fatalf("the broker never bound its socket; stderr:\n%s", logged)
	}
	st, err := broker.QueryStatus(t.Context(), addr)
	require.NoError(t, err)
	require.Equal(t, cmd.Process.Pid, st.PID, "the process answering is the one under inspection")

	out, err := exec.Command(lsof, "-a", "-p", strconv.Itoa(cmd.Process.Pid), "-i").CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Skipf("lsof could not run: %v", err)
	}
	assert.Empty(t, bytes.TrimSpace(out), "the broker holds an AF_INET/AF_INET6 socket:\n%s", out)
}

// TestBrokerStatusExitsNonZeroWithNoneRunning pins the chaining contract `server status`
// already keeps.
func TestBrokerStatusExitsNonZeroWithNoneRunning(t *testing.T) {
	privateSockDir(t)
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg.Broker = types.BrokerRequired

	err := brokerStatus(t.Context(), nil)
	var silent errSilent
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, 1, silent.exitCode)
}

func TestBrokerStatusPrintsItsRows(t *testing.T) {
	privateSockDir(t)
	ln, err := broker.Listen(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- broker.Serve(ctx, ln, broker.WithCapacity(2048, 4)) }()
	t.Cleanup(func() { cancel(); <-done })

	c, err := broker.Dial(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_, err = c.Request(t.Context(), types.MachineClaim{Project: "api", Target: "test", MemoryMB: 1024, Slots: 2, Dir: "/src/a"})
	require.NoError(t, err)

	st, err := queryBroker(t.Context())
	require.NoError(t, err)
	var buf bytes.Buffer
	printBrokerRows(&buf, st, types.BrokerBestEffort, time.Now())
	got := buf.String()
	assert.Contains(t, got, "broker  ")
	assert.Contains(t, got, "capacity  -")
	assert.Contains(t, got, "slots 2/4")
	assert.Contains(t, got, "mem 1.0 GiB/2.0 GiB")
	assert.Contains(t, got, "broker: best-effort", "the policy in force rides the capacity row")
	assert.Contains(t, got, "held  ")
	assert.Contains(t, got, "api test")
	assert.Contains(t, got, "/src/a")
	assert.Contains(t, got, "exits after 10m0s holding nothing")
}

func TestBrokerDownRowNamesThePolicy(t *testing.T) {
	var buf bytes.Buffer
	printBrokerDown(&buf, types.BrokerOff)
	assert.Contains(t, buf.String(), "not running (runs here never ask one; broker: off)")

	buf.Reset()
	printBrokerDown(&buf, "")
	assert.Contains(t, buf.String(), "a run starts one; broker: best-effort", "unset reads as the default it behaves as")
}

func TestServerRowsNameEveryListener(t *testing.T) {
	var buf bytes.Buffer
	printServerRows(&buf, &types.StatusServer{
		PID: 47001, Socket: "unix:///run/magus/server.sock", StartTime: time.Now().Add(-time.Hour),
		Listeners: []types.StatusListener{
			{Kind: types.ListenerSocket, Address: "unix:///run/magus/server.sock"},
			{Kind: types.ListenerHTTP, Address: "127.0.0.1:7391"},
		},
		Watch: []string{"/repo"},
	}, time.Now())
	got := buf.String()
	assert.Contains(t, got, "server  47001")
	assert.Contains(t, got, "listen  47001  http 127.0.0.1:7391")
	assert.Contains(t, got, "watch   47001  graph+symbols  /repo")
}
