package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/config"
	internalmcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/types"
)

// mcpAddress returns the MCP host:port for a given MCP config, falling back to the
// default when unset. It takes the config explicitly (rather than reading globalCfg)
// so callers that already hold a config.MCP (including tests) resolve the address
// without touching package state.
func mcpAddress(mcp config.MCP) string {
	if mcp.Address != "" {
		return mcp.Address
	}
	return internalmcp.DefaultAddress
}

// mcpAddrPort parses the configured MCP address, falling back to DefaultAddress. The
// config validator guarantees the string is a valid host:port, so this parse should
// never fail in practice.
func mcpAddrPort() (netip.AddrPort, error) {
	return netip.ParseAddrPort(mcpAddress(globalCfg.MCP))
}

// mcpAddrString returns the configured MCP address as a host:port string, falling back
// to the default. Used by buildServerInfo so the bridge doctor check knows which
// address to probe.
func mcpAddrString() string {
	return mcpAddress(globalCfg.MCP)
}

// mcpCmd serves MCP over stdin and stdout for the agent host that launched it, against the
// workspace it was launched in. It serves whoever runs it, a person at a terminal included:
// the stderr line serveMCPStdio prints is what tells that person what they started.
func mcpCmd(ctx context.Context, root string, args []string) error {
	rest, err := cmdParse("mcp", args, func(fs *flag.FlagSet) {
		fs.Usage = mcpUsage
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus mcp: takes no arguments (got %q)", rest[0])
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	return serveMCPStdio(ctx, m, os.Stdin, os.Stdout, os.Stderr)
}

// mcpUsage is `magus mcp --help`: the stdio registration a host needs, then the server's
// HTTP endpoint for a client that wants one long-lived server. It names no host: each
// client's config dialect belongs in docs/guides/integrations/mcp.md, where a change to one
// is not a magus release.
func mcpUsage() {
	w := os.Stderr
	fmt.Fprintln(w, "Usage: magus mcp")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Serve MCP over stdin and stdout for the agent host that launched this process,")
	fmt.Fprintln(w, "against the workspace it was launched in. No server and no token: the caller")
	fmt.Fprintln(w, "is the local process the host started, holding mcp=write. It stops when the")
	fmt.Fprintln(w, "host closes stdin.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Register it with an MCP client as a stdio server:")
	fmt.Fprintln(w, "  command  magus")
	fmt.Fprintln(w, `  args     ["mcp"]`)
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "magus server start also serves MCP over Streamable HTTP, for one")
	fmt.Fprintln(w, "long-lived server shared by several clients:")
	fmt.Fprintf(w, "  url        http://%s/mcp\n", mcpAddrString())
	fmt.Fprintln(w, "  auth       Authorization: Bearer <token>, minted with")
	fmt.Fprintln(w, "             magus config mcp connector create --name <client> --expires 366d")
	fmt.Fprintln(w, "  check      magus status --probe=liveness,mcp")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Per-client configuration: docs/guides/integrations/mcp.md")
}

// serveMCPStdio serves MCP for m with wire as the protocol's output. For as long as it
// serves, os.Stdout points at diag, so a stray print or a child process handed os.Stdout
// lands on stderr rather than between two frames a host is parsing.
func serveMCPStdio(ctx context.Context, m *magus.Magus, in io.Reader, wire io.Writer, diag *os.File) error {
	addr, err := mcpAddrPort()
	if err != nil {
		return fmt.Errorf("invalid mcp.address: %w", err)
	}
	stdout := os.Stdout
	os.Stdout = diag
	defer func() { os.Stdout = stdout }()

	fmt.Fprintf(diag, "magus: serving MCP over stdio for %s; close stdin or press Ctrl+C to stop (magus mcp --help shows how to register it)\n", m.Root())
	return internalmcp.ServeStdio(ctx, internalmcp.Options{
		Magus:    m,
		Logger:   slog.Default(),
		Version:  version,
		Build:    types.BuildInfo{Version: version, Commit: commit, Date: buildDate},
		Config:   globalCfg,
		HTTPAddr: addr,
		// The store the server and `magus diff` use, so an agent joins the session a person
		// already has open.
		DiffSessions: changeset.NewStore(m.CacheDir()),
	}, in, wire)
}

// publishServerTrailBase resolves the server-wide activity-trail base and publishes it, so
// background-job recording and the maintenance scheduler land in the same trail the MCP
// handler writes and the ActivityService reads.
//
// ResolveCacheDir is the narrow path: it reads config without discovering projects or
// evaluating magusfiles, so the value is the bridge Magus's CacheDir without paying a
// workspace load to learn it, which is what lets this run ahead of the MCP gate. A root
// that will not resolve leaves the base empty, and every writer already treats an empty
// base as "drop the record, best-effort".
func publishServerTrailBase() {
	root, err := magus.FindRoot("")
	if err != nil {
		slog.Warn("[AGENT] no workspace root; background jobs and scheduled maintenance will not be recorded", slog.String("error", err.Error()))
		return
	}
	base, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		slog.Warn("[AGENT] cache dir unresolvable; background jobs and scheduled maintenance will not be recorded", slog.String("error", err.Error()))
		return
	}
	serverTrailBase = base
	serverJobStore = job.NewStore(job.Location{CacheDir: base, Root: root})
}

