package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egladman/magus/broker"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/internal/sys/pid"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

// brokerReadyTimeout bounds how long a run waits for a broker it spawned to bind its
// socket. The broker loads nothing, so this is generous.
const brokerReadyTimeout = 10 * time.Second

func brokerCommand(ctx context.Context, args []string) error {
	if brokerServes(args) {
		return brokerServe(ctx, args)
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		brokerUsage()
		return nil
	case hint.BrokerStatus.Leaf():
		return brokerStatus(ctx, args[1:])
	case hint.BrokerStop.Leaf():
		return brokerStop(ctx, args[1:])
	case hint.BrokerUnits.Leaf():
		return brokerUnits(ctx, args[1:])
	default:
		brokerUsage()
		return exit.Usagef("magus broker: unknown target %q (want status, stop or units, or nothing to run one)", args[0])
	}
}

// brokerServes reports whether a `broker` argv runs a broker in this process: no target,
// only flags, and no request for help.
func brokerServes(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "-h", "-help", "--help":
		return false
	}
	return strings.HasPrefix(args[0], "-")
}

func brokerUsage() {
	fmt.Fprintln(os.Stderr, "usage: magus broker [status|stop|units] [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The broker holds this host's capacity (slots and declared memory) and the")
	fmt.Fprintln(os.Stderr, "services every magus on it shares. A run or session brief starts one when none answers; it")
	fmt.Fprintln(os.Stderr, "listens on a unix socket only and exits once it has held nothing for ten minutes.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Targets:")
	fmt.Fprintln(os.Stderr, "  (none)  run a broker in this process, logging to stderr or to --log; under")
	fmt.Fprintln(os.Stderr, "          systemd it serves the socket the supervisor hands over")
	fmt.Fprintln(os.Stderr, "  status  is one up, what it holds; exits non-zero when none is")
	fmt.Fprintln(os.Stderr, "  stop    stop it, or with --services stop only the services it hosts")
	fmt.Fprintln(os.Stderr, "  units   print the systemd or launchd units that supervise it")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags, with no target:")
	fmt.Fprintln(os.Stderr, "  --idle-exit DURATION  exit after holding nothing this long; 0 never exits")
	fmt.Fprintln(os.Stderr, "  --log FILE            write its log to FILE instead of stderr")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Signals: SIGHUP reopens the --log file. A first SIGTERM drains: nothing new is")
	fmt.Fprintln(os.Stderr, "seated, and it exits once the runs holding it finish or shutdown_grace passes.")
	fmt.Fprintln(os.Stderr, "A second SIGTERM, or a SIGINT, stops its services and exits now.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Socket: "+broker.DefaultAddr())
	fmt.Fprintln(os.Stderr, "Log, when a run started it: "+brokerLogPath())
}

