package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/daemon"
	internalmcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/rpcerr"
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
// to the default. Used by buildDaemonInfo so the bridge doctor check knows which
// address to probe.
func mcpAddrString() string {
	return mcpAddress(globalCfg.MCP)
}

// mcpCmd prints instructions for using the MCP server.
// MCP is no longer a standalone command — it is served by `magus server start`.
func mcpCmd(_ context.Context, _ []string) error {
	addr, err := mcpAddrPort()
	if err != nil {
		return fmt.Errorf("invalid mcp.address: %w", err)
	}
	fmt.Fprintf(os.Stderr, "MCP is served by the magus daemon, not as a standalone command.\n\n")
	fmt.Fprintf(os.Stderr, "Start the daemon:\n  magus server start\n\n")
	fmt.Fprintf(os.Stderr, "MCP endpoint (Streamable HTTP):\n  http://%s/mcp\n\n", addr)
	fmt.Fprintf(os.Stderr, "The endpoint requires a bearer token. Print it with:\n  magus config token print\n\n")
	// Everything above is what EVERY client needs: transport, URL, credential.
	// What each client does with them (a TOML table, a JSON object, an env var
	// read at launch) is that client's dialect, and it belongs in documentation
	// the reader owns, not in this binary. Naming clients here would make a
	// change to any one of them a magus release. See docs/guides/integrations/mcp.md.
	fmt.Fprintf(os.Stderr, "Point your MCP client at that endpoint. Most take three settings:\n")
	fmt.Fprintf(os.Stderr, "  transport  streamable-http\n")
	fmt.Fprintf(os.Stderr, "  url        http://%s/mcp\n", addr)
	fmt.Fprintf(os.Stderr, "  auth       Authorization: Bearer <token>, or a token env var\n\n")
	fmt.Fprintf(os.Stderr, "Many clients read a token from the environment at launch:\n")
	fmt.Fprintf(os.Stderr, "  export MAGUS_MCP_TOKEN=\"$(magus config token print)\"\n\n")
	fmt.Fprintf(os.Stderr, "Then confirm the endpoint is actually serving:\n")
	fmt.Fprintf(os.Stderr, "  magus status --probe=liveness,mcp\n\n")
	fmt.Fprintf(os.Stderr, "Per-client configuration, and what to re-register when mcp.address\n")
	fmt.Fprintf(os.Stderr, "changes, are documented in: docs/guides/integrations/mcp.md\n")
	// mcp is a retired verb: everything useful it could do is printed above, and
	// nothing was run, so it exits like any other misuse (errUsage's 2) rather
	// than 0: a script that still invokes `magus mcp` expecting a server should
	// see a failure, not a silent no-op success.
	return errSilent{exitCode: exitUsage}
}

// publishDaemonTrailBase resolves the daemon-wide activity-trail base and publishes it, so
// background-job recording and the maintenance scheduler land in the same trail the MCP
// handler writes and the ActivityService reads.
//
// ResolveCacheDir is the narrow path: it reads config without discovering projects or
// evaluating magusfiles, so the value is the bridge Magus's CacheDir without paying a
// workspace load to learn it, which is what lets this run ahead of the MCP gate. A root
// that will not resolve leaves the base empty, and every writer already treats an empty
// base as "drop the record, best-effort".
func publishDaemonTrailBase() {
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
	daemonTrailBase = base
	daemonJobStore = job.NewStore(job.Location{CacheDir: base, Root: root})
}

