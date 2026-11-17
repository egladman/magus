package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/internal/sys/pid"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types/daemon"
)

// BrokerServes reports whether a `broker` argv runs a broker in this process: no target,
// only flags, and no request for help.
func BrokerServes(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "-h", "-help", "--help":
		return false
	}
	return strings.HasPrefix(args[0], "-")
}

func BrokerUsage() {
	fmt.Fprintln(os.Stderr, "usage: magus server broker [status|stop|units] [flags]")
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
	fmt.Fprintln(os.Stderr, "Log, when a run started it: "+BrokerLogPath())
}

// BrokerRun is what `magus server broker` hands ServeBroker once its flags are read.
type BrokerRun struct {
	IdleExit time.Duration
	Log      string
	Config   config.Config
	Version  string
	// RedirectStdio points stdout and stderr at path, for --log and for SIGHUP.
	RedirectStdio func(path string) error
}

// ServeBroker serves this host's capacity until idle, stopped or signalled. Losing the
// bind to a live broker ends a start race, so it exits 0. A supervisor's socket is served,
// not bound.
func ServeBroker(ctx context.Context, o BrokerRun) error {
	var err error
	if o.Log != "" {
		if err := o.RedirectStdio(o.Log); err != nil {
			return fmt.Errorf("magus server broker: %w", err)
		}
	}

	addr := broker.DefaultAddr()
	ln, err := broker.Activated(addr)
	if err != nil {
		return fmt.Errorf("magus server broker: %w", err)
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
			return fmt.Errorf("magus server broker: %w", err)
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
		slog.With(attr.Component("broker")).WarnContext(ctx, "service journal unavailable; crash reaping disabled", attr.Error(jerr))
	} else if res := journal.Sweep(ctx); res.Reaped > 0 || res.Unreapable > 0 {
		slog.With(attr.Component("broker")).InfoContext(ctx, "reaped orphaned services a previous broker left",
			slog.Int("reaped", res.Reaped), slog.Int("left_running", res.Unreapable))
	}
	reg := service.New(service.ExecRunner{}, defaultServiceIdle, service.WithJournal(journal))

	// Sized from HOST facts, never from the workspace that happened to start it: the
	// broker is started by whichever run got there first. Memory comes from what this
	// process may commit, so a broker inside a memory-limited container budgets the
	// container; slots come from the cores, the ceiling ClampConcurrency holds every run
	// to. The profile decides memory's reservation.
	usable := mem.UsableBytes(ctx)
	memMB := mem.BudgetMB(usable, o.Config.ConcurrencyProfile)
	reserve := mem.Reserve(usable, o.Config.ConcurrencyProfile)
	slots := cache.MachineCeiling()
	exits := "never exits for idleness"
	if o.IdleExit > 0 {
		exits = fmt.Sprintf("exits after %s holding nothing", IdleText(o.IdleExit))
	}
	slog.InfoContext(ctx, fmt.Sprintf("broker (pid %d) %s %s: %d slots, %s; %s",
		os.Getpid(), from, addr, slots, budget.FormatMB(memMB), exits), attr.Notice(""), attr.Component("magus"))

	ctx, stopNow := context.WithCancel(ctx)
	defer stopNow()
	drain := make(chan struct{})
	release := proc.Lifecycle{
		Hangup:         func() { reopenBrokerLog(ctx, o.Log, o.RedirectStdio) },
		Stop:           func() { close(drain) },
		StopNow:        stopNow,
		InterruptIsNow: true,
	}.Watch(ctx)
	defer release()

	err = broker.Serve(ctx, ln,
		broker.WithCapacity(memMB, slots),
		broker.WithMemoryReserve(reserve),
		broker.WithServices(serviceHost{reg: reg}),
		broker.WithIdleExit(o.IdleExit),
		broker.WithLogger(slog.Default()),
		broker.WithVersion(o.Version),
		broker.WithDrain(drain, o.Config.ShutdownGrace),
		broker.WithCrashDir(crashReportDir()),
		broker.WithAdmissionLog(file.Log[budget.Admission]{Path: AdmissionLogPath(), Keep: admissionsKeep}),
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
func reopenBrokerLog(ctx context.Context, path string, redirectStdio func(string) error) {
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

// StopUnanswering stops whatever holds the broker socket when this build
// cannot talk to it, as another build's broker refuses to. The kernel names
// the holder.
func StopUnanswering(ctx context.Context, services bool, dialErr error, drainWait time.Duration) (int, error) {
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
		return 0, fmt.Errorf("broker stop --services: broker (pid %d) does not answer this build, which cannot ask it to stop only its services, so `magus server broker stop` stops it whole: %w", pid, dialErr)
	}
	command := processCommand(ctx, pid)
	if !slices.Contains(strings.Fields(command), "broker") {
		return 0, fmt.Errorf("broker stop: pid %d (%s) holds %s and is no broker, so magus signalled nothing, and under systemd it is the socket unit, so run `systemctl --user stop magus-broker.socket magus-broker.service`: %w",
			pid, cmp.Or(command, "command unreadable"), sock, dialErr)
	}
	return pid, signalBrokerStop(ctx, pid, dialErr, drainWait)
}

// unknownBrokerError says what to run when the kernel will not name the holder.
func unknownBrokerError(sock string, err, dialErr error) error {
	return fmt.Errorf("broker stop: something holds %s and does not answer this build, and its pid is unreadable here, so run `lsof -U`, take the pid on the line naming that socket, then `kill -TERM <pid>`: %w",
		sock, fmt.Errorf("%w: %w", err, dialErr))
}

// signalBrokerStop sends pid SIGTERM, which every build's broker handles: the
// first drains it and a second stops it now. It returns once pid is gone.
func signalBrokerStop(ctx context.Context, pid int, dialErr error, drainWait time.Duration) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("broker stop: find broker (pid %d): %w", pid, err)
	}
	slog.InfoContext(ctx, fmt.Sprintf("broker (pid %d) does not answer this build; sending it SIGTERM and waiting up to %s for it to drain",
		pid, drainWait), attr.Error(dialErr), attr.Notice(""), attr.Component("magus"))
	for range 2 {
		if err := p.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("broker stop: signal broker (pid %d): %w", pid, err)
		}
		if waitProcessGone(ctx, pid, drainWait) {
			slog.InfoContext(ctx, fmt.Sprintf("stopped broker (pid %d)", pid), attr.Notice(""), attr.Component("magus"))
			return nil
		}
	}
	return fmt.Errorf("broker stop: broker (pid %d) outlived two SIGTERMs, %s apart, so run `kill -KILL %d`", pid, drainWait, pid)
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