// brokerServe is `magus broker`: serve this host's capacity and shared services until
// idle, stopped or signalled. Losing the bind to a live broker is the ordinary end of a
// start race, so it exits 0. A socket a supervisor hands over is served instead of
// binding one.
func brokerServe(ctx context.Context, args []string) error {
	var f *flaggen.BrokerFlags
	rest, err := commandParse("broker", args, func(fs *flag.FlagSet) {
		f = flaggen.BindBroker(fs)
		fs.Usage = brokerUsage
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus broker: unexpected argument %q (want status, stop or units, or flags only to run one)", rest[0])
	}
	if f.IdleExit < 0 {
		return exit.Usagef("magus broker: --idle-exit %s is negative; 0 never exits", f.IdleExit)
	}
	if f.Log != "" {
		if err := redirectStdio(f.Log); err != nil {
			return fmt.Errorf("magus broker: %w", err)
		}
	}

	addr := broker.DefaultAddr()
	ln, err := broker.Activated(addr)
	if err != nil {
		return fmt.Errorf("magus broker: %w", err)
	}
	from := "serving"
	if ln != nil {
		from = "serving the supervisor's socket"
	} else {
		ln, err = broker.Listen(ctx, addr)
		if errors.Is(err, broker.ErrRunning) {
			slog.InfoContext(ctx, "a broker is already serving "+addr, attr.Notice(""), attr.Component("magus"))
			return nil
		}
		if err != nil {
			return fmt.Errorf("magus broker: %w", err)
		}
	}
	if warn := broker.OtherBrokersWarning(broker.OtherBrokers(ctx, proc.SockDir())); warn != "" {
		slog.WarnContext(ctx, warn, attr.Component("magus"))
	}

	// Services outlive the runs that asked for them, so a journal records each one's
	// stop command; a broker that crashed left orphans, and they are swept before
	// anything new is hosted.
	journal, jerr := service.NewJournal(filepath.Join(proc.SockDir(), "services"))
	if jerr != nil {
		slog.With(attr.Component("broker")).Warn("service journal unavailable; crash reaping disabled", attr.Error(jerr))
	} else if res := journal.Sweep(ctx); res.Reaped > 0 || res.Unreapable > 0 {
		slog.With(attr.Component("broker")).Info("reaped orphaned services a previous broker left",
			slog.Int("reaped", res.Reaped), slog.Int("left_running", res.Unreapable))
	}
	reg := service.New(service.ExecRunner{}, defaultServiceIdle, service.WithJournal(journal))

	// Sized from HOST facts, never from the workspace that happened to start it: the
	// broker is started by whichever run got there first. Memory comes from what this
	// process may commit, so a broker inside a memory-limited container budgets the
	// container; slots come from the cores, the ceiling ClampConcurrency holds every run
	// to. The profile decides memory's reservation.
	memMB := mem.BudgetMB(mem.UsableBytes(ctx), globalCfg.ConcurrencyProfile)
	slots := cache.MachineCeiling()
	exits := "never exits for idleness"
	if f.IdleExit > 0 {
		exits = fmt.Sprintf("exits after %s holding nothing", idleText(f.IdleExit))
	}
	slog.InfoContext(ctx, fmt.Sprintf("broker (pid %d) %s %s: %d slots, %s; %s",
		os.Getpid(), from, addr, slots, cache.FormatMB(memMB), exits), attr.Notice(""), attr.Component("magus"))

	ctx, stopNow := context.WithCancel(ctx)
	defer stopNow()
	drain := make(chan struct{})
	release := lifecycle{
		hangup:         func() { reopenBrokerLog(ctx, f.Log) },
		stop:           func() { close(drain) },
		stopNow:        stopNow,
		interruptIsNow: true,
	}.watch(ctx)
	defer release()

	err = broker.Serve(ctx, ln,
		broker.WithCapacity(memMB, slots),
		broker.WithServices(serviceHost{reg}),
		broker.WithIdleExit(f.IdleExit),
		broker.WithLogger(slog.Default()),
		broker.WithVersion(version),
		broker.WithDrain(drain, globalCfg.ShutdownGrace),
		broker.WithCrashDir(crashReportDir()),
	)
	// The broker's own context may be done (a signal), and Shutdown's wait for a service
	// still starting returns at once on a done context, so teardown gets a fresh bound.
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.DefaultShutdownTimeout)
	defer cancel()
	reg.Shutdown(stopCtx)
	return err
}

// reopenBrokerLog answers SIGHUP. A broker logging to stderr under a supervisor has no
// file to reopen, and says so rather than exiting.
func reopenBrokerLog(ctx context.Context, path string) {
	if path == "" {
		slog.With(attr.Component("broker")).InfoContext(ctx, "SIGHUP reopens the --log file, and this broker logs to stderr")
		return
	}
	if err := redirectStdio(path); err != nil {
		slog.With(attr.Component("broker")).ErrorContext(ctx, "could not reopen its log; still writing to the old one", attr.Error(err))
		return
	}
	slog.With(attr.Component("broker")).InfoContext(ctx, "reopened its log", slog.String("log", path))
}