// startMCPWithDaemon starts the MCP HTTP server as a background goroutine
// alongside the daemon. Called from serverStart; no-op when MCP is disabled.
// cancel is the CancelFunc for the daemon's context; it is called if ServeHTTP
// exits for any reason other than ctx cancellation, so the daemon shuts down
// rather than continuing to run with MCP unavailable.
func startMCPWithDaemon(ctx context.Context, cancel context.CancelFunc, tel observability.Provider) {
	// Before the gate, deliberately. The trail base is the workspace cache dir and has
	// nothing to do with MCP, but it used to be published from below this return, so
	// mcp.enabled: false left it empty, the maintenance scheduler bailed at every tick,
	// and no background job was ever recorded. Turning off one integration silently
	// turned off all scheduled maintenance.
	publishDaemonTrailBase()
	if globalCfg.MCP.Enabled != nil && !*globalCfg.MCP.Enabled {
		return
	}
	addr, err := mcpAddrPort()
	if err != nil {
		slog.Error("[AGENT] skipping: invalid MCP address", slog.String("error", err.Error()))
		return
	}
	// The bridge Magus MUST share the daemon's single provider (WithProvider) so the
	// /dashboard derived metrics (GetMetrics / StreamMetrics) read the counters that
	// per-workspace registry builds actually recorded, not this bridge's own empty
	// ManualReader. If no shared provider was supplied (bridge started without the
	// multi-workspace daemon), fall back to a bridge-local collector so the endpoint is
	// still non-empty; one-shot CLI runs leave metrics off entirely.
	metricsOpt := magus.WithMetricsCollection()
	if tel != nil {
		metricsOpt = magus.WithProvider(tel)
	}
	m, err := loadMagus(ctx, "", metricsOpt)
	if err != nil {
		root, rerr := magus.FindRoot("")
		if rerr != nil || daemonRegistry == nil {
			slog.Warn("[AGENT] skipping: workspace unavailable", slog.String("error", err.Error()))
			return
		}
		// Serve anyway: a console and an agent that can connect and read the diagnostic
		// beat a daemon with nothing listening. The registry holds the failure and retries
		// once a source changes; the full surface takes over when that load succeeds.
		slog.Warn("[AGENT] workspace failed to load; serving its failure until a source changes",
			slog.String("root", root), slog.String("error", err.Error()))
		daemonRegistry.failBridge(root, err)
		go serveUnloadedBridge(ctx, cancel, root, addr)
		return
	}
	// Register this bridge workspace in the per-workspace registry the WorkspaceLister reports,
	// so /readyz counts the daemon's own MCP workspace as loaded instead of waiting for an
	// adopted run to populate the pool. Without this the daemon ran two workspace pools and
	// readyz reported "no workspaces loaded" even after a live MCP query used this same Magus.
	if daemonRegistry != nil {
		daemonRegistry.adoptBridge(m.Root(), m)
	}
	serveBridge(ctx, cancel, m, addr)
}

// serveUnloadedBridge serves the daemon's surface for a workspace that failed to load
// until the registry reports it ACTIVE, then hands the listener to the full surface over
// that workspace.
func serveUnloadedBridge(ctx context.Context, cancel context.CancelFunc, root string, addr netip.AddrPort) {
	status := daemonStatus(os.Getenv("MAGUS_DAEMON_SOCKET"))
	srvCtx, stop := context.WithCancel(ctx)
	defer stop()
	d := daemon.NewUnloaded(internalmcp.Options{
		Logger:     slog.Default(),
		Version:    version,
		Build:      types.BuildInfo{Version: version, Commit: commit, Date: buildDate},
		Config:     globalCfg,
		HTTPAddr:   addr,
		StatusBase: buildStatusBase(),
		HealthRoutes: healthRoutes(status, readinessExtras{
			services: bridgeServices,
		}),
	}, daemon.Unloaded{
		Root: root,
		Err:  func() rpcerr.Error { return daemonRegistry.unavailable(root) },
	}, bridgeDaemonOptions()...)
	done := make(chan error, 1)
	go func() { done <- d.Serve(srvCtx) }()
	active := make(chan *magus.Magus, 1)
	go func() { active <- daemonRegistry.awaitActive(srvCtx, root) }()
	select {
	case err := <-done:
		if err != nil && ctx.Err() == nil {
			slog.Error("[AGENT] MCP HTTP server failed; initiating daemon shutdown", slog.String("error", err.Error()))
			cancel()
		}
	case m := <-active:
		stop()
		<-done // the listener is released before the full surface binds it
		if m == nil {
			return
		}
		slog.Info("[AGENT] workspace loaded; serving the full surface", slog.String("root", root))
		serveBridge(ctx, cancel, m, addr)
	}
}

// bridgeServices is the hosted-services source the console and /readyz read; nil when the
// daemon hosts none.
func bridgeServices() []types.StatusService {
	if daemonServices == nil {
		return nil
	}
	return serviceStatuses(daemonServices)
}

// healthRoutes are the k8s probes the daemon serves beside MCP, on the same port.
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

