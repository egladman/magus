package main

import (
	"context"
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
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/jobs"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

func serverCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		serverUsage()
		return usagef("magus server: target required (want start, stop, reload, or job)")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		serverUsage()
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case clihint.ServerStart.Leaf():
		return serverStart(ctx, rest)
	case clihint.ServerStop.Leaf():
		return serverStop(ctx, rest)
	case clihint.ServerJob.Leaf():
		return serverJob(ctx, rest)
	case clihint.ServerReload.Leaf():
		return serverReload(ctx, rest)
	case jobs.NameRotateActivities:
		return serverRotateActivities(ctx, root, rest)
	case jobs.NameRotateLogs:
		return serverRotateLogs(ctx, root, rest)
	case jobs.NameCheckReview:
		return serverCheckReview(ctx, root, rest)
	default:
		return usagef("magus server: unknown target %q (want start, stop, reload, or job); use `%s` to inspect daemon state", sub, clihint.Status)
	}
}

func serverUsage() {
	fmt.Fprintln(os.Stderr, "usage: magus server <start|stop|reload|job> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Targets:")
	fmt.Fprintln(os.Stderr, "  start   start a persistent daemon and block until stopped")
	fmt.Fprintln(os.Stderr, "  stop    send a graceful shutdown request to a running daemon")
	fmt.Fprintln(os.Stderr, "  reload  re-read configuration without restarting: drop the daemon's open workspaces")
	fmt.Fprintln(os.Stderr, "  job     submit a background maintenance job to a running daemon (run `magus server job` to list)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "Use `%s` to inspect daemon pool state and check reachability.\n", clihint.Status)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The socket address is taken from --daemon-address, MAGUS_DAEMON_ADDRESS,")
	fmt.Fprintln(os.Stderr, "or daemon.address in magus.yaml. When none is set, `server start` uses:")
	fmt.Fprintln(os.Stderr, "  "+daemonDefaultAddr())
}

// daemonDetachEnv marks the re-execed child of an auto-backgrounding `server start`. When
// set, serverStart runs the daemon in the foreground (the parent already detached it); the
// child never re-backgrounds itself. See startDaemonBackground.
const daemonDetachEnv = "MAGUS_DAEMON_DETACH"

// daemonReadyTimeout bounds how long the backgrounding parent waits for the detached child
// to start accepting on the socket before it reports the start as failed.
const daemonReadyTimeout = 60 * time.Second