func brokerStatus(ctx context.Context, args []string) error {
	rest, err := commandParse("broker status", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus broker status [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The broker: its pid, capacity, every claim holding it and the services it")
			fmt.Fprintln(os.Stderr, "hosts. Exits non-zero when none is running, so a script can chain on it.")
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus broker status: takes no arguments (got %q)", rest[0])
	}
	if warn := broker.OtherBrokersWarning(broker.OtherBrokers(ctx, proc.SockDir())); warn != "" {
		slog.WarnContext(ctx, warn, attr.Notice(""))
	}
	st, qerr := queryBroker(ctx)
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		if qerr != nil {
			return exit.Reported(exit.Failure)
		}
		return emitRecord(opts, st, []string{strconv.Itoa(st.PID)})
	}
	if errors.Is(qerr, types.PeerFromAnotherBuild) {
		return fmt.Errorf("broker status: %w", qerr)
	}
	if qerr != nil {
		why := "a run starts one"
		if globalCfg.Broker.Resolved() == types.BrokerOff {
			why = "runs here never ask one"
		}
		slog.ErrorContext(ctx, fmt.Sprintf("no broker is running (broker: %s); %s", globalCfg.Broker, why), attr.Notice(""))
		return exit.Reported(exit.Failure)
	}
	printBrokerRows(os.Stdout, st, globalCfg.Broker, time.Now())
	return nil
}

func brokerStop(ctx context.Context, args []string) error {
	var f *flaggen.BrokerStopFlags
	if _, err := commandParse("broker stop", args, func(fs *flag.FlagSet) {
		f = flaggen.BindBrokerStop(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus broker stop [--services]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Stop the broker. Every claim on this host goes with it; runs already going")
			fmt.Fprintln(os.Stderr, "keep going and re-assert their claims on the next broker. With --services,")
			fmt.Fprintln(os.Stderr, "stop only the services it hosts and leave it running.")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted):")
			printOwnDefaults(fs)
		}
	}); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	c, err := broker.Dial(ctx, broker.DefaultAddr())
	if err != nil {
		pid, err := stopUnanswering(ctx, f.Services, err)
		if err == nil && opts.Format != outputText {
			return emitRecord(opts, brokerStopOutput{Stopped: true, PID: pid}, []string{strconv.Itoa(pid)})
		}
		return err
	}
	defer func() { _ = c.Close() }()
	if f.Services {
		n, err := c.StopServices(ctx)
		if err != nil {
			return fmt.Errorf("broker stop: %w", err)
		}
		if opts.Format != outputText {
			return emitRecord(opts, brokerStopOutput{Services: n}, []string{"services"})
		}
		slog.InfoContext(ctx, fmt.Sprintf("stopped %d hosted service(s); the broker is still running", n), attr.Notice(""), attr.Component("magus"))
		return nil
	}
	st, err := c.Status(ctx)
	if err != nil {
		return fmt.Errorf("broker stop: %w", err)
	}
	// The hangup, not the socket file going, is the stop: under systemd the supervisor
	// keeps the socket, and the next connection starts another broker.
	sctx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	if err := c.Shutdown(sctx); errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("broker stop: broker (pid %d) did not stop within %s", st.PID, stopTimeout)
	} else if err != nil {
		return fmt.Errorf("broker stop: %w", err)
	}
	if opts.Format != outputText {
		return emitRecord(opts, brokerStopOutput{Stopped: true, PID: st.PID}, []string{strconv.Itoa(st.PID)})
	}
	slog.InfoContext(ctx, fmt.Sprintf("stopped broker (pid %d)", st.PID), attr.Notice(""), attr.Component("magus"))
	return nil
}

type brokerStopOutput struct {
	Stopped  bool `json:"stopped"`
	PID      int  `json:"pid"`
	Services int  `json:"services"`
}

// brokerDrainWait is how long a signalled broker gets per SIGTERM. A var so a
// test can shorten it.
var brokerDrainWait = stopTimeout