// startBridge opens the server's own workspace, keeps its graph and symbol indexes
// current, and serves MCP, the console and the APIs over loopback HTTP, and MCP and the APIs
// on the server's own socket, when mcp.enabled allows. Called from
// the server surface. cancel is the CancelFunc for the server's context; it is called if
// the HTTP server exits for any reason other than ctx cancellation, so the server shuts
// down rather than running on with MCP unavailable.
//
// The watch belongs to the server, not to MCP: turning off one integration must not turn
// off symbol indexing, the same coupling that once left the trail base empty.
func startBridge(ctx context.Context, cancel context.CancelFunc, tel observability.Provider) {
	// Before any gate, deliberately. The trail base is the workspace cache dir and has
	// nothing to do with MCP, but it used to be published below the MCP gate, so
	// mcp.enabled: false left it empty, the maintenance scheduler bailed at every tick,
	// and no background job was ever recorded.
	publishServerTrailBase()
	mcpOn := globalCfg.MCP.Enabled == nil || *globalCfg.MCP.Enabled
	addr, err := mcpAddrPort()
	if mcpOn && err != nil {
		slog.Error("[AGENT] MCP skipped: invalid MCP address", slog.String("error", err.Error()))
		mcpOn = false
	}
	// The bridge Magus MUST share the server's single provider (WithProvider) so the
	// /dashboard derived metrics (GetMetrics / StreamMetrics) read the counters that
	// per-workspace registry builds actually recorded, not this bridge's own empty
	// ManualReader. If no shared provider was supplied, fall back to a bridge-local
	// collector so the endpoint is still non-empty; one-shot CLI runs leave metrics off.
	opts := []magus.Option{magus.WithMetricsCollection()}
	if tel != nil {
		opts[0] = magus.WithProvider(tel)
	}
	if serverRegistry != nil && serverRegistry.broker != nil && globalCfg.Broker.Resolved() != types.BrokerOff {
		opts = append(opts, magus.WithBroker(serverRegistry.broker))
	}
	m, err := loadMagus(ctx, "", opts...)
	if err != nil {
		root, rerr := magus.FindRoot("")
		if !mcpOn || rerr != nil || serverRegistry == nil {
			slog.Warn("[AGENT] workspace unavailable; no MCP, console or watch for it", slog.String("error", err.Error()))
			return
		}
		// Serve anyway: a console and an agent that can connect and read the diagnostic
		// beat a server with nothing listening. The registry holds the failure and retries
		// once a source changes; the full surface takes over when that load succeeds.
		slog.Warn("[AGENT] workspace failed to load; serving its failure until a source changes",
			slog.String("root", root), slog.String("error", err.Error()))
		serverRegistry.failBridge(root, err)
		serverHTTPAddr.Store(addr.String())
		go serveUnloadedBridge(ctx, cancel, root, addr)
		return
	}
	// Register this bridge workspace in the per-workspace registry the WorkspaceLister reports,
	// so /readyz counts the server's own workspace as loaded instead of waiting for an
	// adopted run to populate the pool. Without this the server ran two workspace pools and
	// readyz reported "no workspaces loaded" even after a live MCP query used this same Magus.
	if serverRegistry != nil {
		serverRegistry.adoptBridge(m.Root(), m)
	}
	startWatch(ctx, m)
	if !mcpOn {
		return
	}
	serverHTTPAddr.Store(addr.String())
	serveBridge(ctx, cancel, m, addr)
}

