package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/interactive/tty"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/internal/proc"
	procrun "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/service/console"
	sysPID "github.com/egladman/magus/internal/sys/pid"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

func serverCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		serverUsage()
		return usagef("magus server: target required (want start, stop, status, or reload)")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		serverUsage()
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case hint.ServerStart.Leaf():
		return serverStart(ctx, rest)
	case "-foreground", "--foreground":
		// The process `server start` detaches: argv names what it is, and no `start` in
		// ps reads like a launcher that hung.
		return serverStart(ctx, args)
	case hint.ServerStop.Leaf():
		return serverStop(ctx, rest)
	case hint.ServerStatus.Leaf():
		return serverStatus(ctx, rest)
	case hint.ServerReload.Leaf():
		return serverReload(ctx, rest)
	case job.NameRotateActivities:
		return serverRotateActivities(ctx, root, rest)
	case job.NameRotateLogs:
		return serverRotateLogs(ctx, root, rest)
	case job.NamePrunePreserved:
		return serverPrunePreserved(ctx, root, rest)
	case job.NameCheckReview:
		return serverCheckReview(ctx, root, rest)
	case job.NameCheckDrift:
		return serverCheckDrift(ctx, root, rest)
	case job.NameRegenerateOwed:
		return serverRegenerateOwed(ctx, root, rest)
	default:
		return usagef("magus server: unknown target %q (want start, stop, status, or reload)", sub)
	}
}

func serverUsage() {
	fmt.Fprintln(os.Stderr, "usage: magus server <start|stop|status|reload> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Targets:")
	fmt.Fprintln(os.Stderr, "  start   start the server: MCP, the console, APIs, background jobs")
	fmt.Fprintln(os.Stderr, "  stop    send a graceful shutdown request to the running server")
	fmt.Fprintln(os.Stderr, "  status  is the server up, and where do I reach it")
	fmt.Fprintln(os.Stderr, "  reload  re-read configuration without restarting: drop the server's open workspaces")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "`%s` is this workspace and this host: the broker and what holds capacity,\n", hint.Status)
	fmt.Fprintln(os.Stderr, "the server, and what the cache and config are.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The socket address is taken from --daemon-address, MAGUS_DAEMON_ADDRESS,")
	fmt.Fprintln(os.Stderr, "or daemon.address in magus.yaml. When none is set, `server start` uses:")
	fmt.Fprintln(os.Stderr, "  "+proc.ServerDefaultAddr())
}