// QueryBroker reads the broker's status, bounded so a wedged broker cannot hang a
// status command.
func QueryBroker(ctx context.Context) (daemon.Broker, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return broker.QueryStatus(ctx, broker.DefaultAddr())
}

// IdleText is an idle window as a person reads it.
func IdleText(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	}
	return d.String()
}

// BrokerLogPath is where a broker a run started writes: the XDG state directory, which
// survives logout, where the runtime directory holding its socket does not.
func BrokerLogPath() string { return StateLogPath("broker.log") }

// AdmissionLogPath is the store of the admissions the broker recorded, one
// budget.Admission per line.
func AdmissionLogPath() string { return StateLogPath("admissions.jsonl") }

// admissionsKeep is the admissions a compaction of the store leaves.
const admissionsKeep = 5000

// LogPath is BrokerLogPath's counterpart for a server `server start` detached.
func LogPath() string { return StateLogPath("server.log") }

// crashReportDir is where the broker saves the crash report of a process it watched.
func crashReportDir() string { return StateLogPath("crashes") }

func StateLogPath(name string) string {
	dir, err := xdg.StateDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "magus", name)
}

// GatherUnitFacts is what the supervisor units name: this binary, the socket, the log and
// the shutdown grace.
func GatherUnitFacts(grace time.Duration) (broker.UnitFacts, error) {
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
		Log:       BrokerLogPath(),
		Grace:     grace,
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

// defaultServiceIdle is how long the broker keeps a shared service warm after its
// last dependent releases, unless the service overrides it via Service.Idle. Shorter
// than the workspace idle TTL: a shared service is cheap to restart but costly to
// leave running all day.
const defaultServiceIdle = 30 * time.Minute

// serviceHost adapts a service.Registry to broker.serviceHost: the client only needs
// to know the service is up and whether the broker owns it, so the Handle is reduced
// to that. Release maps straight through.
type serviceHost struct{ reg *service.Registry }

func (h serviceHost) Snapshot() []daemon.Service { return serviceStatuses(h.reg) }

func (h serviceHost) Acquire(ctx context.Context, key string, spec broker.ServiceSpec) (bool, error) {
	handle, err := h.reg.Acquire(ctx, key, spec.Service())
	return err == nil && handle != service.Adopted, err
}

func (h serviceHost) Release(key string) { h.reg.Release(key) }

// StopAll stops every hosted service and returns how many were stopped, leaving the
// broker running (the registry stays usable). context.Background: the RPC handler
// that calls this has no per-request ctx to thread through today, and unlike broker
// teardown there is no already-cancelled parent ctx to work around here; Shutdown's
// own per-victim bounds (readiness timeout, stop grace) still apply regardless.
func (h serviceHost) StopAll() int {
	n := h.reg.Held()
	h.reg.Shutdown(context.Background())
	return n
}

// serviceStatuses adapts the registry's introspection snapshot (service.ServiceStatus)
// to the status-wire shape (daemon.Service) the console and the Status RPC serve.
func serviceStatuses(reg *service.Registry) []daemon.Service {
	snap := reg.Snapshot()
	out := make([]daemon.Service, 0, len(snap))
	for _, s := range snap {
		out = append(out, daemon.Service{
			ID:         s.ID,
			Label:      s.Label,
			Command:    s.Command,
			Ports:      s.Ports,
			State:      daemon.ServiceState(s.State),
			Dependents: s.Dependents,
			StartedAt:  s.StartedAt,
		})
	}
	return out
}
