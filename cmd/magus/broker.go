package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/types"
)

// brokerReadyTimeout bounds how long a run waits for a broker it spawned to bind its
// socket. The broker loads nothing, so this is generous.
const brokerReadyTimeout = 10 * time.Second

func brokerCmd(ctx context.Context, args []string) error {
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
		return brokerUnits(args[1:])
	default:
		brokerUsage()
		return usagef("magus broker: unknown target %q (want status, stop or units, or nothing to run one)", args[0])
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
	fmt.Fprintln(os.Stderr, "services every magus on it shares. A run starts one when none answers; it")
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
	var f *gen.BrokerFlags
	rest, err := cmdParse("broker", args, func(fs *flag.FlagSet) {
		f = gen.BindBroker(fs)
		fs.Usage = brokerUsage
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus broker: unexpected argument %q (want status, stop or units, or flags only to run one)", rest[0])
	}
	if f.IdleExit < 0 {
		return usagef("magus broker: --idle-exit %s is negative; 0 never exits", f.IdleExit)
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
			fmt.Fprintf(os.Stderr, "magus: a broker is already serving %s\n", addr)
			return nil
		}
		if err != nil {
			return fmt.Errorf("magus broker: %w", err)
		}
	}

	// Services outlive the runs that asked for them, so a journal records each one's
	// stop command; a broker that crashed left orphans, and they are swept before
	// anything new is hosted.
	journal, jerr := service.NewJournal(filepath.Join(proc.SockDir(), "services"))
	if jerr != nil {
		slog.Warn("broker: service journal unavailable; crash reaping disabled", slog.String("error", jerr.Error()))
	} else if res := journal.Sweep(ctx); res.Reaped > 0 || res.Unreapable > 0 {
		slog.Info("broker: reaped orphaned services a previous broker left",
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
		exits = "exits after " + idleText(f.IdleExit) + " holding nothing"
	}
	fmt.Fprintf(os.Stderr, "magus: broker (pid %d) %s %s: %d slots, %s; %s\n",
		os.Getpid(), from, addr, slots, cache.FormatMB(memMB), exits)

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
		slog.InfoContext(ctx, "broker: SIGHUP reopens the --log file, and this broker logs to stderr")
		return
	}
	if err := redirectStdio(path); err != nil {
		slog.ErrorContext(ctx, "broker: could not reopen its log; still writing to the old one", slog.String("error", err.Error()))
		return
	}
	slog.InfoContext(ctx, "broker: reopened its log", slog.String("log", path))
}

func brokerStatus(ctx context.Context, args []string) error {
	rest, err := cmdParse("broker status", args, func(fs *flag.FlagSet) {
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
		return usagef("magus broker status: takes no arguments (got %q)", rest[0])
	}
	st, qerr := queryBroker(ctx)
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		if qerr != nil {
			return errSilent{exitCode: 1}
		}
		return emitFormatted(opts, st)
	}
	if qerr != nil {
		why := "a run starts one"
		if globalCfg.Broker.Resolved() == types.BrokerOff {
			why = "runs here never ask one"
		}
		fmt.Fprintf(os.Stderr, "no broker is running (broker: %s); %s\n", globalCfg.Broker, why)
		return errSilent{exitCode: 1}
	}
	printBrokerRows(os.Stdout, st, globalCfg.Broker, time.Now())
	return nil
}

func brokerStop(ctx context.Context, args []string) error {
	var f *gen.BrokerStopFlags
	if _, err := cmdParse("broker stop", args, func(fs *flag.FlagSet) {
		f = gen.BindBrokerStop(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus broker stop [--services]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Stop the broker. Every claim on this host goes with it; runs already going")
			fmt.Fprintln(os.Stderr, "keep going and re-assert their claims on the next broker. With --services,")
			fmt.Fprintln(os.Stderr, "stop only the services it hosts and leave it running.")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted):")
			fs.PrintDefaults()
		}
	}); err != nil {
		return err
	}
	c, err := broker.Dial(ctx, broker.DefaultAddr())
	if err != nil {
		fmt.Fprintln(os.Stderr, "magus: no broker is running")
		return errSilent{exitCode: 1}
	}
	defer func() { _ = c.Close() }()
	if f.Services {
		n, err := c.StopServices(ctx)
		if err != nil {
			return fmt.Errorf("broker stop: %w", err)
		}
		fmt.Fprintf(os.Stderr, "magus: stopped %d hosted service(s); the broker is still running\n", n)
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
	fmt.Fprintf(os.Stderr, "magus: stopped broker (pid %d)\n", st.PID)
	return nil
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

func stateLogPath(name string) string {
	dir, err := config.UserStateDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "magus", name)
}

// spawnBroker is the seam a test replaces to observe that a broker WOULD be started
// without one being started.
var spawnBroker = func() (pid int, logPath string, err error) {
	logPath = brokerLogPath()
	pid, err = spawnDetached([]string{"broker", "--" + gen.FlagBrokerLog, logPath}, logPath)
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
		slog.Debug("magus: could not start a broker", slog.String("error", err.Error()))
		return 0
	}
	deadline := time.Now().Add(brokerReadyTimeout)
	for !broker.Live(ctx, addr) {
		if time.Now().After(deadline) || ctx.Err() != nil {
			slog.Debug("magus: the broker a run started did not come up", slog.Int("pid", pid), slog.String("log", logPath))
			return 0
		}
		time.Sleep(20 * time.Millisecond)
	}
	return pid
}

// announceBroker tells the person that their run left a process behind: which one, what
// it is for, when it goes away, and that it listens on nothing but its socket. A
// background process nobody mentions is the surprise this line exists to remove, so it
// is a notice on stderr rather than a log record a default level filters out.
//
// quiet (-q or -s) drops it. Any structured -o gets a run.notice record instead of prose,
// so a caller parsing stderr meets one record shape.
func announceBroker(w io.Writer, pid int, output string, quiet bool) {
	if pid == 0 || quiet {
		return
	}
	msg := fmt.Sprintf("started a broker (pid %d) to hold this host's capacity; it opens no network listener and exits after %s holding nothing (`%s` lists it)",
		pid, idleText(broker.DefaultIdleExit), hint.BrokerStatus)
	if output != "" && output != string(outputText) {
		_ = report.NewLineEncoder(w).Encode(report.Notice{
			Level:   slog.LevelInfo,
			Message: msg,
			Attrs: map[string]any{
				"pid":         pid,
				"log":         brokerLogPath(),
				"idle_exit_s": int(broker.DefaultIdleExit / time.Second),
			},
		})
		return
	}
	fmt.Fprintf(w, "magus: %s\n", msg)
}

// newBrokerClient is the broker client a CLI process opens its workspaces with. It
// starts a broker when a step or a service finds none, which is how a long-lived
// process (`magus mcp`, the server) gets one back after the last idled out. announce
// false keeps a detached server's notice out of its log.
func newBrokerClient(announce bool) *broker.Client {
	return broker.NewClient(broker.DefaultAddr(),
		broker.WithIdentity(os.Args, version),
		broker.WithStart(func(ctx context.Context) bool {
			pid := ensureBroker(ctx)
			if announce {
				announceBroker(os.Stderr, pid, global.output, global.quiet || global.silent)
			}
			return broker.Live(ctx, broker.DefaultAddr())
		}))
}

var (
	processBrokerOnce sync.Once
	processBroker     *broker.Client
)

// processBrokerClient is this process's one broker client: one connection per process
// is what lets the broker release everything the process held when it exits.
func processBrokerClient() *broker.Client {
	processBrokerOnce.Do(func() { processBroker = newBrokerClient(true) })
	return processBroker
}