// serverStatus answers one question: is my server up, and where do I reach it.
//
// Deliberately NOT a second `magus status`. That verb is this workspace and this host,
// and it embeds the server rows inside a broader view; this one is the server and its
// addresses, which is what somebody asks when nothing is answering. Both print the
// server rows through printServerRows, so the two can never disagree.
//
// It exits non-zero with no server, matching `server stop`, so a script can chain on it.
func serverStatus(ctx context.Context, args []string) error {
	var socket string
	rest, err := cmdParse("server status", args, func(fs *flag.FlagSet) {
		fs.StringVar(&socket, "socket", "", "Server socket (default: config / MAGUS_DAEMON_ADDRESS / server.sock)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server status [--socket <addr>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The server: whether it is up and where you reach it. Its pid and version, how")
			fmt.Fprintln(os.Stderr, "long it has been observing, the socket and listeners, the MCP url, the console")
			fmt.Fprintln(os.Stderr, "url, what it is running, and the workspaces it has loaded.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "`%s` is the other one: this workspace and this host, the broker and what\n", hint.Status)
			fmt.Fprintln(os.Stderr, "holds capacity, and what the cache and config are.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Exits non-zero when no server is running, so a script can chain on it.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus server status: takes no arguments (got %q)", rest[0])
	}

	report := buildStatusSnapshot(ctx, resolveServerAddr(socket), false)
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		if err := emitFormatted(opts, report); err != nil {
			return err
		}
		if report.Server == nil {
			return errSilent{exitCode: 1}
		}
		return nil
	}

	if report.Server == nil || report.Pool == nil {
		// To stderr, like every other thing `magus server` says when it cannot do what it
		// was asked: stdout carries the report, and a caller redirecting it wants an empty
		// file rather than a sentence about why there is nothing in it.
		fmt.Fprintln(os.Stderr, "no server is running")
		if report.PoolError != "" {
			fmt.Fprintln(os.Stderr, report.PoolError)
		}
		fmt.Fprintf(os.Stderr, "start one with `%s`. `%s` still reports this workspace and this host without a server.\n",
			hint.ServerStart, hint.Status)
		return errSilent{exitCode: 1}
	}
	printServerRows(os.Stdout, report.Server, time.Now())
	printPoolSummary(os.Stdout, report.Pool, "pool")
	if skew := serverVersionSkew(report.Pool); skew != "" {
		fmt.Print(skew)
	}
	if !report.ObservingSince.IsZero() {
		fmt.Printf("observing since: %s\n", report.ObservingSince.Format(time.RFC3339))
	}
	printMCPEndpointStatus(os.Stdout, report.MCPEndpoint)
	printConsoleStatus(os.Stdout, report.Console)
	if len(report.Pool.Workspaces) > 0 {
		fmt.Printf("\nworkspaces (%d)\n", len(report.Pool.Workspaces))
		for _, ws := range report.Pool.Workspaces {
			if !ws.Loaded() {
				fmt.Printf("  %s  (%s)\n", ws.Root, ws.State)
				continue
			}
			fmt.Printf("  %s  (idle %s)\n", ws.Root, time.Since(ws.LastAccess).Round(time.Second))
		}
	}
	return nil
}

// serverReadyTimeout bounds how long the backgrounding parent waits for the detached child
// to start accepting on the socket before it reports the start as failed.
const serverReadyTimeout = 60 * time.Second

func serverStart(ctx context.Context, args []string) error {
	ctx = trail.ContextWithEntryPoint(ctx, types.EntryPointDaemon)
	var sf *gen.ServerStartFlags
	_, err := cmdParse("server start", args, func(fs *flag.FlagSet) {
		sf = gen.BindServerStart(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server start [--foreground] [flags]")
			fmt.Fprintln(os.Stderr, "\nStart the server: MCP and the console over HTTP, the APIs, background jobs and")
			fmt.Fprintln(os.Stderr, "the warm graph and symbol watch. By default it auto-backgrounds: this command")
			fmt.Fprintln(os.Stderr, "detaches `magus server --foreground`, waits until it is accepting connections,")
			fmt.Fprintln(os.Stderr, "prints its pid, and returns 0. Starting when a server is already running is a")
			fmt.Fprintln(os.Stderr, "no-op that also returns 0.")
			fmt.Fprintln(os.Stderr, "\nWith --foreground the server runs in this process and blocks until stopped")
			fmt.Fprintln(os.Stderr, "(SIGINT / SIGTERM or `"+hint.ServerStop.String()+"`), logging to stderr. Use it")
			fmt.Fprintln(os.Stderr, "under a process supervisor (systemd --user) or when debugging.")
			fmt.Fprintln(os.Stderr, "\nSocket address: --daemon-address flag > MAGUS_DAEMON_ADDRESS env >")
			fmt.Fprintln(os.Stderr, "daemon.address in magus.yaml > default ("+proc.ServerDefaultAddr()+")")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	_ = sf.Foreground // the parent/child split is decided in startServerBackground; here we always run the server

	addr := os.Getenv("MAGUS_DAEMON_SOCKET")
	if addr == "" {
		return fmt.Errorf("magus server: socket not available (no workspace found, or socket bind failed)")
	}
	fmt.Fprintf(os.Stderr, "magus: server (pid %d) listening on %s\n", os.Getpid(), addr)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	startServerSurface(ctx, cancel)

	// Block until a signal cancels ctx OR an RPC `server stop` closes the proc server. The
	// second case is the load-bearing one: the shutdown handler cancels only the listener's
	// own context (a sibling of this ctx), so without observing srv.Done() the server would
	// keep running after its socket was already torn down.
	var serverDone <-chan struct{}
	if procServer != nil {
		serverDone = procServer.Done()
	}
	select {
	case <-ctx.Done():
	case <-serverDone:
	}
	return nil
}

// startServerSurface opens what the server serves beyond its socket: the VCS hooks that
// poke it, the graph and symbol watch, MCP and the console over HTTP on mcp.address, and
// the maintenance scheduler. A var so a test can observe it without binding a port.
var startServerSurface = func(ctx context.Context, cancel context.CancelFunc) {
	// The socket is unusable by a person and the console is the thing they open, so it is
	// printed here rather than left in the log. A console that is not mounted says so: a
	// silent absence is what sends somebody reading the server's source.
	if u := consoleRootURL(); u != "" {
		fmt.Fprintf(os.Stderr, "magus: console at %s (it asks for a token; `%s` mints one)\n", u, hint.ConfigConsoleTokenCreate.With("--expires", console.LinkTokenExpires()))
	} else {
		fmt.Fprintln(os.Stderr, "magus: no console is mounted (none is built, or console.enabled is false)")
	}
	fmt.Fprintf(os.Stderr, "magus: send SIGINT / SIGTERM or run `%s` to shut down\n", hint.ServerStop)

	installRefreshHooks(ctx)
	installDriftHooks(ctx)
	installRegenHooks(ctx)

	// serverProvider was built by startServer (which runs before this command handler)
	// so the bridge Magus shares the same OTel instruments the per-workspace builds record
	// into.
	startBridge(ctx, cancel, serverProvider)

	// Background maintenance: rotate the trail/run-logs and reconcile the graph on their
	// configured intervals, idle-gated. Socket and trail base are late-bound (set during
	// startup), so the scheduler reads them per tick.
	maintenance.Start(ctx, maintenance.Options{
		Schedule: globalCfg.Daemon.Maintenance,
		Socket:   func() string { return os.Getenv("MAGUS_DAEMON_SOCKET") },
		Trail:    func() string { return serverTrailBase },
		Version:  version,
	})
}

// startServerBackground implements the default auto-backgrounding of `server start`. It
// runs in the launching process before the server is built. It returns done==true when it
// fully handled the request (the caller returns exitCode without building a server): one
// was already running (idempotent no-op), a detached child was spawned and became ready,
// or spawning failed. It returns done==false for a foreground server: an explicit
// `server start --foreground`, or the detached `server --foreground` child.
func startServerBackground(ctx context.Context, cfg config.Config, subArgs []string) (exitCode int, done bool) {
	if wantsForeground(subArgs) {
		return 0, false
	}

	addr := cfg.Daemon.Address // startup defaulted this to server.sock
	// Idempotent start: a server already accepting on the socket means there is nothing to do.
	if proc.SocketLive(ctx, addr) {
		if st, err := proc.QueryStatus(ctx, addr); err == nil && st.ParentPID != 0 {
			fmt.Fprintf(os.Stderr, "magus: server already running (pid %d) on %s%s\n",
				st.ParentPID, addr, servingSuffix(st))
		} else {
			fmt.Fprintf(os.Stderr, "magus: server already running on %s\n", addr)
		}
		return 0, true
	}

	logPath := serverLogPath()
	pid, err := spawnDetached(serverChildArgs(os.Args[1:]), logPath)
	if err != nil {
		slog.ErrorContext(ctx, "server start: could not background the server", slog.String("error", err.Error()))
		return 1, true
	}
	if err := waitServerReady(ctx, addr, serverReadyTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "magus: server (pid %d) did not become ready within %s; see %s\n", pid, serverReadyTimeout, logPath)
		return 1, true
	}
	fmt.Fprintf(os.Stderr, "magus: server started (pid %d) on %s; logs at %s\n", pid, addr, logPath)
	return 0, true
}

// serverChildArgs turns a `server start` argv into the detached child's: the `start` verb
// becomes --foreground, and every flag the person passed (a --daemon-address, say) rides
// along so the child binds where the parent waits.
func serverChildArgs(argv []string) []string {
	out := make([]string, 0, len(argv)+1)
	replaced := false
	prev := ""
	for _, a := range argv {
		if !replaced && a == hint.ServerStart.Leaf() && prev == "server" {
			out = append(out, "--foreground")
			replaced = true
			prev = a
			continue
		}
		out = append(out, a)
		prev = a
	}
	return out
}

// servingSuffix names the workspaces a running server has loaded, or "" when it has none yet.
//
// One socket per user serves every workspace, so "already running" answered the question the
// caller asked and not the one they meant. Starting the server from a second worktree returns 0
// with nothing loaded from THIS tree, and the console then shows the tree it was started in,
// which reads as the command having worked. The roots are already on the status wire; the message
// simply never said them.
//
// The workspace this call was made from is not marked, deliberately: a server loads a workspace
// lazily on first use, so "not listed" means "not loaded yet" far more often than it means
// "wrong server", and flagging it would raise an alarm about the ordinary case.
func servingSuffix(st *proc.StatusReply) string {
	if st == nil || len(st.Workspaces) == 0 {
		return ""
	}
	roots := make([]string, 0, len(st.Workspaces))
	for _, w := range st.Workspaces {
		roots = append(roots, w.Root)
	}
	slices.Sort(roots)
	return ", serving " + strings.Join(roots, ", ")
}

// detachedChildEnv returns this process's environment with the variables a detached broker
// or server must not inherit removed.
//
// MAGUS_DAEMON_SOCKET: a child inheriting it believes it is already adopted, binds no
// socket, and reports the parent's, leaving a server `server stop` cannot find.
//
// The invocation ancestry and recursion depth, because A BACKGROUND PROCESS DESCENDS FROM
// NOBODY: the same rule submitJob already applies to a job's context. A run starts the
// broker, and a command may start the server, so without this the child's environment
// permanently records that one run's ancestry, and every workspace the server serves would
// read those refs as its own: claims belonging to an invocation that ended hours ago
// would be excused from the budget, and a run with no ancestry of its own would be judged
// a nested magus that had lost it.
func detachedChildEnv() []string {
	drop := []string{"MAGUS_DAEMON_SOCKET=", procrun.AncestorsEnvVar + "=", "MAGUS_LEVEL="}
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if slices.ContainsFunc(drop, func(p string) bool { return strings.HasPrefix(kv, p) }) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// detachedCmd builds the re-exec command with Path set to exe verbatim, skipping
// exec.Command's resolution. exe is the running binary's own path, so PATH and PATHEXT have
// no say in which binary it is. On Windows their say is fatal: given an absolute path with
// no extension, exec.Command demands a PATHEXT sibling on disk, and setup-magus installs an
// extensionless magus, so the binary could not re-exec itself ("executable file not found
// in %PATH%") and no broker ever started.
func detachedCmd(exe string, args []string) *exec.Cmd {
	return &exec.Cmd{Path: exe, Args: append([]string{exe}, args...)}
}

// spawnDetached re-execs this binary with args, detached, with its stdio appended to
// logPath, and returns its pid. The child is fully detached (its own session on unix) and
// Release()d so this process never waits on it. Its argv is its role: `broker`, or
// `server --foreground`.
func spawnDetached(args []string, logPath string) (pid int, err error) {
	exe, err := os.Executable()
	if err != nil {
		// argv[0] may be a bare name, which detachedCmd will not search for; resolve it the
		// way exec.Command used to.
		exe = os.Args[0]
		if lp, lerr := exec.LookPath(exe); lerr == nil {
			exe = lp
		}
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return 0, fmt.Errorf("create log directory for %s: %w", logPath, err)
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open log %s: %w", logPath, err)
	}
	defer func() { _ = logf.Close() }()

	cmd := detachedCmd(exe, args)
	cmd.Env = detachedChildEnv()
	cmd.Stdin = nil
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = detachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", strings.Join(args, " "), err)
	}
	pid = cmd.Process.Pid
	_ = cmd.Process.Release() // detach: the child outlives us, so never wait/reap it
	return pid, nil
}

// consoleReadyTimeout bounds the wait for a freshly spawned server to serve the console.
// It covers process start, socket bind, and bridge mount together, so it is longer than
// the socket alone would need.
const consoleReadyTimeout = 20 * time.Second

// ensureConsoleServer brings the server up when a command needs the console.
//
// The console IS the server's own surface, so `graph export --follow` is a plain request
// for a server and starting one is doing what was asked rather than a side effect.
// Commands that merely run FASTER with a server never call this, which is why an ordinary
// build (and therefore CI) never starts one.
//
// A server already up whose console is not serving is NOT restarted: a second one cannot
// fix a bridge disabled by config or bound elsewhere, and it would leave two running where
// the user asked for none.
//
// root names the workspace the child will serve. One socket per user serves every
// workspace, so a server started from here is authoritative for whoever connects next;
// saying which tree it came up in is what stops it reading as "the server", the same
// reason servingSuffix exists on the `server start` path.
func ensureConsoleServer(ctx context.Context, addr, root string) error {
	probe := func() error {
		pctx, cancel := context.WithTimeout(ctx, probeLiveBridgeTimeout)
		defer cancel()
		return probeLiveBridge(pctx, addr)
	}
	if err := probe(); err == nil {
		return nil
	}
	if sock := resolveServerAddr(""); proc.SocketLive(ctx, sock) {
		return fmt.Errorf("the server is running on %s but its console is not serving at %s; check console.enabled and mcp.address", sock, addr)
	}

	fmt.Fprintf(os.Stderr, "magus: starting the server to serve the console, from %s.\n", root)
	logPath := serverLogPath()
	pid, err := spawnDetached([]string{"server", "--foreground"}, logPath)
	if err != nil {
		return fmt.Errorf("could not start the server: %w", err)
	}
	// A serving console proves the socket bound AND the bridge mounted, so waiting on the
	// socket first would add a failure mode without adding information.
	deadline := time.Now().Add(consoleReadyTimeout)
	for {
		if err := probe(); err == nil {
			fmt.Fprintf(os.Stderr, "magus: server started (pid %d); logs at %s; `%s` stops it\n", pid, logPath, hint.ServerStop)
			return nil
		}
		if time.Now().After(deadline) {
			reapSpawned(ctx, pid, "")
			return fmt.Errorf("server (pid %d) did not serve the console at %s within %s; see %s", pid, addr, consoleReadyTimeout, logPath)
		}
		select {
		case <-ctx.Done():
			reapSpawned(ctx, pid, "")
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// reapSpawned kills a server this process spawned but never got a working socket out of.
// Without it every failure path leaks the process: with console.enabled=false the wait
// times out by construction, so each attempt left one more running.
//
// It checks WHO it is about to kill. The pid was Release()d to detach the child, so
// this process is not its parent any more and the number is free for reuse the moment
// it exits, and the callers reach here precisely when something went wrong, which is
// when that is likeliest. A server answering on sock under a different pid means ours
// is already gone and the number belongs to somebody else now. An empty sock skips that
// half and checks liveness only, for a caller with no socket address to ask.
//
// os.FindProcess rather than the spawned handle, which was Release()d.
func reapSpawned(ctx context.Context, pid int, sock string) {
	if !sysPID.Alive(pid) {
		return
	}
	if sock != "" {
		if st, err := proc.QueryStatus(ctx, sock); err == nil && st.ParentPID != 0 && st.ParentPID != pid {
			// Somebody else's server owns the socket, so ours exited and this pid is no
			// longer ours to kill.
			return
		}
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Kill()
}

// waitServerReady polls the server socket until it answers a status query or the timeout
// elapses. A successful status round-trip is the readiness signal: the socket is bound and
// the server is accepting, so a script that chained on `server start` can proceed.
func waitServerReady(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := proc.QueryStatus(ctx, addr); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server did not become ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// isServerRun reports whether a `server` argv runs a server, here or detached: `start`
// without a help flag, or the detached child's `--foreground`.
func isServerRun(subArgs []string) bool {
	if len(subArgs) == 0 {
		return false
	}
	switch subArgs[0] {
	case hint.ServerStart.Leaf():
		return !isServerStartHelp(subArgs)
	case "-foreground", "--foreground":
		return true
	}
	return false
}

// isServerStartHelp reports whether `server start` was invoked only to print usage. subArgs
// starts with "start"; a help token after it means the caller wants the flag list, not a
// server, so startup skips the auto-background handoff and lets the normal dispatch print it.
func isServerStartHelp(subArgs []string) bool {
	for _, a := range subArgs[1:] {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

// wantsForeground detects --foreground before the formal flag parse, so the
// backgrounding decision is made in startup().
func wantsForeground(args []string) bool {
	for _, a := range args {
		if a == "-foreground" || a == "--foreground" {
			return true
		}
	}
	return false
}

func serverStop(ctx context.Context, args []string) error {
	var tf *gen.ServerStopFlags
	_, err := cmdParse("server stop", args, func(fs *flag.FlagSet) {
		tf = gen.BindServerStop(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server stop [flags]")
			fmt.Fprintln(os.Stderr, "\nSend a graceful shutdown request to the running server. In-flight RPCs")
			fmt.Fprintln(os.Stderr, "complete before the server exits. The broker is a separate process; the")
			fmt.Fprintln(os.Stderr, "services it hosts are stopped with `"+hint.BrokerStop.With("--services")+"`.")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	// Confirm a server is actually there before claiming to stop it, and capture its pid
	// for the report. A resolved address does not guarantee a live one (a configured
	// daemon.address, or a stale socket), so a failed status query means nothing to stop.
	addr := resolveServerAddr(tf.Socket)
	st, qerr := proc.QueryStatus(ctx, addr)
	if qerr != nil {
		fmt.Fprintf(os.Stderr, "magus: no server is running at %s\n", addr)
		return errSilent{exitCode: 1}
	}
	if err := proc.Shutdown(ctx, addr); err != nil {
		return fmt.Errorf("server stop: %w", err)
	}
	// Verify the server is actually gone rather than trusting the shutdown reply. The
	// shutdown handler acknowledges before the process has torn down, and an earlier bug made
	// stop a silent no-op, so stop must observe the socket stop answering before reporting.
	if err := waitSocketGone(ctx, addr, stopTimeout); err != nil {
		return fmt.Errorf("server stop: server (pid %d) on %s did not stop within %s", st.ParentPID, addr, stopTimeout)
	}
	if st.ParentPID != 0 {
		fmt.Fprintf(os.Stderr, "magus: stopped server (pid %d)\n", st.ParentPID)
	} else {
		fmt.Fprintln(os.Stderr, "magus: stopped server")
	}
	return nil
}

// stopTimeout bounds how long a stop waits to confirm a server or broker is gone after a
// shutdown request. In-flight builds drain first, so allow generous headroom before
// declaring the stop unverified.
const stopTimeout = 30 * time.Second

// waitSocketGone polls addr until it stops answering or the timeout elapses. It is the
// verification half of a stop: a shutdown reply only means the request was accepted, so
// stop confirms the socket has actually gone quiet before reporting success.
func waitSocketGone(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if !proc.SocketLive(ctx, addr) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s still answers after %s", addr, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// resolveServerAddr is where the server listens: an explicit --socket, then
// daemon.address (the flag, env and yaml all land there), then server.sock. It never
// consults MAGUS_DAEMON_SOCKET or scans the socket directory, which could only find a
// per-process pool that dies with its command, and never the server.
func resolveServerAddr(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := globalCfg.Daemon.Address; v != "" {
		return v
	}
	return proc.ServerDefaultAddr()
}

// jobRunCatalog submits one of the server's OWN jobs and returns immediately, the CLI
// counterpart to the magus.job.v1alpha1 JobService RPC. The set is the shared jobs registry
// (sync-graph, rotate-activities, rotate-logs, clear-cache); `job run` with no name lists
// them. A no-op when no server is running, so the VCS refresh hook (which calls
// `job run sync-graph`) never blocks or fails a checkout. The server coalesces an identical
// in-flight job, reported back as an empty invocation id ("already running").
//
// It dials the server's own socket and nothing else: only the server runs a job that
// outlives this process, and no per-process pool ever binds that socket.
//
// RUN rather than fork, and the difference is the user's: fork declares work somebody else
// will hold, and nobody forks the server's housekeeping. `run` is the shell's word for
// starting something you do not hold, which is exactly what submitting this is.
func jobRunCatalog(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		jobRunUsage()
		return nil
	}
	name := args[0]
	job, ok := job.Lookup(name)
	if !ok {
		return fmt.Errorf("magus job run: no job named %q; run `%s` to list them", name, hint.JobRun)
	}
	addr := resolveServerAddr("")
	if !proc.SocketLive(ctx, addr) {
		return nil // no server: quietly do nothing so a checkout hook is never delayed
	}
	inv, err := proc.SubmitJob(ctx, addr, job.Argv, version)
	if err != nil {
		// Best-effort: a hook must not fail a checkout. Swallow and succeed; the next
		// trigger (hook, RPC, or manual submit) will catch up.
		slog.DebugContext(ctx, "server job: submit failed", slog.String("job", name), slog.String("error", err.Error()))
		return nil
	}
	if inv == "" { // the server coalesced this into an already-running job of the same kind
		fmt.Fprintf(os.Stderr, "magus: %s is already running\n", name)
	} else {
		fmt.Fprintf(os.Stderr, "magus: submitted %s in the background (job %s)\n", name, inv)
	}
	printJobWatchHint(os.Stderr)
	return nil
}

// printJobWatchHint prints a link to watch jobs in the console dashboard.
//
// The link is UNAUTHENTICATED and the token stays one shell substitution away, which is the
// same call liveExplorerLink already made and for the same reason: a fragment is never
// transmitted on the document GET, so embedding the token read as safe, but the line is
// still a credential written to stdout, and stdout is scrollback, a captured run log, a
// termcast, and the context of whatever agent ran the command. This repository has already
// rotated tokens that escaped that way.
//
// The terminal check stays, but it is no longer a secrecy measure: it is that this line
// invites somebody to go look at something, and the VCS refresh hook is not somebody. A
// suggestion nobody can act on is noise in a log.
func printJobWatchHint(w *os.File) {
	if !tty.IsTerminalWriter(w, tty.SystemProbe) {
		return
	}
	if u := consoleWatchURL(); u != "" {
		fmt.Fprintf(w, "magus: watch it in the console dashboard: %s\n%s\n", u, authHint(u))
	}
}

// consoleWatchURL builds the console dashboard URL for watching jobs, served BY this
// daemon from its own loopback origin (http://<host>/console/dashboard/): the browser
// loads the page and connects back to this daemon over that one loopback origin and shows
// the running pool, where a submitted job appears and deep-links to its live log. Returns
// "" when the console is disabled.
//
// It NEVER embeds the bearer token (see printJobWatchHint), so it also no longer depends on
// a token being loadable. It used to return "" when auth.Load failed, which meant a reader
// with no token yet was shown nothing at all rather than the URL plus the command that mints
// one.
func consoleWatchURL() string {
	if globalCfg.Console.Enabled != nil && !*globalCfg.Console.Enabled {
		return ""
	}
	return console.Link(console.LinkOpts{Host: mcpAddrString(), Surface: "dashboard"})
}

// consoleRootURL is the console's own address, for the three places a person is already
// looking when they need it: `server start`, `status`, and `session --brief`. Empty when
// the console is disabled or there is no address to build one from.
//
// It exists because the address was only ever in the daemon's log, on a line written for a
// machine ("static console mounted path=/console/"), so the one surface built for a person
// to look at was the one surface nothing told them how to reach.
func consoleRootURL() string {
	if globalCfg.Console.Enabled != nil && !*globalCfg.Console.Enabled {
		return ""
	}
	return console.Root(mcpAddrString())
}

// consoleDiffURL builds the console Diff surface URL for the working changeset, with the same
// degrade as consoleWatchURL: "" when the console is disabled, and never a token in the link.
func consoleDiffURL() string {
	if globalCfg.Console.Enabled != nil && !*globalCfg.Console.Enabled {
		return ""
	}
	return console.Link(console.LinkOpts{Host: mcpAddrString(), Surface: "diff"})
}

func jobRunUsage() {
	fmt.Fprintln(os.Stderr, "usage: magus job run <name>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Submit one of the server's own jobs, the housekeeping magus does for itself,")
	fmt.Fprintln(os.Stderr, "then return immediately. It shows beside every other job in `magus ls jobs`.")
	fmt.Fprintln(os.Stderr, "A no-op when no server is running, so a VCS hook can call it unconditionally.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Jobs:")
	for _, j := range job.All() {
		fmt.Fprintf(os.Stderr, "  %-16s%s\n", j.Name, j.Desc)
	}
}

// serverRotateActivities is the worker for the rotate-activities job: it trims the workspace
// activity trail back to its cap and garbage-collects orphaned payload blobs. It runs inside the
// daemon when dispatched as a job (reusing the warm workspace) and works standalone with no
// daemon too. The trail lives under the workspace cache dir, the same base the MCP handler
// writes and the ActivityService reads. Normally reached via `magus job run rotate-activities`.
func serverRotateActivities(ctx context.Context, root string, args []string) error {
	if _, err := cmdParse("server rotate-activities", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server rotate-activities")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Trim the activity trail to its cap and drop orphaned payload blobs. This is")
			fmt.Fprintln(os.Stderr, "the worker for `"+hint.JobRun.With("rotate-activities")+"`; prefer that form.")
		}
	}); err != nil {
		return err
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("server rotate-activities: %w", err)
	}
	trail.Rotate(m.CacheDir())
	return nil
}

// serverRotateLogs is the worker for the rotate-logs job: it trims the invocation run-log
// journals (<cacheDir>/runs/<inv>.jsonl) to the count and byte caps and drops anything older
// than config.Maintenance.RotateLogs, keeping the most recent ones. It runs inside the daemon
// when dispatched as a job and works standalone too. Normally reached via
// `magus job run rotate-logs`.
func serverRotateLogs(ctx context.Context, root string, args []string) error {
	if _, err := cmdParse("server rotate-logs", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server rotate-logs")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Trim the invocation run-log journals to their cap, keeping the most recent.")
			fmt.Fprintln(os.Stderr, "This is the worker for `"+hint.JobRun.With("rotate-logs")+"`; prefer that form.")
		}
	}); err != nil {
		return err
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("server rotate-logs: %w", err)
	}
	removed, freed := cache.NewOutputStore(m.CacheDir()).RotateRuns(cache.DefaultMaxRuns, cache.DefaultMaxRunBytes, globalCfg.Daemon.Maintenance.RotateLogs)
	slog.InfoContext(ctx, "rotated run-logs", slog.Int("removed", removed), slog.Int64("bytes_freed", freed))
	return nil
}

// serverPrunePreserved is the worker for the prune-preserved job: it drops the working-copy
// captures `vcs checkpoint --preserve` minted once they outlive the retention that flag
// promises. It runs inside the daemon when dispatched as a job and works standalone too.
// Normally reached via `magus job run prune-preserved`.
//
// The failure is RETURNED here, where Preserve's own prune deliberately discards it. The
// two are not the same call: a housekeeping failure reported out of Preserve would send a
// caller looking for work that is safely stored, while a pass whose only job IS the
// housekeeping has nothing else to report, and a store nobody can prune any more has to be
// visible somewhere. Here that is the job's outcome in the activity trail.
func serverPrunePreserved(ctx context.Context, root string, args []string) error {
	if _, err := cmdParse("server "+job.NamePrunePreserved, args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server prune-preserved")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Drop the working-copy captures `magus vcs checkpoint --preserve` minted")
			fmt.Fprintln(os.Stderr, "once they are past their retention. Sapling captures survive this pass;")
			fmt.Fprintln(os.Stderr, "Jujutsu mints none. This is the worker for")
			fmt.Fprintln(os.Stderr, "`"+hint.JobRun.With(job.NamePrunePreserved)+"`; prefer that form.")
		}
	}); err != nil {
		return err
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NamePrunePreserved, err)
	}
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NamePrunePreserved, err)
	}
	dropped, err := vcs.PrunePreserved(ctx, m.Root(), res)
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NamePrunePreserved, err)
	}
	slog.InfoContext(ctx, "pruned preserved captures",
		slog.Int("dropped", len(dropped)), slog.String("vcs", res.Name))
	return nil
}