func serverStart(ctx context.Context, args []string) error {
	var sf *gen.ServerStartFlags
	_, err := cmdParse("server start", args, func(fs *flag.FlagSet) {
		sf = gen.BindServerStart(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server start [--foreground] [flags]")
			fmt.Fprintln(os.Stderr, "\nStart the persistent daemon that serves MCP and accepts nested magus calls.")
			fmt.Fprintln(os.Stderr, "By default it auto-backgrounds: this command detaches the daemon, waits until")
			fmt.Fprintln(os.Stderr, "it is accepting connections, prints its pid, and returns 0. Starting when a")
			fmt.Fprintln(os.Stderr, "daemon is already running is a no-op that also returns 0.")
			fmt.Fprintln(os.Stderr, "\nWith --foreground the daemon runs in this process and blocks until stopped")
			fmt.Fprintln(os.Stderr, "(SIGINT / SIGTERM or `"+clihint.ServerStop.String()+"`). Use it under a process")
			fmt.Fprintln(os.Stderr, "supervisor (systemd --user) or when debugging.")
			fmt.Fprintln(os.Stderr, "\nSocket address: --daemon-address flag > MAGUS_DAEMON_ADDRESS env >")
			fmt.Fprintln(os.Stderr, "daemon.address in magus.yaml > stable default ("+daemonDefaultAddr()+")")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	_ = sf.Foreground // the parent/child split is decided in startDaemonBackground; here we always run the daemon

	addr := os.Getenv("MAGUS_DAEMON_SOCKET")
	if addr == "" {
		return fmt.Errorf("magus server start: daemon socket not available (no workspace found, or socket bind failed)")
	}
	fmt.Fprintf(os.Stderr, "magus: daemon listening on %s\n", addr)
	fmt.Fprintf(os.Stderr, "magus: send SIGINT / SIGTERM or run `%s` to shut down\n", clihint.ServerStop)

	installRefreshHooks(ctx)

	// Start the MCP HTTP server alongside the daemon so MCP clients can
	// connect without a separate process. No-op when mcp.enabled=false.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// daemonProvider was built by startMultiWorkspaceDaemon (which runs before this
	// command handler) so the bridge Magus shares the same OTel instruments the
	// per-workspace builds record into.
	startMCPWithDaemon(ctx, cancel, daemonProvider)

	// Low-key background maintenance: rotate the trail/run-logs and reconcile the graph on their
	// configured intervals, idle-gated. Only a persistent `server start` daemon schedules these,
	// since they must outlive any single invocation. Socket and trail base are late-bound (set
	// during startup), so the scheduler reads them per tick.
	maintenance.Start(ctx, maintenance.Options{
		Schedule: globalCfg.Daemon.Maintenance,
		Socket:   func() string { return os.Getenv("MAGUS_DAEMON_SOCKET") },
		Trail:    func() string { return daemonTrailBase },
		Version:  version,
	})

	// Block until a signal cancels ctx OR an RPC `server stop` closes the proc server. The
	// second case is the load-bearing one: the shutdown handler cancels only the listener's
	// own context (a sibling of this ctx), so without observing srv.Done() the daemon would
	// keep running after its socket was already torn down.
	var serverDone <-chan struct{}
	if daemonServer != nil {
		serverDone = daemonServer.Done()
	}
	select {
	case <-ctx.Done():
	case <-serverDone:
	}
	return nil
}

// startDaemonBackground implements the default auto-backgrounding of `server start`. It runs
// in the launching process before the daemon is built. It returns done==true when it fully
// handled the request (the caller returns exitCode without building a daemon): a daemon was
// already running (idempotent no-op), a detached child was spawned and became ready, or
// spawning failed. It returns done==false only for the foreground daemon - an explicit
// --foreground, or the re-execed detached child (marked by daemonDetachEnv) - which then
// builds and runs the daemon in-process.
func startDaemonBackground(ctx context.Context, cfg config.Config, subArgs []string) (exitCode int, done bool) {
	if os.Getenv(daemonDetachEnv) != "" {
		return 0, false // we are the detached child: run the daemon in the foreground
	}
	if wantsForeground(subArgs) {
		return 0, false // explicit foreground for a supervisor / debugging
	}

	addr := cfg.Daemon.Address // startup defaulted this to the stable socket for `server start`
	// Idempotent start: a daemon already accepting on the socket means there is nothing to do.
	if proc.SocketLive(ctx, addr) {
		if st, err := proc.QueryStatus(ctx, addr); err == nil && st.ParentPID != 0 {
			fmt.Fprintf(os.Stderr, "magus: daemon already running (pid %d) on %s%s\n",
				st.ParentPID, addr, servingSuffix(st))
		} else {
			fmt.Fprintf(os.Stderr, "magus: daemon already running on %s\n", addr)
		}
		return 0, true
	}

	pid, logPath, err := spawnDetachedDaemon(os.Args[1:])
	if err != nil {
		slog.ErrorContext(ctx, "server start: could not background the daemon", slog.String("error", err.Error()))
		return 1, true
	}
	if err := waitDaemonReady(ctx, addr, daemonReadyTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "magus: daemon (pid %d) did not become ready within %s; see %s\n", pid, daemonReadyTimeout, logPath)
		return 1, true
	}
	fmt.Fprintf(os.Stderr, "magus: daemon started (pid %d) on %s; logs at %s\n", pid, addr, logPath)
	return 0, true
}

// servingSuffix names the workspaces a running daemon has loaded, or "" when it has none yet.
//
// One socket per user serves every workspace, so "already running" answered the question the
// caller asked and not the one they meant. Starting the daemon from a second worktree returns 0
// with nothing loaded from THIS tree, and the console then shows the tree it was started in -
// which reads as the command having worked. The roots are already on the status wire; the message
// simply never said them.
//
// The workspace this call was made from is not marked, deliberately: a daemon loads a workspace
// lazily on first use, so "not listed" means "not loaded yet" far more often than it means
// "wrong daemon", and flagging it would raise an alarm about the ordinary case.
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

// daemonChildEnv returns this process's environment with MAGUS_DAEMON_SOCKET removed. A
// child inheriting it believes it is already adopted, binds no socket, and reports the
// parent's - leaving a daemon `server stop` cannot find.
func daemonChildEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "MAGUS_DAEMON_SOCKET=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// spawnDetachedDaemon re-execs this binary as a detached foreground daemon and returns its
// pid and the log file its stdio is redirected to. It marks the child via daemonDetachEnv so
// it runs the daemon rather than backgrounding again, and the child is fully detached (its
// own session on unix) and Release()d so this process never waits on it.
//
// args is the child's argv. `server start` passes its own through, so a --daemon-address the
// user set is honored. Any other caller must pass an explicit `server start --foreground`:
// daemonDetachEnv is read only on the server-start path, so re-execing another command with
// it set would rerun that command instead of starting a daemon.
func spawnDetachedDaemon(args []string) (pid int, logPath string, err error) {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	logPath = filepath.Join(proc.SockDir(), "magus-daemon.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("open daemon log %s: %w", logPath, err)
	}
	defer func() { _ = logf.Close() }()

	cmd := exec.Command(exe, args...) //nolint:gosec // G702: re-execs this same magus binary to detach the daemon
	cmd.Env = append(daemonChildEnv(), daemonDetachEnv+"=1")
	cmd.Stdin = nil
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return 0, "", fmt.Errorf("start daemon process: %w", err)
	}
	pid = cmd.Process.Pid
	_ = cmd.Process.Release() // detach: the child outlives us, so never wait/reap it
	return pid, logPath, nil
}

// consoleReadyTimeout bounds the wait for a freshly spawned daemon to serve the console.
// It covers process start, socket bind, and bridge mount together, so it is longer than
// daemonReadyTimeout, which covers only the socket.
const consoleReadyTimeout = 20 * time.Second

// ensureConsoleDaemon brings the daemon up when a command needs the console.
//
// The console IS the daemon's own surface, so `graph export --follow` is a plain request
// for a server and starting one is doing what was asked rather than a side effect.
// Commands that merely run FASTER with a daemon never call this, which is why an ordinary
// build (and therefore CI) still starts nothing.
//
// A daemon already up whose console is not serving is NOT restarted: a second one cannot
// fix a bridge disabled by config or bound elsewhere, and it would leave two running where
// the user asked for none.
//
// root names the workspace the child will serve. One socket per user serves every
// workspace, so a daemon started from here is authoritative for whoever connects next -
// saying which tree it came up in is what stops it reading as "the daemon", the same
// reason servingSuffix exists on the `server start` path.
func ensureConsoleDaemon(ctx context.Context, addr, root string) error {
	probe := func() error {
		pctx, cancel := context.WithTimeout(ctx, probeLiveBridgeTimeout)
		defer cancel()
		return probeLiveBridge(pctx, addr)
	}
	if err := probe(); err == nil {
		return nil
	}
	if sock, ok := proc.LookupStableSocket(ctx); ok {
		return fmt.Errorf("the daemon is running on %s but its console is not serving at %s; check console.enabled and mcp.address", sock, addr)
	}

	fmt.Fprintf(os.Stderr, "magus: starting the daemon to serve the console, from %s.\n", root)
	pid, logPath, err := spawnDetachedDaemon([]string{"server", "start", "--foreground"})
	if err != nil {
		return fmt.Errorf("could not start the daemon: %w", err)
	}
	// A serving console proves the socket bound AND the bridge mounted, so waiting on the
	// socket first would add a failure mode without adding information.
	deadline := time.Now().Add(consoleReadyTimeout)
	for {
		if err := probe(); err == nil {
			fmt.Fprintf(os.Stderr, "magus: daemon started (pid %d); logs at %s\n", pid, logPath)
			return nil
		}
		if time.Now().After(deadline) {
			reapDaemon(pid)
			return fmt.Errorf("daemon (pid %d) did not serve the console at %s within %s; see %s", pid, addr, consoleReadyTimeout, logPath)
		}
		select {
		case <-ctx.Done():
			reapDaemon(pid)
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// reapDaemon kills a daemon this process spawned but never got a console out of. Without it
// every failure path leaks the process: with console.enabled=false the wait times out by
// construction, so each attempt left one more running.
//
// os.FindProcess rather than the spawned handle, which was Release()d to detach it.
func reapDaemon(pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Kill()
}

// waitDaemonReady polls the daemon socket until it answers a status query or the timeout
// elapses. A successful status round-trip is the readiness signal: the socket is bound and
// the daemon is accepting, so a script that chained on `server start` can proceed.
func waitDaemonReady(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := proc.QueryStatus(ctx, addr); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon did not become ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// isServerStartHelp reports whether `server start` was invoked only to print usage. subArgs
// starts with "start"; a help token after it means the caller wants the flag list, not a
// daemon, so startup skips the auto-background handoff and lets the normal dispatch print it.
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
			fmt.Fprintln(os.Stderr, "\nSend a graceful shutdown request to a running daemon. In-flight RPCs")
			fmt.Fprintln(os.Stderr, "complete before the daemon exits.")
			fmt.Fprintln(os.Stderr, "\nWith --services, stop the shared services the daemon is hosting (to clear")
			fmt.Fprintln(os.Stderr, "stale state or free held ports) without shutting the daemon down.")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	addr, err := resolveDaemonAddr(ctx, tf.Socket)
	if err != nil {
		// No socket resolved (nothing configured, nothing discoverable): there is no daemon
		// to stop. Say so plainly and exit non-zero so a script never reads a clean stop as
		// having terminated something.
		fmt.Fprintln(os.Stderr, "magus: no running daemon found")
		return errSilent{exitCode: 1}
	}
	if tf.Services {
		n, err := proc.StopAllServices(ctx, addr)
		if err != nil {
			return fmt.Errorf("server stop: %w", err)
		}
		fmt.Fprintf(os.Stderr, "magus: stopped %d hosted service(s); daemon still running\n", n)
		return nil
	}

	// Confirm a daemon is actually there before claiming to stop it, and capture its pid for
	// the report. A resolved address does not guarantee a live daemon (a configured
	// daemon.address, or a stale socket), so a failed status query means nothing to stop.
	st, qerr := proc.QueryStatus(ctx, addr)
	if qerr != nil {
		fmt.Fprintf(os.Stderr, "magus: no running daemon at %s\n", addr)
		return errSilent{exitCode: 1}
	}
	if err := proc.Shutdown(ctx, addr); err != nil {
		return fmt.Errorf("server stop: %w", err)
	}
	// Verify the daemon is actually gone rather than trusting the shutdown reply. The
	// shutdown handler acknowledges before the process has torn down, and an earlier bug made
	// stop a silent no-op, so stop must observe the socket stop answering before reporting.
	if err := waitDaemonStopped(ctx, addr, daemonStopTimeout); err != nil {
		return fmt.Errorf("server stop: daemon (pid %d) on %s did not stop within %s", st.ParentPID, addr, daemonStopTimeout)
	}
	if st.ParentPID != 0 {
		fmt.Fprintf(os.Stderr, "magus: stopped daemon (pid %d)\n", st.ParentPID)
	} else {
		fmt.Fprintln(os.Stderr, "magus: stopped daemon")
	}
	return nil
}

// daemonStopTimeout bounds how long `server stop` waits to confirm the daemon is gone after
// a shutdown request. In-flight builds drain first (the daemon completes them before exiting),
// so allow generous headroom before declaring the stop unverified.
const daemonStopTimeout = 30 * time.Second

// waitDaemonStopped polls the daemon socket until it stops answering or the timeout elapses.
// It is the verification half of `server stop`: a shutdown reply only means the request was
// accepted, so stop confirms the socket has actually gone quiet before reporting success.
func waitDaemonStopped(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if !proc.SocketLive(ctx, addr) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon did not stop within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// resolveDaemonAddr resolves the daemon address: explicit flag → config → env → DiscoverSocket.
func resolveDaemonAddr(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := globalCfg.Daemon.Address; v != "" {
		return v, nil
	}
	if v := os.Getenv("MAGUS_DAEMON_SOCKET"); v != "" {
		return v, nil
	}
	return proc.DiscoverSocket(ctx)
}

func daemonDefaultAddr() string {
	return "unix://" + filepath.Join(proc.SockDir(), "magus-daemon.sock")
}

// serverJob submits a named background maintenance job to a running daemon and returns
// immediately. It is the ONLY way to run one: the daemon serves no job RPC, so maintenance is a
// command a person or a hook issues. The job set is the shared jobs registry (sync-graph,
// rotate-activities, rotate-logs, clear-cache); `server job` with no name lists them. A no-op when no persistent daemon is running, so the VCS refresh hook (which
// calls `server job sync-graph`) never blocks or fails a checkout. The daemon coalesces an
// identical in-flight job, reported back as an empty invocation id ("already running").
func serverJob(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		serverJobUsage()
		return nil
	}
	name := args[0]
	job, ok := jobs.Lookup(name)
	if !ok {
		return fmt.Errorf("magus server job: unknown job %q; run `%s` to list jobs", name, clihint.ServerJob)
	}
	addr, err := resolveDaemonAddr(ctx, "")
	if err != nil || addr == "" {
		return nil //nolint:nilerr // no daemon: quietly do nothing so a checkout hook is never delayed
	}
	// Only a PERSISTENT daemon (`server start`) runs a job that outlives this process; a
	// per-process proc server (which magus may spin up for any command) would die when this
	// invocation exits, silently dropping the job. Submit only when we see a real daemon;
	// otherwise no-op, so a hook stays a safe no-op off the daemon.
	st, serr := proc.QueryStatus(ctx, addr)
	if serr != nil || st == nil || st.Mode != "daemon" {
		return nil //nolint:nilerr // not a persistent daemon: no-op so a hook stays a safe no-op off the daemon
	}
	inv, err := proc.SubmitJob(ctx, addr, job.Argv, version)
	if err != nil {
		// Best-effort: a hook must not fail a checkout. Swallow and succeed; the next
		// trigger (hook, RPC, or manual submit) will catch up.
		slog.DebugContext(ctx, "server job: submit failed", slog.String("job", name), slog.String("error", err.Error()))
		return nil
	}
	if inv == "" { // the daemon coalesced this into an already-running job of the same kind
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
// still a credential written to stdout - and stdout is scrollback, a captured run log, a
// termcast, and the context of whatever agent ran the command. This repository has already
// rotated tokens that escaped that way.
//
// The terminal check stays, but it is no longer a secrecy measure - it is that this line
// invites somebody to go look at something, and the VCS refresh hook is not somebody. A
// suggestion nobody can act on is noise in a log.
func printJobWatchHint(w *os.File) {
	if !tty.IsTerminalWriter(w, tty.SystemProbe) {
		return
	}
	if u := consoleWatchURL(); u != "" {
		fmt.Fprintf(w, "magus: watch it in the console dashboard: %s\n%s\n", u, authHint)
	}
}

// consoleWatchURL builds the console dashboard URL for watching jobs, served BY this
// daemon from its own loopback origin (http://<host>/console/dashboard/): the browser
// loads the page and connects back to this daemon over that one loopback origin and shows
// the running pool, where a submitted job appears and deep-links to its live log. Returns
// "" when the console is disabled.
//
// It NEVER embeds the bearer token - see printJobWatchHint - so it also no longer depends on
// a token being loadable. It used to return "" when auth.Load failed, which meant a reader
// with no token yet was shown nothing at all rather than the URL plus the command that mints
// one.
func consoleWatchURL() string {
	if globalCfg.Console.Enabled != nil && !*globalCfg.Console.Enabled {
		return ""
	}
	return console.Link(console.LinkOpts{Host: mcpAddrString(), Surface: "dashboard"})
}

// consoleDiffURL builds the console Diff surface URL for the working changeset, with the same
// degrade as consoleWatchURL: "" when the console is disabled, and never a token in the link.
func consoleDiffURL() string {
	if globalCfg.Console.Enabled != nil && !*globalCfg.Console.Enabled {
		return ""
	}
	return console.Link(console.LinkOpts{Host: mcpAddrString(), Surface: "diff"})
}

func serverJobUsage() {
	fmt.Fprintln(os.Stderr, "usage: magus server job <name>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Submit a background maintenance job to a running daemon, then return")
	fmt.Fprintln(os.Stderr, "immediately. The job shows in the Dashboard. A no-op when no daemon is")
	fmt.Fprintln(os.Stderr, "running, so a VCS hook can call it unconditionally.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Jobs:")
	for _, j := range jobs.All() {
		fmt.Fprintf(os.Stderr, "  %-16s%s\n", j.Name, j.Desc)
	}
}

// serverRotateActivities is the worker for the rotate-activities job: it trims the workspace
// activity trail back to its cap and garbage-collects orphaned payload blobs. It runs inside the
// daemon when dispatched as a job (reusing the warm workspace) and works standalone with no
// daemon too. The trail lives under the workspace cache dir - the same base the MCP handler
// writes and the ActivityService reads. Normally reached via `server job rotate-activities`.
func serverRotateActivities(ctx context.Context, root string, args []string) error {
	if _, err := cmdParse("server rotate-activities", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server rotate-activities")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Trim the activity trail to its cap and drop orphaned payload blobs. This is")
			fmt.Fprintln(os.Stderr, "the worker for `magus server job rotate-activities`; prefer that form.")
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
// journals (<cacheDir>/runs/<inv>.jsonl) back to their cap, keeping the most recent ones. It runs
// inside the daemon when dispatched as a job and works standalone too. Normally reached via
// `server job rotate-logs`.
func serverRotateLogs(ctx context.Context, root string, args []string) error {
	if _, err := cmdParse("server rotate-logs", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server rotate-logs")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Trim the invocation run-log journals to their cap, keeping the most recent.")
			fmt.Fprintln(os.Stderr, "This is the worker for `magus server job rotate-logs`; prefer that form.")
		}
	}); err != nil {
		return err
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("server rotate-logs: %w", err)
	}
	removed, freed := cache.NewOutputStore(m.CacheDir()).RotateRuns(cache.DefaultMaxRuns, cache.DefaultMaxRunBytes)
	slog.InfoContext(ctx, "rotated run-logs", slog.Int("removed", removed), slog.Int64("bytes_freed", freed))
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
	installer, ok := res.VCS.(types.RefreshHookInstaller)
	if !ok {
		return // this VCS has no hook support
	}
	root, err := res.VCS.Root(ctx, cwd)
	if err != nil {
		root = cwd
	}
	installed, err := installer.InstallRefreshHook(ctx, root, "magus server job sync-graph")
	if err != nil {
		slog.WarnContext(ctx, "server start: could not install VCS refresh hook", slog.String("error", err.Error()))
		return
	}
	if len(installed) > 0 {
		fmt.Fprintf(os.Stderr, "magus: installed %s refresh hook(s) [%s]; history changes now reconcile the graph automatically\n", res.Name, strings.Join(installed, ", "))
	}
}

// serverReload drops the workspaces a running daemon holds open, so the next command
// against each reopens it and re-reads its config.
//
// It exists because editing magus.yaml otherwise meant restarting the daemon: the daemon
// keeps a workspace warm across invocations, and each one captured its config when it
// loaded. Nothing was stale in a way that looked broken - the setting simply had no
// effect until something evicted the workspace, which is a TTL away and invisible.
//
// Deliberately not a `server job`: a job is dispatched against a workspace, so it would
// acquire the very entry it is meant to drop and then have to exempt itself. This is a
// control operation on the daemon, like `stop --services`, and sits beside it.
func serverReload(ctx context.Context, args []string) error {
	var socket string
	_, err := cmdParse("server reload", args, func(fs *flag.FlagSet) {
		fs.StringVar(&socket, "socket", "", "daemon socket (default: config / MAGUS_DAEMON_ADDRESS / auto-detect)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server reload [flags]")
			fmt.Fprintln(os.Stderr, "\nRe-read configuration without restarting the daemon. Drops the workspaces")
			fmt.Fprintln(os.Stderr, "the daemon is holding open, so the next command against each reopens it and")
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

	addr, err := resolveDaemonAddr(ctx, socket)
	if err != nil {
		// No daemon means nothing is holding a stale config: every one-shot command reads
		// magus.yaml as it runs. Saying so and exiting 0 is the honest answer - this is
		// "make sure nothing is holding an old config", and nothing is.
		fmt.Fprintln(os.Stderr, "magus: no running daemon; every command already reads the current config")
		return nil //nolint:nilerr // no daemon is the success case here: nothing is holding an old config
	}
	if _, qerr := proc.QueryStatus(ctx, addr); qerr != nil {
		fmt.Fprintln(os.Stderr, "magus: no running daemon; every command already reads the current config")
		return nil //nolint:nilerr // see above: a resolved address with no live daemon is still "nothing to reload"
	}

	dropped, busy, err := proc.ReloadConfig(ctx, addr)
	if err != nil {
		return fmt.Errorf("server reload: %w", err)
	}
	switch {
	case dropped == 0 && busy == 0:
		fmt.Fprintln(os.Stderr, "magus: daemon held no open workspaces; the next command reads the current config")
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
// `magus server job check-review`.
func serverCheckReview(ctx context.Context, root string, args []string) error {
	if _, err := cmdParse("server "+jobs.NameCheckReview, args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server check-review")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Note when a review this tree took part in has merged. This is the worker")
			fmt.Fprintln(os.Stderr, "for `magus server job check-review`; prefer that form.")
		}
	}); err != nil {
		return err
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("server %s: %w", jobs.NameCheckReview, err)
	}
	// The PERSISTED watermark, not a session. This runs in its own process, so the store's
	// in-memory session map is empty by construction - reading it was a gate that could never
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
	// list - so reporting anyway meant "3 remarks live only on the host" when the true figure was
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
	// The watermark is the READER's - see DiffSession.SeenThreads for why it cannot be the job's.
	if unseen := (types.DiffSession{SeenThreads: seen}).UnseenThreads(threads); len(unseen) > 0 {
		trail.Append(ctx, m.CacheDir(), trail.Event{
			Ts:        time.Now().UnixMilli(),
			Kind:      trail.KindJob,
			Actor:     "daemon",
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
		Actor:     "daemon",
		Workspace: m.Root(),
		Action:    "review.merged",
		Outcome:   trail.OutcomeOK,
		Preview:   fmt.Sprintf("%s: %d", at.Repo, said),
	})
	return nil
}
