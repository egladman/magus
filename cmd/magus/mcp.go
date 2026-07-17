package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/daemon"
	internalmcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/types"
)

// mcpAddress returns the MCP host:port for a given MCP config, falling back to the
// default when unset. It takes the config explicitly (rather than reading globalCfg)
// so callers that already hold a config.MCP - including tests - resolve the address
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
	fmt.Fprintf(os.Stderr, "The endpoint requires a bearer token. Print it with:\n  magus config mcp token print\n\n")
	fmt.Fprintf(os.Stderr, "Claude Desktop / IDE configuration:\n")
	fmt.Fprintf(os.Stderr, "  { \"type\": \"streamable-http\", \"url\": \"http://%s/mcp\",\n", addr)
	fmt.Fprintf(os.Stderr, "    \"headers\": { \"Authorization\": \"Bearer <token>\" } }\n")
	return nil
}

// startMCPWithDaemon starts the MCP HTTP server as a background goroutine
// alongside the daemon. Called from serverStart; no-op when MCP is disabled.
// cancel is the CancelFunc for the daemon's context; it is called if ServeHTTP
// exits for any reason other than ctx cancellation, so the daemon shuts down
// rather than continuing to run with MCP unavailable.
func startMCPWithDaemon(ctx context.Context, cancel context.CancelFunc, tel observability.Provider) {
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
	// per-workspace registry builds actually recorded - not this bridge's own empty
	// ManualReader. If no shared provider was supplied (bridge started without the
	// multi-workspace daemon), fall back to a bridge-local collector so the endpoint is
	// still non-empty; one-shot CLI runs leave metrics off entirely.
	metricsOpt := magus.WithMetricsCollection()
	if tel != nil {
		metricsOpt = magus.WithProvider(tel)
	}
	m, err := loadMagus(ctx, "", metricsOpt)
	if err != nil {
		slog.Warn("[AGENT] skipping: workspace unavailable", slog.String("error", err.Error()))
		return
	}
	// Publish the daemon-wide activity-trail base so background-job recording lands in the same
	// trail the MCP handler writes and the ActivityService reads (both off this Magus's cache dir).
	daemonTrailBase = m.CacheDir()
	// Keep a warm knowledge graph for MCP queries: the watcher invalidates it on
	// source changes, so query/explain/path/stats answer from memory without
	// re-parsing every magusfile per call. Non-fatal if it cannot start - the
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
	// The live-run registry (built by startMultiWorkspaceDaemon) backs the dashboard's
	// runs view. It is nil for a bridge started without the multi-workspace daemon;
	// WithRuns then goes unset and the status report simply omits runs.
	var daemonOpts []daemon.Option
	if daemonRuns != nil {
		daemonOpts = append(daemonOpts, daemon.WithRuns(daemonRuns.Snapshot))
	}
	// The hosted-services registry (built by startMultiWorkspaceDaemon) backs the
	// dashboard's services view the same way daemonRuns backs its runs view. Nil for a
	// bridge started without the multi-workspace daemon, leaving StatusReport.Services empty.
	if daemonServices != nil {
		daemonOpts = append(daemonOpts, daemon.WithServices(func() []types.StatusService {
			return serviceStatuses(daemonServices)
		}))
	}
	m.SetDaemon(daemon.New(internalmcp.Options{
		Magus:      m,
		Logger:     slog.Default(),
		Version:    version,
		Build:      types.BuildInfo{Version: version, Commit: commit, Date: buildDate},
		Config:     globalCfg,
		HTTPAddr:   addr,
		StatusBase: buildStatusBase(),
		// Health endpoints share this HTTP server so k8s probes hit the
		// same port as MCP. Set MAGUS_MCP_ADDRESS=0.0.0.0:7391 (or mcp.address)
		// so the kubelet can reach them (default 127.0.0.1 is pod-local).
		// /healthz aliases /livez (liveness): a liveness probe must not
		// depend on warm-up state, or it would crash-loop pods. Use
		// /readyz for the workspace-loaded readiness gate.
		HealthRoutes: map[string]http.Handler{
			"/livez": healthHTTPHandler(probeLiveness, status),
			// /readyz carries component-level detail (symbol-index freshness, hosted
			// services, the warm knowledge-graph watcher) alongside the same pass/fail
			// gate, so the console dashboard can render real per-subsystem health
			// instead of a bare text line. m, daemonServices, and the warm graph are
			// all in scope right here, so the wiring stays local to this call.
			"/readyz": readinessHTTPHandler(status, readinessExtras{
				symbolIndexes: m.SymbolIndexStatus,
				services: func() []types.StatusService {
					if daemonServices == nil {
						return nil
					}
					return serviceStatuses(daemonServices)
				},
				knowledgeGraph: m.KnowledgeGraphHealthy,
			}),
			"/healthz": healthHTTPHandler(probeLiveness, status),
		},
	}, daemonOpts...))
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