// installRefreshHooks installs the VCS refresh hook so a history change (branch switch,
// merge, rebase) pokes this daemon to reconcile in the background. It reuses the same
// per-VCS installer as the merge driver (types.RefreshHookInstaller), so there is one
// VCS-integration path. Best-effort: a non-git tree, a VCS with no hook support (jj), or
// a write failure is noted, never fatal to starting the daemon.
func installRefreshHooks(ctx context.Context) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	res, err := vcs.Resolve(ctx, cwd, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return
	}
	root, err := res.VCS.Root(ctx, cwd)
	if err != nil {
		root = cwd
	}
	installed, err := res.VCS.InstallRefreshHook(ctx, root, hint.JobRun.With("sync-graph"))
	if errors.Is(err, types.ErrVCSUnsupported) {
		return // this VCS has no hook support
	}
	if err != nil {
		slog.WarnContext(ctx, "server start: could not install VCS refresh hook", slog.String("error", err.Error()))
		return
	}
	if len(installed) > 0 {
		fmt.Fprintf(os.Stderr, "magus: installed %s refresh hook(s) [%s]; history changes now reconcile the graph automatically\n", res.Name, strings.Join(installed, ", "))
	}
}

// installDriftHooks installs the VCS drift-notice hook (types.DriftHookInstaller) so a
// commit and the push that follows it each poke this daemon to check, in the background,
// whether the commit left generated output stale. Same shape and same guarantees as
// installRefreshHooks: best-effort, never fatal to starting the daemon, and a no-op on a
// non-git tree or a VCS with no hook support (jj).
func installDriftHooks(ctx context.Context) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	res, err := vcs.Resolve(ctx, cwd, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return
	}
	root, err := res.VCS.Root(ctx, cwd)
	if err != nil {
		root = cwd
	}
	installed, err := res.VCS.InstallDriftHook(ctx, root, hint.JobRun.With(job.NameCheckDrift))
	if errors.Is(err, types.ErrVCSUnsupported) {
		return // this VCS has no hook support
	}
	if err != nil {
		slog.WarnContext(ctx, "server start: could not install VCS drift-notice hook", slog.String("error", err.Error()))
		return
	}
	if len(installed) > 0 {
		fmt.Fprintf(os.Stderr, "magus: installed %s drift-notice hook(s) [%s]; a commit that leaves generated output stale is now noticed automatically\n", res.Name, strings.Join(installed, ", "))
	}
}

