package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/broker"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/testkit"
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
	var silent reportedExit
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, 1, silent.ExitCode())
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

func TestBrokerRefusesANegativeIdleExit(t *testing.T) {
	privateSockDir(t)
	err := brokerCommand(t.Context(), []string{"--idle-exit", "-1s"})
	require.Equal(t, exit.Usage, exit.Code(err))
	assert.False(t, broker.Live(t.Context(), broker.DefaultAddr()), "nothing was bound")
}

// TestBrokerIdleExitDefaultsToTheBrokers keeps the flag's declared default, a literal
// in the CLI registry, on the package constant a broker a run starts uses.
func TestBrokerIdleExitDefaultsToTheBrokers(t *testing.T) {
	fs := flag.NewFlagSet("broker", flag.ContinueOnError)
	f := flaggen.BindBroker(fs)
	assert.Equal(t, broker.DefaultIdleExit, f.IdleExit)
}

// The hint names the code only when that code exists at a fetchable commit.
func TestCrashHint(t *testing.T) {
	const sha = "d32c55d6700000000000000000000000000000ab"
	pushed := crashHint(versionOutput{Version: "v0.5.0", Commit: sha, Repository: sourceRepository})
	assert.Contains(t, pushed, "not in your workspace")
	assert.Contains(t, pushed, sourceRepository+"/tree/"+sha)
	assert.Contains(t, pushed, "magus-upstream-source")

	for _, v := range []versionOutput{
		{Version: "v0.5.0-dirty", Commit: sha, Dirty: true, Repository: sourceRepository},
		{Version: unknownVersion, Commit: unknownVersion, Repository: sourceRepository},
	} {
		hint := crashHint(v)
		assert.Contains(t, hint, "not published")
		assert.NotContains(t, hint, "/tree/")
	}
}

// GateArgvSuite pins which command lines the broker is told run the ci composition, the
// runs whose claims outrank every other's for seats.
type GateArgvSuite struct{ suite.Suite }

func TestGateArgvSuite(t *testing.T) { suite.Run(t, new(GateArgvSuite)) }

func (s *GateArgvSuite) TestAffectedCIIsAGate() {
	s.True(gateArgv([]string{"affected", "ci", "-s"}))
}

func (s *GateArgvSuite) TestRunCIIsAGate() {
	s.True(gateArgv([]string{"-v", "run", "ci", "."}))
}

func (s *GateArgvSuite) TestANarrowRunIsNoGate() {
	s.False(gateArgv([]string{"run", "go-test", "."}))
}

func (s *GateArgvSuite) TestAProjectNamedCIIsNoGate() {
	s.False(gateArgv([]string{"run", "lint", "ci"}))
}

func (s *GateArgvSuite) TestAnOutputFlagValueIsNotTheTarget() {
	s.True(gateArgv([]string{"run", "-o", "json", "ci"}))
}

func (s *GateArgvSuite) TestARootFlagValueIsNotTheVerb() {
	s.False(gateArgv([]string{"--root", "run", "go-test", "."}))
}

func (s *GateArgvSuite) TestAVerbThatRunsNothingIsNoGate() {
	s.False(gateArgv([]string{"status", "--wait"}))
}

// A wrapper's mark on its whole path: real `job exec`, real run, private
// broker. Its pair is TestAForkUnderJobExecSharesItsClaim.
func TestARunUnderJobExecIsRefusedOnABudgetStrangersFill(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	testkit.Isolate(t)
	privateSockDir(t)
	t.Setenv("MAGUS_BROKER", "")
	ln, err := broker.Listen(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- broker.Serve(ctx, ln, broker.WithCapacity(0, 1)) }()
	t.Cleanup(func() { cancel(); <-done })

	root := testkit.NewRepository(t, map[string]string{
		"magus.yaml":     "{}\n",
		"magusfile.buzz": "import \"magus\";\n" + testTarget,
		"a.go":           "package a\n",
	}).Root()
	self, err := os.Executable()
	require.NoError(t, err)
	bin := filepath.Join(root, "magus")
	require.NoError(t, os.Symlink(self, bin))

	stranger, err := broker.Dial(t.Context(), broker.DefaultAddr())
	require.NoError(t, err)
	defer func() { _ = stranger.Close() }()
	v, err := stranger.Request(t.Context(), types.MachineClaim{Project: "other", Target: "test", Slots: 1, PID: 1})
	require.NoError(t, err)
	require.True(t, v.Granted)

	cmd := exec.Command(bin, "job", "exec", "--timeout", "2m", "--", bin, "run", "--no-cache", "test", ".")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "%s", out)
	assert.Contains(t, string(out), "MGS3009")
}

type BrokerStopSuite struct{ suite.Suite }

func TestBrokerStopSuite(t *testing.T) { suite.Run(t, new(BrokerStopSuite)) }

// standInOtherBuild refuses one hello as a broker on another protocol does.
func (s *BrokerStopSuite) standInOtherBuild() {
	sock := strings.TrimPrefix(broker.DefaultAddr(), "unix://")
	ln, err := net.Listen("unix", sock)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = ln.Close() })
	s.Require().NoError(os.WriteFile(sock+".handshake", []byte("stand-in"), 0o600))
	serveOtherBuild(ln, false)
}

// serveOtherBuild refuses every hello on ln, or hangs up on each with hangup.
func serveOtherBuild(ln net.Listener, hangup bool) {
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = nc.Close() }()
				if hangup {
					return
				}
				_, _ = nc.Write([]byte(`{"type":"handshake"}` + "\n"))
				line, err := bufio.NewReader(nc).ReadBytes('\n')
				if err != nil {
					return
				}
				var f struct {
					ID uint64 `json:"id"`
				}
				_ = json.Unmarshal(line, &f)
				refusal := `{"type":"error","id":%d,"body":{"code":"protocol","message":"protocol 99"}}`
				_, _ = fmt.Fprintf(nc, refusal+"\n", f.ID)
			}()
		}
	}()
}

