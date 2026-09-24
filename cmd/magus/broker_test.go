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

// TestBrokerUnitsRenderWhatTheSupervisorReads pins the units byte for byte, with a
// binary path that needs quoting and a socket path carrying systemd's specifier
// character: a unit that parses differently from what was printed is a broker that
// never starts.
func TestBrokerUnitsRenderWhatTheSupervisorReads(t *testing.T) {
	f := unitFacts{
		exe:       "/opt/my tools/magus",
		socket:    "/run/user/1000/magus%/broker.sock",
		log:       "/home/eli/.local/state/magus/broker.log",
		home:      "/home/eli",
		configDir: "/home/eli/.config",
		env:       [][2]string{{"TMPDIR", "/var/folders/x&y/T/"}},
	}

	systemd, err := renderBrokerUnits(supervisorSystemd, f)
	require.NoError(t, err)
	assert.Equal(t, []brokerUnit{
		{Supervisor: "systemd", Path: "/home/eli/.config/systemd/user/magus-broker.socket", Content: `[Unit]
Description=magus broker socket

[Socket]
ListenStream=/run/user/1000/magus%%/broker.sock
SocketMode=0600
DirectoryMode=0700

[Install]
WantedBy=sockets.target
`},
		{Supervisor: "systemd", Path: "/home/eli/.config/systemd/user/magus-broker.service", Content: `[Unit]
Description=magus broker: this host's capacity and shared services
Requires=magus-broker.socket
After=magus-broker.socket

[Service]
ExecStart="/opt/my tools/magus" broker
`},
	}, systemd)

	launchd, err := renderBrokerUnits(supervisorLaunchd, f)
	require.NoError(t, err)
	assert.Equal(t, []brokerUnit{{Supervisor: "launchd", Path: "/home/eli/Library/LaunchAgents/magus.broker.plist", Content: `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>magus.broker</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/my tools/magus</string>
    <string>broker</string>
    <string>--idle-exit</string>
    <string>0</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>TMPDIR</key>
    <string>/var/folders/x&amp;y/T/</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardErrorPath</key>
  <string>/home/eli/.local/state/magus/broker.log</string>
</dict>
</plist>
`}}, launchd)

	_, err = renderBrokerUnits("runit", f)
	assert.Error(t, err, "a supervisor magus prints nothing for is refused, not guessed at")
}

func TestBrokerRefusesANegativeIdleExit(t *testing.T) {
	privateSockDir(t)
	err := brokerCmd(t.Context(), []string{"--idle-exit", "-1s"})
	var usage errUsage
	require.ErrorAs(t, err, &usage)
	assert.False(t, broker.Live(t.Context(), broker.DefaultAddr()), "nothing was bound")
}