// stopUnanswering stops whatever holds the broker socket when this build
// cannot talk to it, as another build's broker refuses to. The kernel names
// the holder.
func stopUnanswering(ctx context.Context, services bool, dialErr error) (int, error) {
	addr := broker.DefaultAddr()
	sock := strings.TrimPrefix(addr, "unix://")
	pid, err := broker.SocketPeer(ctx, addr)
	if errors.Is(err, broker.ErrUnavailable) {
		slog.ErrorContext(ctx, "no broker is running", attr.Notice(""), attr.Component("magus"))
		return 0, exit.Reported(exit.Failure)
	}
	if err != nil {
		return 0, unknownBrokerError(sock, err, dialErr)
	}
	if services {
		return 0, fmt.Errorf("broker stop --services: broker (pid %d) does not answer this build, which cannot ask it to stop only its services, so `magus broker stop` stops it whole: %w", pid, dialErr)
	}
	command := processCommand(ctx, pid)
	if !slices.Contains(strings.Fields(command), "broker") {
		return 0, fmt.Errorf("broker stop: pid %d (%s) holds %s and is no broker, so magus signalled nothing, and under systemd it is the socket unit, so run `systemctl --user stop magus-broker.socket magus-broker.service`: %w",
			pid, cmp.Or(command, "command unreadable"), sock, dialErr)
	}
	return pid, signalBrokerStop(ctx, pid, dialErr)
}

// unknownBrokerError says what to run when the kernel will not name the holder.
func unknownBrokerError(sock string, err, dialErr error) error {
	return fmt.Errorf("broker stop: something holds %s and does not answer this build, and its pid is unreadable here, so run `lsof -U`, take the pid on the line naming that socket, then `kill -TERM <pid>`: %w",
		sock, fmt.Errorf("%w: %w", err, dialErr))
}

// signalBrokerStop sends pid SIGTERM, which every build's broker handles: the
// first drains it and a second stops it now. It returns once pid is gone.
func signalBrokerStop(ctx context.Context, pid int, dialErr error) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("broker stop: find broker (pid %d): %w", pid, err)
	}
	slog.InfoContext(ctx, fmt.Sprintf("broker (pid %d) does not answer this build; sending it SIGTERM and waiting up to %s for it to drain",
		pid, brokerDrainWait), attr.Error(dialErr), attr.Notice(""), attr.Component("magus"))
	for range 2 {
		if err := p.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("broker stop: signal broker (pid %d): %w", pid, err)
		}
		if waitProcessGone(ctx, pid, brokerDrainWait) {
			slog.InfoContext(ctx, fmt.Sprintf("stopped broker (pid %d)", pid), attr.Notice(""), attr.Component("magus"))
			return nil
		}
	}
	return fmt.Errorf("broker stop: broker (pid %d) outlived two SIGTERMs, %s apart, so run `kill -KILL %d`", pid, brokerDrainWait, pid)
}

func waitProcessGone(ctx context.Context, procID int, bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for pid.Alive(procID) {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// processCommand is pid's command line, "" when it cannot be read.
func processCommand(ctx context.Context, pid int) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// queryBroker reads the broker's status, bounded so a wedged broker cannot hang a
// status command.
func queryBroker(ctx context.Context) (types.StatusBroker, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return broker.QueryStatus(ctx, broker.DefaultAddr())
}

// idleText is an idle window as a person reads it.
func idleText(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	}
	return d.String()
}

// brokerLogPath is where a broker a run started writes: the XDG state directory, which
// survives logout, where the runtime directory holding its socket does not.
func brokerLogPath() string { return stateLogPath("broker.log") }

// serverLogPath is brokerLogPath's counterpart for a server `server start` detached.
func serverLogPath() string { return stateLogPath("server.log") }

// crashReportDir is where the broker saves the crash report of a process it watched.
func crashReportDir() string { return stateLogPath("crashes") }

func stateLogPath(name string) string {
	dir, err := xdg.StateDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "magus", name)
}

// spawnBroker is the seam a test replaces to observe that a broker WOULD be started
// without one being started.
var spawnBroker = func() (pid int, logPath string, err error) {
	logPath = brokerLogPath()
	pid, err = spawnDetached([]string{"broker", "--" + flaggen.FlagBrokerLog, logPath}, logPath)
	return pid, logPath, err
}