// startWatch keeps m's knowledge graph and symbol indexes current for the server's
// life: the watcher invalidates the warm graph on source changes, so queries answer from
// memory without re-parsing every magusfile per call, and the indexer re-runs SCIP in the
// background, throttled and idle-gated. Neither is fatal: queries fall back to a
// cache-first rebuild, and symbols go stale until a manual `magus run ::scip`.
func startWatch(ctx context.Context, m *magus.Magus) {
	if _, werr := m.WatchKnowledgeGraph(ctx); werr != nil {
		slog.Warn("[AGENT] knowledge-graph watcher unavailable; queries will rebuild per call", slog.String("error", werr.Error()))
	}
	if _, werr := m.WatchSymbolIndexing(ctx); werr != nil {
		slog.Warn("[AGENT] symbol auto-indexer unavailable; symbol indexes will not refresh automatically", slog.String("error", werr.Error()))
		return
	}
	watching(m.Root())
}

// serveUnloadedBridge serves the server's surface for a workspace that failed to load
// until the registry reports it ACTIVE, then hands the listener to the full surface over
// that workspace.
func serveUnloadedBridge(ctx context.Context, cancel context.CancelFunc, root string, addr netip.AddrPort) {
	status := serverSnapshot(os.Getenv(proc.SocketEnv))
	srvCtx, stop := context.WithCancel(ctx)
	defer stop()
	d := server.NewUnloaded(internalmcp.Options{
		Logger:     slog.Default(),
		Version:    version,
		Build:      types.BuildInfo{Version: version, Commit: commit, Date: buildDate},
		Config:     globalCfg,
		HTTPAddr:   addr,
		StatusBase: buildStatusBase(),
		HealthRoutes: healthRoutes(status, readinessExtras{
			services: bridgeServices,
		}),
	}, server.Unloaded{
		Root: root,
		Err:  func() rpcerr.Error { return serverRegistry.unavailable(root) },
	}, bridgeServerOptions()...)
	done := make(chan error, 1)
	go func() { done <- d.Serve(srvCtx) }()
	active := make(chan *magus.Magus, 1)
	go func() { active <- serverRegistry.awaitActive(srvCtx, root) }()
	select {
	case err := <-done:
		if err != nil && ctx.Err() == nil {
			slog.Error("[AGENT] MCP HTTP server failed; initiating server shutdown", slog.String("error", err.Error()))
			cancel()
		}
	case m := <-active:
		stop()
		<-done // the listener is released before the full surface binds it
		if m == nil {
			return
		}
		slog.Info("[AGENT] workspace loaded; serving the full surface", slog.String("root", root))
		startWatch(ctx, m)
		serveBridge(ctx, cancel, m, addr)
	}
}

// bridgeServices is the hosted-services source the console and /readyz read: the broker's,
// since the broker hosts them. Nil when no broker answers. Asking holds nothing, so it
// never keeps the broker awake.
func bridgeServices() []types.StatusService {
	if st := bridgeBroker(); st != nil {
		return st.Services
	}
	return nil
}

// bridgeBroker is the broker's status for the console, nil when none answers.
func bridgeBroker() *types.StatusBroker {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	st, err := broker.QueryStatus(ctx, broker.DefaultAddr())
	if err != nil {
		return nil
	}
	return &st
}

