package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/egladman/magus/broker"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
)

// brokerReadyTimeout bounds how long a run waits for a broker it spawned to bind its
// socket. The broker loads nothing, so this is generous.
const brokerReadyTimeout = 10 * time.Second

func brokerCommand(ctx context.Context, args []string) error {
	if server.BrokerServes(args) {
		return brokerServe(ctx, args)
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		server.BrokerUsage()
		return nil
	case hint.BrokerStatus.Leaf():
		return brokerStatus(ctx, args[1:])
	case hint.BrokerStop.Leaf():
		return brokerStop(ctx, args[1:])
	case hint.BrokerUnits.Leaf():
		return brokerUnits(ctx, args[1:])
	default:
		server.BrokerUsage()
		return exit.Usagef("magus server broker: unknown target %q (want status, stop or units, or nothing to run one)", args[0])
	}
}

// brokerServe is `magus server broker`.
func brokerServe(ctx context.Context, args []string) error {
	var f *flaggen.ServerBrokerFlags
	rest, err := commandParse("server broker", args, func(fs *flag.FlagSet) {
		f = flaggen.BindServerBroker(fs)
		fs.Usage = server.BrokerUsage
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus server broker: unexpected argument %q (want status, stop or units, or flags only to run one)", rest[0])
	}
	if f.IdleExit < 0 {
		return exit.Usagef("magus server broker: --idle-exit %s is negative; 0 never exits", f.IdleExit)
	}
	return server.ServeBroker(ctx, server.BrokerRun{
		IdleExit:      f.IdleExit,
		Log:           f.Log,
		Config:        globalCfg,
		Version:       version,
		RedirectStdio: proc.RedirectStdio,
	})
}

func brokerStatus(ctx context.Context, args []string) error {
	rest, err := commandParse("server broker status", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server broker status [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The broker: its pid, capacity, every claim holding it and the services it")
			fmt.Fprintln(os.Stderr, "hosts. Exits non-zero when none is running, so a script can chain on it.")
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus server broker status: takes no arguments (got %q)", rest[0])
	}
	if warn := broker.OtherBrokersWarning(broker.OtherBrokers(ctx, proc.SockDir())); warn != "" {
		slog.WarnContext(ctx, warn, attr.Notice(""))
	}
	st, qerr := server.QueryBroker(ctx)
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
	server.PrintBrokerRows(os.Stdout, st, globalCfg.Broker, time.Now())
	return nil
}

func brokerStop(ctx context.Context, args []string) error {
	var f *flaggen.ServerBrokerStopFlags
	if _, err := commandParse("server broker stop", args, func(fs *flag.FlagSet) {
		f = flaggen.BindServerBrokerStop(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server broker stop [--services]")
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
		pid, err := server.StopUnanswering(ctx, f.Services, err, brokerDrainWait)
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
	sctx, cancel := context.WithTimeout(ctx, proc.StopTimeout)
	defer cancel()
	if err := c.Shutdown(sctx); errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("broker stop: broker (pid %d) did not stop within %s", st.PID, proc.StopTimeout)
	} else if err != nil {
		return fmt.Errorf("broker stop: %w", err)
	}
	if opts.Format != outputText {
		return emitRecord(opts, brokerStopOutput{Stopped: true, PID: st.PID}, []string{strconv.Itoa(st.PID)})
	}
	slog.InfoContext(ctx, fmt.Sprintf("stopped broker (pid %d)", st.PID), attr.Notice(""), attr.Component("magus"))
	return nil
}

// brokerDrainWait is how long a signalled broker gets per SIGTERM. A var so a
// test can shorten it.
var brokerDrainWait = proc.StopTimeout

type brokerStopOutput struct {
	Stopped  bool `json:"stopped"`
	PID      int  `json:"pid"`
	Services int  `json:"services"`
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
	pid, logPath, err := server.SpawnBroker()
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
		pid, server.IdleText(broker.DefaultIdleExit), hint.BrokerStatus)
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
			slog.String("log", server.BrokerLogPath()),
			slog.Int("idle_exit_s", int(broker.DefaultIdleExit/time.Second)))
	}
	slog.LogAttrs(ctx, slog.LevelInfo, startedBrokerMessage(pid), attrs...)
}

var upstreamSourceSkill = agent.MustSkill("magus-upstream-source")

// crashHint is what the broker writes after Go's trace when this process crashes: that
// the defect is magus's, and where this build's source can be read.
func crashHint(v buildinfo.VersionOutput) string {
	head := fmt.Sprintf("magus %s crashed. This is a defect in magus, not in your workspace.\n", v.Version)
	if v.Dirty || v.Commit == buildinfo.UnknownVersion {
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
		broker.WithCrashHint(crashHint(buildinfo.NewVersionOutput(selfBuild(), builtBy))),
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
		processBroker = newBrokerClient(true, broker.WithLease(processLease(commands.ExtractRootFlag(os.Args[1:]))), broker.WithGate(gateArgv(os.Args[1:])))
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

// brokerUnits is `magus server broker units [systemd|launchd]`. It prints
// the files a supervisor needs to run the broker, for a person to install.
// The default is launchd on macOS and systemd elsewhere.
func brokerUnits(ctx context.Context, args []string) error {
	rest, err := commandParse("server broker units", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server broker units [systemd|launchd] [flags]")
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
		return exit.Usagef("magus server broker units: takes at most one supervisor (got %q)", rest)
	}
	facts, err := server.GatherUnitFacts(globalCfg.ShutdownGrace)
	if err != nil {
		return fmt.Errorf("magus server broker units: %w", err)
	}
	units, err := broker.RenderUnits(supervisor, facts)
	if err != nil {
		return exit.Usagef("magus server broker units: %v", err)
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