// TestHelperOtherBuildBroker is not a test: it is a broker of another build,
// whose argv says broker: `broker <socket> <SIGTERMs to exit 7 at> <mode>`. The
// test binary scrubs its environment, so argv carries it all.
func TestHelperOtherBuildBroker(t *testing.T) {
	args := flag.Args()
	if len(args) != 4 || args[0] != "broker" {
		t.Skip("subprocess helper; not run directly")
	}
	sock := args[1]
	terms := make(chan os.Signal, 4)
	signal.Notify(terms, syscall.SIGTERM)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		os.Exit(3)
	}
	serveOtherBuild(ln, args[3] == "hangup")
	_ = os.WriteFile(sock+".handshake", []byte("stand-in"), 0o600)
	want, _ := strconv.Atoi(args[2])
	for range want {
		<-terms
	}
	os.Exit(7)
}

// otherBuild runs that helper at this test's socket and returns its exit code.
// Reaping it is what lets the stop see it gone.
func (s *BrokerStopSuite) otherBuild(terms int, mode string) (pid int, exited <-chan int) {
	privateSockDir(s.T())
	sock := strings.TrimPrefix(broker.DefaultAddr(), "unix://")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperOtherBuildBroker$", "broker", sock, strconv.Itoa(terms), mode)
	s.Require().NoError(cmd.Start())
	done := make(chan int, 1)
	go func() {
		_ = cmd.Wait()
		done <- cmd.ProcessState.ExitCode()
	}()
	s.T().Cleanup(func() { _ = cmd.Process.Kill() })
	s.Require().Eventually(func() bool {
		_, err := os.Stat(sock + ".handshake")
		return err == nil
	}, 30*time.Second, 5*time.Millisecond, "the stand-in never bound its socket")
	return cmd.Process.Pid, done
}

func (s *BrokerStopSuite) TestABrokerOfAnotherBuildIsStopped() {
	_, exited := s.otherBuild(1, "refuse")
	s.Require().NoError(brokerStop(s.T().Context(), nil))
	s.Equal(7, <-exited, "it took the one SIGTERM")
}

func (s *BrokerStopSuite) TestAPeerThatHangsUpOnTheHelloIsStoppedNotDenied() {
	_, exited := s.otherBuild(1, "hangup")
	s.Require().NoError(brokerStop(s.T().Context(), nil))
	s.Equal(7, <-exited)
}

func (s *BrokerStopSuite) TestABrokerStillDrainingGetsASecondSignal() {
	saved := brokerDrainWait
	brokerDrainWait = 200 * time.Millisecond
	s.T().Cleanup(func() { brokerDrainWait = saved })
	_, exited := s.otherBuild(2, "refuse")
	s.Require().NoError(brokerStop(s.T().Context(), nil))
	s.Equal(7, <-exited, "it took the second SIGTERM")
}

func (s *BrokerStopSuite) TestABrokerThatOutlivesBothSignalsIsNamed() {
	saved := brokerDrainWait
	brokerDrainWait = 50 * time.Millisecond
	s.T().Cleanup(func() { brokerDrainWait = saved })
	pid, _ := s.otherBuild(3, "refuse")
	err := brokerStop(s.T().Context(), nil)
	s.Require().Error(err)
	s.ErrorContains(err, fmt.Sprintf("kill -KILL %d", pid))
}

// The holder here is this test process, whose argv says no broker: signalling
// it would end the suite.
func (s *BrokerStopSuite) TestAHolderThatIsNoBrokerIsNotSignalled() {
	privateSockDir(s.T())
	s.standInOtherBuild()
	err := brokerStop(s.T().Context(), nil)
	s.Require().Error(err)
	s.ErrorIs(err, types.PeerFromAnotherBuild)
	s.ErrorContains(err, fmt.Sprintf("pid %d", os.Getpid()))
	s.ErrorContains(err, "signalled nothing")
	s.ErrorContains(err, "systemctl --user stop magus-broker.socket magus-broker.service")
}

func (s *BrokerStopSuite) TestStoppingOnlyTheServicesOfAnotherBuildSaysWhatItCan() {
	pid, exited := s.otherBuild(1, "refuse")
	err := brokerStop(s.T().Context(), []string{"--services"})
	s.Require().Error(err)
	s.ErrorContains(err, fmt.Sprintf("broker (pid %d)", pid))
	s.ErrorContains(err, "`magus broker stop` stops it whole")
	s.Empty(exited, "nothing was signalled")
}

func (s *BrokerStopSuite) TestAnUnreadablePeerSaysWhatToRun() {
	err := unknownBrokerError("/run/broker.sock", errors.ErrUnsupported, types.PeerFromAnotherBuild)
	s.ErrorContains(err, "`lsof -U`")
	s.ErrorContains(err, "/run/broker.sock")
	s.ErrorContains(err, "`kill -TERM <pid>`")
}

func (s *BrokerStopSuite) TestStatusNamesABrokerOfAnotherBuild() {
	s.otherBuild(1, "refuse")
	err := brokerStatus(s.T().Context(), nil)
	s.Require().ErrorIs(err, types.PeerFromAnotherBuild)
}

func (s *BrokerStopSuite) TestNoBrokerIsStillNoBroker() {
	privateSockDir(s.T())
	s.Equal(exit.Reported(exit.Failure), brokerStop(s.T().Context(), nil))
}