// bridgeDaemonOptions wires the daemon-wide registries (runs, hosted services, every
// workspace's activity) into the bridge's daemon. Each is nil for a bridge started without
// the multi-workspace daemon, and the option is then left unset.
func bridgeDaemonOptions() []daemon.Option {
	var opts []daemon.Option
	// The live-run registry (built by startMultiWorkspaceDaemon) backs the dashboard's
	// runs view; without it the status report simply omits runs.
	if daemonRuns != nil {
		opts = append(opts, daemon.WithRuns(daemonRuns.Snapshot))
	}
	// The hosted-services registry backs the dashboard's services view the same way.
	if daemonServices != nil {
		opts = append(opts, daemon.WithServices(bridgeServices))
	}
	// The activity view is daemon-wide, so it reads every loaded workspace's trail, not just this
	// bridge's: an agent hook runs as a short-lived client outside the daemon and writes to ITS
	// workspace's cache dir, so a bridge-only view misses every other workspace's agent activity.
	// Same registry the WorkspaceLister reports from.
	if daemonRegistry != nil {
		opts = append(opts, daemon.WithActivityWorkspaces(daemonRegistry.activityWorkspaces))
	}
	return opts
}

// serveBridge serves the full daemon surface over the loaded bridge workspace m.
func serveBridge(ctx context.Context, cancel context.CancelFunc, m *magus.Magus, addr netip.AddrPort) {
	// Keep a warm knowledge graph for MCP queries: the watcher invalidates it on
	// source changes, so query/explain/path/stats answer from memory without
	// re-parsing every magusfile per call. Non-fatal if it cannot start: the
	// tools fall back to a cache-first rebuild per call (equally fresh). The
	// watcher lives for the daemon's context.
	if _, werr := m.WatchKnowledgeGraph(ctx); werr != nil {
		slog.Warn("[AGENT] knowledge-graph watcher unavailable; MCP queries will rebuild per call", slog.String("error", werr.Error()))
	}
	// Keep each symbol-capable project's SCIP index fresh in the background, so
	// symbol queries and `magus refs` see current code without a manual scip run.
	// Throttled and idle-gated; non-fatal if the watcher cannot start (symbols then
	// go stale until a manual `magus run ::scip`).
	if _, werr := m.WatchSymbolIndexing(ctx); werr != nil {
		slog.Warn("[AGENT] symbol auto-indexer unavailable; symbol indexes will not refresh automatically", slog.String("error", werr.Error()))
	}
	// Capture the daemon's own socket now (set by startMultiWorkspaceDaemon)
	// so the health handlers query this daemon, not whatever a per-request
	// discovery scan happens to find.
	status := daemonStatus(os.Getenv("MAGUS_DAEMON_SOCKET"))
	m.SetDaemon(daemon.New(internalmcp.Options{
		Magus:      m,
		Logger:     slog.Default(),
		Version:    version,
		Build:      types.BuildInfo{Version: version, Commit: commit, Date: buildDate},
		Config:     globalCfg,
		HTTPAddr:   addr,
		StatusBase: buildStatusBase(),
		// ONE diff-session store for the whole daemon, constructed here because this is
		// where the daemon's dependencies are assembled. The console's /api/v1/diff routes
		// and the magus_diff MCP tool both read it, and that sharing IS the pairing: a
		// person opens a diff, an agent joins the session they started.
		DiffSessions: changeset.NewStore(m.CacheDir()),
		// The same store the OnJobDone callback completes rows in, so the daemon's own
		// jobs and the delegated ones are one book with one lock.
		Jobs: daemonJobStore,
		// Health endpoints share this HTTP server so k8s probes hit the
		// same port as MCP. Set MAGUS_MCP_ADDRESS=0.0.0.0:7391 (or mcp.address)
		// so the kubelet can reach them (default 127.0.0.1 is pod-local).
		HealthRoutes: healthRoutes(status, readinessExtras{
			symbolIndexes:  m.SymbolIndexStatus,
			services:       bridgeServices,
			knowledgeGraph: m.KnowledgeGraphHealthy,
		}),
	}, bridgeDaemonOptions()...))
	go func() {
		err := m.ServeDaemon(ctx)
		if err != nil && ctx.Err() == nil {
			// ServeDaemon exiting due to ctx cancellation is normal shutdown.
			// Any other error means MCP is gone while the daemon is still up —
			// clients would receive no response indefinitely. Cancel the daemon
			// context to trigger a clean restart by the process supervisor.
			slog.Error("[AGENT] MCP HTTP server failed; initiating daemon shutdown", slog.String("error", err.Error()))
			cancel()
		}
	}()
}
