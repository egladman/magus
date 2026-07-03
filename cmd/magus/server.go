package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/proc"
)

func serverCmd(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		serverUsage()
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "start":
		return serverStart(ctx, rest)
	case "stop":
		return serverStop(ctx, rest)
	default:
		return fmt.Errorf("magus server: unknown target %q (want start or stop); use `magus status` to inspect daemon state", sub)
	}
}

func serverUsage() {
	fmt.Fprintln(os.Stderr, "usage: magus server <start|stop> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Targets:")
	fmt.Fprintln(os.Stderr, "  start   start a persistent daemon and block until stopped")
	fmt.Fprintln(os.Stderr, "  stop    send a graceful shutdown request to a running daemon")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Use `magus status` to inspect daemon pool state and check reachability.")
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
	fmt.Fprintln(os.Stderr, "magus: send SIGINT / SIGTERM or run `magus server stop` to shut down")

	// Start the MCP HTTP server alongside the daemon so MCP clients can
	// connect without a separate process. No-op when not compiled with -tags mcp
	// or when mcp.enabled=false.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	startMCPWithDaemon(ctx, cancel)

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
