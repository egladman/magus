//go:build mcp

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"

	internalmcp "github.com/egladman/magus/internal/handler/mcp"
)

// mcpIsCompiled is true when the binary was built with -tags mcp.
const mcpIsCompiled = true

// mcpAddrPort parses the MCP address from config, falling back to
// DefaultAddress. The config validator guarantees the string is a valid
// host:port, so this parse should never fail in practice.
func mcpAddrPort() (netip.AddrPort, error) {
	raw := globalCfg.MCP.Address
	if raw == "" {
		raw = internalmcp.DefaultAddress
	}
	return netip.ParseAddrPort(raw)
}

// mcpAddrString returns the configured MCP address as a host:port string,
// falling back to the default. Used by buildDaemonInfo so the bridge doctor
// check knows which address to probe.
func mcpAddrString() string {
	raw := globalCfg.MCP.Address
	if raw == "" {
		raw = internalmcp.DefaultAddress
	}
	return raw
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
func startMCPWithDaemon(ctx context.Context, cancel context.CancelFunc) {
	if globalCfg.MCP.Enabled != nil && !*globalCfg.MCP.Enabled {
		return
	}
	addr, err := mcpAddrPort()
	if err != nil {
		slog.Error("[AGENT] skipping: invalid MCP address", slog.String("error", err.Error()))
		return
	}
	m, err := loadMagus(ctx, "")
	if err != nil {
		slog.Warn("[AGENT] skipping: workspace unavailable", slog.String("error", err.Error()))
		return
	}
	// Keep a warm knowledge graph for MCP queries: the watcher invalidates it on
	// source changes, so query/explain/path/stats answer from memory without
	// re-parsing every magusfile per call. Non-fatal if it cannot start - the
	// tools fall back to a cache-first rebuild per call (equally fresh). The
	// watcher lives for the daemon's context.
	if _, werr := m.WatchKnowledgeGraph(ctx); werr != nil {
		slog.Warn("[AGENT] knowledge-graph watcher unavailable; MCP queries will rebuild per call", slog.String("error", werr.Error()))
	}
	// Capture the daemon's own socket now (set by startMultiWorkspaceDaemon)
	// so the health handlers query this daemon, not whatever a per-request
	// discovery scan happens to find.
	status := daemonStatus(os.Getenv("MAGUS_DAEMON_SOCKET"))
	go func() {
		err := internalmcp.ServeHTTP(ctx, internalmcp.ServerOptions{
			Magus:      m,
			Logger:     slog.Default(),
			Version:    version,
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
				"/livez":   healthHTTPHandler(probeLiveness, status),
				"/readyz":  healthHTTPHandler(probeReadiness, status),
				"/healthz": healthHTTPHandler(probeLiveness, status),
			},
		})
		if err != nil && ctx.Err() == nil {
			// ServeHTTP exiting due to ctx cancellation is normal shutdown.
			// Any other error means MCP is gone while the daemon is still up —
			// clients would receive no response indefinitely. Cancel the daemon
			// context to trigger a clean restart by the process supervisor.
			slog.Error("[AGENT] MCP HTTP server failed; initiating daemon shutdown", slog.String("error", err.Error()))
			cancel()
		}
	}()
}