// ensureBroker starts a broker when none answers and returns the pid of the one it
// started, 0 when one was already up or none came up. Only a run that will execute
// steps calls it, and never under `broker: off`. Losing a start race is fine: bind
// decides which broker serves, and the loser exits.
//
// A broker that will not come up is left to the policy: under best-effort the first
// step runs unarbitrated and says so, under required it refuses with MGS3022.
func ensureBroker(ctx context.Context) int {
	addr := broker.DefaultAddr()
	if broker.Live(ctx, addr) {
		return 0
	}
	pid, logPath, err := spawnBroker()
	if err != nil {
		slog.With(attr.Component("magus")).Debug("could not start a broker", attr.Error(err))
		return 0
	}
	deadline := time.Now().Add(brokerReadyTimeout)
	for !broker.Live(ctx, addr) {
		if time.Now().After(deadline) || ctx.Err() != nil {
			slog.With(attr.Component("magus")).Debug("the broker a run started did not come up", slog.Int("pid", pid), slog.String("log", logPath))
			return 0
		}
		time.Sleep(20 * time.Millisecond)
	}
	return pid
}

func startedBrokerMessage(pid int) string {
	return fmt.Sprintf("started a broker (pid %d) to hold this host's capacity; it opens no network listener and exits after %s holding nothing (`%s` lists it)",
		pid, idleText(broker.DefaultIdleExit), hint.BrokerStatus)
}

// noticeBroker tells the person that their run left a process behind, and that it listens
// on nothing but its socket: a background process nobody mentions is the surprise this
// removes. The pid, log and idle attributes ride along only under -o jsonl, since a pretty
// display would print them.
func noticeBroker(ctx context.Context, pid int) {
	if pid == 0 {
		return
	}
	attrs := []slog.Attr{attr.Notice(""), attr.Component("magus")}
	if global.output == string(outputJSONL) {
		attrs = append(attrs,
			slog.Int("pid", pid),
			slog.String("log", brokerLogPath()),
			slog.Int("idle_exit_s", int(broker.DefaultIdleExit/time.Second)))
	}
	slog.LogAttrs(ctx, slog.LevelInfo, startedBrokerMessage(pid), attrs...)
}

var upstreamSourceSkill = agent.MustSkill("magus-upstream-source")

// crashHint is what the broker writes after Go's trace when this process crashes: that
// the defect is magus's, and where this build's source can be read.
func crashHint(v versionOutput) string {
	head := fmt.Sprintf("magus %s crashed. This is a defect in magus, not in your workspace.\n", v.Version)
	if v.Dirty || v.Commit == unknownVersion {
		return head + "This build's source is not published: it was built from a modified or unstamped tree.\n"
	}
	return head + fmt.Sprintf("Its source is %s/tree/%s; the %s skill reads it.\n", v.Repository, v.Commit, upstreamSourceSkill)
}

// newBrokerClient is the broker client a CLI process opens its workspaces with. It
// starts a broker when a step or a service finds none, which is how a long-lived
// process (`magus mcp`, the server) gets one back after the last idled out. announce
// false keeps a detached server's notice out of its log.
func newBrokerClient(announce bool, opts ...broker.ClientOption) *broker.Client {
	return broker.NewClient(broker.DefaultAddr(), append([]broker.ClientOption{
		broker.WithIdentity(os.Args, version),
		broker.WithCrashHint(crashHint(newVersionOutput())),
		broker.WithStart(func(ctx context.Context) bool {
			pid := ensureBroker(ctx)
			if announce {
				noticeBroker(ctx, pid)
			}
			return broker.Live(ctx, broker.DefaultAddr())
		}),
	}, opts...)...)
}

var (
	processBrokerOnce sync.Once
	processBroker     *broker.Client
)

// processBrokerClient is this process's one broker client: one connection per process
// is what lets the broker release everything the process held when it exits. It names
// the lease the process's checkout binds, and whether the process is a gate.
func processBrokerClient() *broker.Client {
	processBrokerOnce.Do(func() {
		processBroker = newBrokerClient(true, broker.WithLease(processLease(extractRootFlag(os.Args[1:]))), broker.WithGate(gateArgv(os.Args[1:])))
	})
	return processBroker
}