// serverReload drops the workspaces a running server holds open, so the next command
// against each reopens it and re-reads its config.
//
// It exists because editing magus.yaml otherwise meant restarting the server: the server
// keeps a workspace warm across invocations, and each one captured its config when it
// loaded. Nothing was stale in a way that looked broken: the setting simply had no
// effect until something evicted the workspace, which is a TTL away and invisible.
//
// Deliberately not a `server job`: a job is dispatched against a workspace, so it would
// acquire the very entry it is meant to drop and then have to exempt itself. This is a
// control operation on the server, like `stop`, and sits beside it.
func serverReload(ctx context.Context, args []string) error {
	var socket string
	_, err := cmdParse("server reload", args, func(fs *flag.FlagSet) {
		fs.StringVar(&socket, "socket", "", "Server socket (default: config / MAGUS_DAEMON_ADDRESS / server.sock)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server reload [flags]")
			fmt.Fprintln(os.Stderr, "\nRe-read configuration without restarting the server. Drops the workspaces")
			fmt.Fprintln(os.Stderr, "the server is holding open, so the next command against each reopens it and")
			fmt.Fprintln(os.Stderr, "picks up magus.yaml as it now stands.")
			fmt.Fprintln(os.Stderr, "\nA workspace with a run in flight is left alone: it keeps the config it")
			fmt.Fprintln(os.Stderr, "started with, and is reported so you know to re-run this once it finishes.")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	// No server means nothing is holding a stale config: every one-shot command reads
	// magus.yaml as it runs. Saying so and exiting 0 is the honest answer: this is "make
	// sure nothing is holding an old config", and nothing is.
	addr := resolveServerAddr(socket)
	if _, qerr := proc.QueryStatus(ctx, addr); qerr != nil {
		fmt.Fprintln(os.Stderr, "magus: no server is running; every command already reads the current config")
		return nil //nolint:nilerr // no server is the success case here: nothing is holding an old config
	}

	dropped, busy, err := proc.ReloadConfig(ctx, addr)
	if err != nil {
		return fmt.Errorf("server reload: %w", err)
	}
	switch {
	case dropped == 0 && busy == 0:
		fmt.Fprintln(os.Stderr, "magus: the server held no open workspaces; the next command reads the current config")
	case busy > 0:
		fmt.Fprintf(os.Stderr, "magus: reloaded %d workspace(s); %d still running and kept the config they started with, so re-run this when they finish\n", dropped, busy)
	default:
		fmt.Fprintf(os.Stderr, "magus: reloaded %d workspace(s); the next command against each reads the current config\n", dropped)
	}
	return nil
}

// serverCheckReview is the worker for the check-review job: it notes, once, that a review this
// tree took part in has merged, so the conversation can be kept before it becomes only a page on
// somebody else's website.
//
// A JOB rather than a poll in the browser, because the console is optional and a merge does not
// wait for it to be open. Being a job also buys what a hand-rolled timer had to fake: coalescing,
// a configured interval, a last-run the schedule reads back from the trail so it survives a
// restart, and idle-gating so it never competes with a build.
//
// The gate is the SESSION, and it is what keeps this from being a tracker: no review session for
// this tree means the reader never opened a review here, and no forge is asked anything at all.
// Opening a review is the opt-in.
//
// It records rather than notifies. The event is the durable fact; the console's watcher reads the
// trail for it, exactly as it already does for a share being opened. Normally reached via
// `magus job run check-review`.
func serverCheckReview(ctx context.Context, root string, args []string) error {
	if _, err := cmdParse("server "+job.NameCheckReview, args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server check-review")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Note when a review this tree took part in has merged. This is the worker")
			fmt.Fprintln(os.Stderr, "for `"+hint.JobRun.With("check-review")+"`; prefer that form.")
		}
	}); err != nil {
		return err
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NameCheckReview, err)
	}
	// The PERSISTED watermark, not a session. This runs in its own process, so the store's
	// in-memory session map is empty by construction: reading it was a gate that could never
	// open, and the job was a guaranteed no-op until this was fixed.
	store := changeset.NewStore(m.CacheDir())
	seen := store.LoadSeenThreads()
	drafts := store.LoadDrafts()
	if len(seen) == 0 && len(drafts) == 0 {
		// Nothing persisted means nobody has read or drafted anything in a review here, which is
		// the opt-in: no forge is asked about a workspace whose reviews were never opened.
		return nil
	}
	from := m.ReviewOrigin(ctx)
	at := bindings.FindReview(ctx, from.Branch, from.Remote)
	if !at.Open() {
		return nil
	}
	// Reachability is READ here, unlike on the surfaces that render what they could get. An
	// unreachable forge answers with an EMPTY list, and every number below is derived from that
	// list, so reporting anyway meant "3 remarks live only on the host" when the true figure was
	// fifteen, or silence about a merge whose whole conversation was unreadable. "Nothing was
	// said" and "I could not ask" are opposite facts, and this is the one place that can still
	// tell them apart.
	//
	// The error is a MALFORMED remark and never the unreachable host, so it is dropped rather
	// than read: the threads that decoded are in hand, and one unreadable record must not blank
	// the only report this merge will get.
	threads, reached, _ := bindings.ReviewThreadsReached(ctx, at)
	if !reached {
		// Not the job's failure to report: a forge that could not be reached is a fact about the
		// network, and raising it would mark this job failed on the trail every fifteen minutes
		// for as long as the reader is offline. The next tick asks again.
		return nil
	}

	// What arrived since the reader last had the conversation on screen. Ids rather than a count,
	// because a deleted remark plus a new one nets zero and the new one would never be reported.
	// The watermark is the READER's; see DiffReview.SeenThreads for why it cannot be the job's.
	if unseen := (types.DiffReview{SeenThreads: seen}).UnseenThreads(threads); len(unseen) > 0 {
		trail.Append(ctx, m.CacheDir(), trail.Event{
			Ts:        time.Now().UnixMilli(),
			Kind:      trail.KindJob,
			Origin:    types.Origin{EntryPoint: types.EntryPointDaemon},
			Workspace: m.Root(),
			Action:    "review.said",
			Outcome:   trail.OutcomeOK,
			// The ids ride along so the console can key its notification on exactly this set: the
			// job running again before the reader looks must not say the same thing twice.
			Preview: fmt.Sprintf("%s: %d: %s", at.Repo, len(unseen), strings.Join(unseen, ",")),
		})
	}

	if !at.Merged() {
		return nil
	}
	said := len(threads) + len(drafts)
	if said == 0 {
		// Merged with nothing said on it. There is no conversation to keep, and an event here
		// would train the reader to ignore the ones that matter. A forge that could not be
		// reached returned above rather than landing here, so this really is "nothing was said".
		return nil
	}
	trail.Append(ctx, m.CacheDir(), trail.Event{
		Ts:        time.Now().UnixMilli(),
		Kind:      trail.KindJob,
		Origin:    types.Origin{EntryPoint: types.EntryPointDaemon},
		Workspace: m.Root(),
		Action:    "review.merged",
		Outcome:   trail.OutcomeOK,
		Preview:   fmt.Sprintf("%s: %d", at.Repo, said),
	})
	return nil
}