// healthRoutes are the k8s probes the server serves beside MCP, on the same port.
// /healthz aliases /livez (liveness): a liveness probe must not depend on warm-up state,
// or it would crash-loop pods. /readyz is the workspace-loaded readiness gate, and carries
// component-level detail so the console dashboard can render per-subsystem health.
func healthRoutes(status statusFunc, extras readinessExtras) map[string]http.Handler {
	return map[string]http.Handler{
		"/livez":   healthHTTPHandler(probeLiveness, status),
		"/readyz":  readinessHTTPHandler(status, extras),
		"/healthz": healthHTTPHandler(probeLiveness, status),
	}
}

// bridgeServerOptions wires the server-wide registries (runs, hosted services, every
// workspace's activity) into the bridge's HTTP server. Each is nil for a bridge started without
// the multi-workspace server, and the option is then left unset.
func bridgeServerOptions() []server.Option {
	var opts []server.Option
	// The live-run registry (built by startServer) backs the dashboard's runs view;
	// without it the status report simply omits runs.
	if serverRuns != nil {
		opts = append(opts, server.WithRuns(serverRuns.Snapshot))
	}
	// The broker's status backs the dashboard's capacity and services views the same way.
	opts = append(opts, server.WithBrokerStatus(bridgeBroker))
	// The activity view is server-wide, so it reads every loaded workspace's trail, not just this
	// bridge's: an agent hook runs as a short-lived client outside the server and writes to ITS
	// workspace's cache dir, so a bridge-only view misses every other workspace's agent activity.
	// Same registry the WorkspaceLister reports from.
	if serverRegistry != nil {
		opts = append(opts, server.WithActivityWorkspaces(serverRegistry.activityWorkspaces))
	}
	// The server's own socket carries MCP and the APIs beside its control routes.
	if procServer != nil {
		opts = append(opts, server.WithSocket(procServer))
	}
	return opts
}

// serveBridge serves MCP, the console and the APIs over HTTP for the loaded bridge
// workspace m. The watch is started separately (startWatch), since it outlives MCP.
func serveBridge(ctx context.Context, cancel context.CancelFunc, m *magus.Magus, addr netip.AddrPort) {
	// Capture the server's own socket now (set by startServer) so the health handlers
	// query this server, not whatever a per-request discovery scan happens to find.
	status := serverSnapshot(os.Getenv(proc.SocketEnv))
	m.SetServer(server.New(internalmcp.Options{
		Magus:      m,
		Logger:     slog.Default(),
		Version:    version,
		Build:      types.BuildInfo{Version: version, Commit: commit, Date: buildDate},
		Config:     globalCfg,
		HTTPAddr:   addr,
		StatusBase: buildStatusBase(),
		// ONE diff-session store for the whole server, constructed here because this is
		// where the server's dependencies are assembled. The console's /api/v1/diff routes
		// and the magus_diff MCP tool both read it, and that sharing IS the pairing: a
		// person opens a diff, an agent joins the session they started.
		DiffSessions: changeset.NewStore(m.CacheDir()),
		// The same store the OnJobDone callback completes rows in, so the server's own
		// jobs and the delegated ones are one book with one lock.
		Jobs: serverJobStore,
		// Health endpoints share this HTTP server so k8s probes hit the
		// same port as MCP. Set MAGUS_MCP_ADDRESS=0.0.0.0:7391 (or mcp.address)
		// so the kubelet can reach them (default 127.0.0.1 is pod-local).
		HealthRoutes: healthRoutes(status, readinessExtras{
			symbolIndexes:  m.SymbolIndexStatus,
			services:       bridgeServices,
			knowledgeGraph: m.KnowledgeGraphHealthy,
		}),
	}, bridgeServerOptions()...))
	go func() {
		err := m.Serve(ctx)
		if err != nil && ctx.Err() == nil {
			// Serve exiting due to ctx cancellation is normal shutdown.
			// Any other error means MCP is gone while the server is still up —
			// clients would receive no response indefinitely. Cancel the server
			// context to trigger a clean restart by the process supervisor.
			slog.Error("[AGENT] MCP HTTP server failed; initiating server shutdown", slog.String("error", err.Error()))
			cancel()
		}
	}()
}