// processLease is the lease this process's runs act under, as job.ActingLease resolves it
// in the checkout workspace.FindRoot(rootOverride) finds. Outside a workspace only BAGGAGE
// can name one. A checkout whose job store cannot be placed is said, and BAGGAGE answers.
func processLease(rootOverride string) string {
	claim := trail.LeaseFromEnv()
	root, err := workspace.FindRoot(rootOverride)
	if err != nil {
		return claim
	}
	loc, err := jobPlace(root)
	if err != nil {
		slog.Warn("the broker charges this process's runs to the lease BAGGAGE names, not the one this checkout binds",
			attr.Component("broker"), attr.Error(err))
		return claim
	}
	lease, _ := job.ActingLease(loc, claim)
	return lease
}

// gateArgv reports whether args run the ci composition: `affected ci` or `run ci`. The
// target is read with the command's own flags bound, so a flag value such as `-o json`
// is never taken for it.
func gateArgv(args []string) bool {
	sub, subArgs := peekSub(args)
	var bind func(*flag.FlagSet)
	switch sub {
	case "run":
		bind = func(fs *flag.FlagSet) { bindRunFlags(fs, nil) }
	case "affected":
		bind = func(fs *flag.FlagSet) { flaggen.BindAffected(fs) }
	default:
		return false
	}
	raw, _, ok := splitTargetFromArgs(subArgs, bind)
	if !ok {
		return false
	}
	_, name := parseTarget(raw)
	t, err := types.ParseTarget(name)
	if err != nil {
		return false
	}
	return canonicalTarget(t.Name) == string(types.TargetCI)
}

// brokerUnits is `magus broker units [systemd|launchd]`: print the files a supervisor
// needs to run the broker, for the person to install. The default is launchd on macOS
// and systemd elsewhere.
func brokerUnits(ctx context.Context, args []string) error {
	rest, err := commandParse("broker units", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus broker units [systemd|launchd] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the units a supervisor needs to run the broker: a systemd socket and")
			fmt.Fprintln(os.Stderr, "service that socket-activate it, or a launchd agent that keeps it alive.")
			fmt.Fprintln(os.Stderr, "magus prints them; installing them is yours.")
		}
	})
	if err != nil {
		return err
	}
	supervisor := broker.SupervisorSystemd
	if runtime.GOOS == "darwin" {
		supervisor = broker.SupervisorLaunchd
	}
	switch len(rest) {
	case 0:
	case 1:
		supervisor = rest[0]
	default:
		return exit.Usagef("magus broker units: takes at most one supervisor (got %q)", rest)
	}
	facts, err := gatherUnitFacts()
	if err != nil {
		return fmt.Errorf("magus broker units: %w", err)
	}
	units, err := broker.RenderUnits(supervisor, facts)
	if err != nil {
		return exit.Usagef("magus broker units: %v", err)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		names := make([]string, len(units))
		for i, u := range units {
			names[i] = u.Path
		}
		return emitRecord(opts, units, names)
	}
	for i, u := range units {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("# %s\n%s", u.Path, u.Content)
	}
	var enable string
	switch supervisor {
	case broker.SupervisorSystemd:
		enable = "systemctl --user daemon-reload && systemctl --user enable --now magus-broker.socket"
	case broker.SupervisorLaunchd:
		enable = fmt.Sprintf("launchctl bootstrap gui/%d %s", os.Getuid(), units[0].Path)
	}
	slog.InfoContext(ctx, "write each file above to the path in its header, then:",
		attr.Notice(""), attr.Component("magus"), attr.Next(enable))
	return nil
}

func gatherUnitFacts() (broker.UnitFacts, error) {
	exe, err := os.Executable()
	if err != nil {
		return broker.UnitFacts{}, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return broker.UnitFacts{}, err
	}
	configDir, err := xdg.ConfigDir()
	if err != nil {
		return broker.UnitFacts{}, err
	}
	f := broker.UnitFacts{
		Exe:       exe,
		Socket:    strings.TrimPrefix(broker.DefaultAddr(), "unix://"),
		Log:       brokerLogPath(),
		Grace:     globalCfg.ShutdownGrace,
		Home:      home,
		ConfigDir: configDir,
	}
	for _, k := range []string{"XDG_RUNTIME_DIR", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			f.Env = append(f.Env, [2]string{k, v})
		}
	}
	return f, nil
}
