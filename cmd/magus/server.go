package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

func serverCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		serverUsage()
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case clihint.ServerStart.Leaf():
		return serverStart(ctx, rest)
	case clihint.ServerStop.Leaf():
		return serverStop(ctx, rest)
	case "sync":
		return serverSync(ctx, rest)
	default:
		return fmt.Errorf("magus server: unknown target %q (want start, stop, or sync); use `%s` to inspect daemon state", sub, clihint.Status)
	}
}

func serverUsage() {
	fmt.Fprintln(os.Stderr, "usage: magus server <start|stop> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Targets:")
	fmt.Fprintln(os.Stderr, "  start   start a persistent daemon and block until stopped")
	fmt.Fprintln(os.Stderr, "  stop    send a graceful shutdown request to a running daemon")
	fmt.Fprintln(os.Stderr, "  sync    reconcile the graph now: ask a running daemon to rebuild+reindex in the background (no-op if none); safe from a VCS hook")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "Use `%s` to inspect daemon pool state and check reachability.\n", clihint.Status)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The socket address is taken from --daemon-address, MAGUS_DAEMON_ADDRESS,")
	fmt.Fprintln(os.Stderr, "or daemon.address in magus.yaml. When none is set, `server start` uses:")
	fmt.Fprintln(os.Stderr, "  "+daemonDefaultAddr())
}

func serverStart(ctx context.Context, args []string) error {
	_, err := cmdParse("server start", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server start [flags]")
			fmt.Fprintln(os.Stderr, "\nStart a persistent daemon that accepts nested magus calls. Runs in the")
			fmt.Fprintln(os.Stderr, "foreground — use & or a process supervisor (systemd --user, nohup,")
			fmt.Fprintln(os.Stderr, "direnv) to run it in the background.")
			fmt.Fprintln(os.Stderr, "\nSocket address: --daemon-address flag > MAGUS_DAEMON_ADDRESS env >")
			fmt.Fprintln(os.Stderr, "daemon.address in magus.yaml > stable default ("+daemonDefaultAddr()+")")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

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

	<-ctx.Done()
	return nil
}

func serverStop(ctx context.Context, args []string) error {
	var socket string
	var servicesOnly bool
	_, err := cmdParse("server stop", args, func(fs *flag.FlagSet) {
		fs.StringVar(&socket, "socket", "", "daemon socket (default: config / MAGUS_DAEMON_ADDRESS / auto-detect)")
		fs.BoolVar(&servicesOnly, "services", false, "stop the daemon's hosted services (leave the daemon running)")
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

	addr, err := resolveDaemonAddr(ctx, socket)
	if err != nil {
		return fmt.Errorf("server stop: %w", err)
	}
	if servicesOnly {
		n, err := proc.StopAllServices(ctx, addr)
		if err != nil {
			return fmt.Errorf("server stop: %w", err)
		}
		fmt.Fprintf(os.Stderr, "stopped %d hosted service(s); daemon still running\n", n)
		return nil
	}
	if err := proc.Shutdown(ctx, addr); err != nil {
		return fmt.Errorf("server stop: %w", err)
	}
	return nil
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

// serverSync reconciles the knowledge graph to the current source: it asks a running
// daemon to rebuild the graph and reindex code symbols as a background job, then returns
// immediately. Named for the gitops "sync" idiom (reconcile actual to desired) - it is
// the one-shot counterpart to the daemon's continuous background indexing. A no-op when
// no daemon runs, so it is safe to call from a VCS hook: it never blocks a checkout.
func serverSync(ctx context.Context, args []string) error {
	_, err := cmdParse("server sync", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server sync")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Reconcile the knowledge graph to current source: ask a running daemon to")
			fmt.Fprintln(os.Stderr, "rebuild the graph and reindex code symbols in the background, then return")
			fmt.Fprintln(os.Stderr, "immediately. The job shows in the Dashboard. A no-op when no daemon is")
			fmt.Fprintln(os.Stderr, "running, so a VCS hook can call it unconditionally.")
		}
	})
	if err != nil {
		return err
	}
	addr, err := resolveDaemonAddr(ctx, "")
	if err != nil || addr == "" {
		return nil // no daemon: quietly do nothing so a checkout hook is never delayed
	}
	// Only a PERSISTENT daemon (`server start`) runs a job that outlives this process; a
	// per-process proc server (which magus may spin up for any command) would die when
	// this invocation exits, silently dropping the job. Submit only when we see a real
	// daemon; otherwise no-op, so a hook stays a safe no-op off the daemon.
	st, serr := proc.QueryStatus(ctx, addr)
	if serr != nil || st == nil || st.Mode != "daemon" {
		return nil
	}
	inv, err := proc.SubmitJob(ctx, addr, []string{"graph", "build"})
	if err != nil {
		// Best-effort: a hook must not fail a checkout. Swallow and succeed; the daemon
		// (or a manual `magus graph build`) will catch up.
		slog.DebugContext(ctx, "server sync: submit failed", slog.String("error", err.Error()))
		return nil
	}
	if inv != "" { // empty inv = the daemon coalesced this into an already-running sync
		fmt.Fprintf(os.Stderr, "magus: syncing the graph in the background (job %s)\n", inv)
	}
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
	installed, err := installer.InstallRefreshHook(ctx, root, "magus server sync")
	if err != nil {
		slog.WarnContext(ctx, "server start: could not install VCS refresh hook", slog.String("error", err.Error()))
		return
	}
	if len(installed) > 0 {
		fmt.Fprintf(os.Stderr, "magus: installed %s refresh hook(s) [%s]; history changes now reconcile the graph automatically\n", res.Name, strings.Join(installed, ", "))
	}
}
