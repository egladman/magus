// Package daemon assembles the magus daemon HTTP server: it mounts the MCP
// Streamable-HTTP handler, the k8s health routes, and the browser Graph
// Explorer web bridge onto one loopback listener, applying the shared bearer
// and DNS-rebind guards. It is the composition point that ties together
// internal/handler/mcp, internal/httpx, and internal/service/console so
// neither the handler/mcp package nor the root magus package has to.
//
// The CLI injects a *Daemon into the root magus package via
// magus.SetDaemon; magus.ServeDaemon then delegates here. That indirection
// keeps magus free of an import cycle (daemon depends on magus, not vice versa).
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/file/watch"
	graphhandler "github.com/egladman/magus/internal/handler/graph"
	mcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/handler/status"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/service/console"
)

// Daemon assembles and runs the daemon HTTP server from a set of MCP server
// options. It satisfies magus.Daemon.
type Daemon struct {
	opts mcp.Options
}

// New returns a Daemon that will serve the MCP endpoint (plus health routes and
// the web bridge) described by opts.
func New(opts mcp.Options) *Daemon {
	return &Daemon{opts: opts}
}

// Serve starts the daemon HTTP server, blocking until ctx is cancelled or the
// server fails. Multiple MCP clients can connect concurrently.
func (s *Daemon) Serve(ctx context.Context) error {
	opts := s.opts

	// Logger and bind address come from the exported option fields, mirroring
	// the fallbacks the handler package applies internally.
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	addr := opts.HTTPAddr
	if !addr.IsValid() {
		addr = netip.MustParseAddrPort(mcp.DefaultAddress)
	}

	// Provision the retrievable cli token before serving. Fail closed: if it
	// can't be loaded or generated, the MCP endpoint never comes up. The guard
	// re-evaluates auth.VerifyBearer on each request (which re-reads both the cli
	// token and the named connector store from disk), so a rotate, create, or
	// revoke takes effect without a daemon restart.
	if _, err := auth.Resolve(log); err != nil {
		return err
	}

	// Build the MCP handler (validates opts and wires session tracking). No
	// routes or listener are mounted here - that is this package's job.
	mcpHandler, err := mcp.HTTPHandler(opts)
	if err != nil {
		return err
	}

	// Serve the MCP Streamable-HTTP handler and any health routes from one
	// mux/listener so health probes share the MCP port - no second http.Server.
	//
	// httpx.GuardRebind and the bearer guard are applied only to /mcp. Health
	// routes are left unguarded so container orchestrators can probe them
	// freely. The rebind check runs outermost so a forged cross-origin browser
	// request is rejected before the bearer token is even examined; the bearer
	// guard then enforces the shared secret on everything that gets past it.
	allowed := httpx.AllowedHosts(addr)
	httpServer, err := httpx.NewServer(addr)
	if err != nil {
		return err
	}
	httpServer.Handle("/mcp", httpx.GuardRebind(allowed, httpx.BearerGuard(auth.VerifyBearer, mcpHandler)))
	for path, h := range opts.HealthRoutes {
		httpServer.Handle(path, h)
	}

	// Web bridge: three frozen GET routes for the browser Graph Explorer.
	// Mounted only when:
	//   1. bridge.enabled is unset or true (opt-out via bridge.enabled: false)
	//   2. The bind address is loopback (non-loopback binding refuses the mount)
	//
	// addr is always a numeric IP:port here because the mcp_address config
	// validator calls netip.ParseAddrPort, which rejects hostnames. IsLoopback
	// therefore always compares against a resolved IP, never a hostname, so
	// the loopback gate is sound: addr.Addr().IsLoopback() is exact.
	if opts.Config.Bridge.Enabled == nil || *opts.Config.Bridge.Enabled {
		if !addr.Addr().IsLoopback() {
			log.Warn("[BRIDGE] refusing to mount web bridge on non-loopback address; set bridge.enabled: false to suppress this warning",
				slog.String("addr", addr.String()))
		} else {
			// Start a file watcher for SSE graph-invalidation events. Non-fatal:
			// if the watcher cannot start, the SSE stream emits only heartbeats.
			var inv <-chan struct{}
			bWatcher, werr := watch.New(ctx,
				watch.WithRoot(opts.Magus.Root()),
				watch.WithIgnore(watch.BuiltinIgnore),
			)
			if werr != nil {
				log.Warn("[BRIDGE] file watcher unavailable; /api/v1/events will emit heartbeats only",
					slog.String("error", werr.Error()))
			} else {
				inv = console.WatchInvalidate(ctx, bWatcher)
				go func() {
					<-ctx.Done()
					_ = bWatcher.Close()
				}()
			}

			// The console service is pure application logic; the three route handlers below
			// hold narrow interfaces satisfied by it and own all wire encoding.
			svc := console.NewService(opts.Magus, opts.Config, opts.StatusBase, opts.Version)

			// CORS allows the hosted explorer origin plus the two loopback origins derived
			// from the server port. Metrics streaming is off in production (no snapshot fn).
			siteOrigin, _ := opts.SiteOrigin()
			port := addr.Port()
			cors := httpx.CORSAllow(
				siteOrigin,
				fmt.Sprintf("http://localhost:%d", port),
				fmt.Sprintf("http://127.0.0.1:%d", port),
			)

			// The bridge routes share the same auth and DNS-rebind middleware as /mcp.
			bridgeMux := http.NewServeMux()
			bridgeMux.Handle("/api/v1/status", cors(status.NewStatusHandler(svc, log)))
			bridgeMux.Handle("/api/v1/events", cors(status.NewEventsHandler(svc, opts.Version, nil, inv, 0, 0, log)))
			bridgeMux.Handle("/api/v1/graph", cors(graphhandler.NewGraphHandler(svc, log)))
			// Wrap every /api/ route with rebind + auth.
			httpServer.Handle("/api/", httpx.GuardRebind(allowed, httpx.BearerGuard(auth.VerifyBearer, bridgeMux)))
			log.Info("[BRIDGE] web bridge mounted", slog.String("addr", addr.String()))
		}
	}

	log.Info("[AGENT] HTTP server starting", slog.String("addr", httpServer.Addr().String()))
	if err := httpServer.Serve(ctx); err != nil {
		log.Warn("[AGENT] shutdown error", slog.String("error", err.Error()))
		return err
	}
	return nil
}
