// Package daemon assembles the magus daemon HTTP server: it mounts the MCP
// Streamable-HTTP handler, the k8s health routes, and the browser Graph
// Explorer web bridge onto one loopback listener, applying the shared bearer
// and DNS-rebind guards. It is the composition point that ties together
// internal/handler/mcp, internal/httpx, and internal/service/dashboard so
// neither the handler/mcp package nor the root magus package has to.
//
// The CLI injects a *Daemon into the root magus package via
// magus.SetDaemon; magus.ServeDaemon then delegates here. That indirection
// keeps magus free of an import cycle (daemon depends on magus, not vice versa).
package daemon

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/egladman/magus/internal/file/watch"
	mcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/handler/mcp/auth"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/service/dashboard"
)

// Daemon assembles and runs the daemon HTTP server from a set of MCP server
// options. It satisfies magus.Daemon.
type Daemon struct {
	opts mcp.ServerOptions
}

// New returns a Daemon that will serve the MCP endpoint (plus health routes and
// the web bridge) described by opts.
func New(opts mcp.ServerOptions) *Daemon {
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

	// Provision the bearer token before serving. Fail closed: if it can't be
	// loaded or generated, the MCP endpoint never comes up. Guard re-reads the
	// token via auth.Load on each request, so token rotations take effect
	// without a daemon restart.
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
	httpServer.Handle("/mcp", httpx.GuardRebind(allowed, httpx.BearerGuard(auth.Load, mcpHandler)))
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
				inv = dashboard.WatchInvalidate(ctx, bWatcher)
				go func() {
					<-ctx.Done()
					_ = bWatcher.Close()
				}()
			}

			siteOrigin, _ := opts.SiteOrigin()
			bridgeOpts := dashboard.Options{
				Magus:           opts.Magus,
				Config:          opts.Config,
				StatusBase:      opts.StatusBase,
				MagusVersion:    opts.Version,
				Addr:            addr,
				SiteOrigin:      siteOrigin,
				GraphInvalidate: inv,
			}
			// The bridge routes share the same auth and DNS-rebind middleware as /mcp.
			bridgeMux := http.NewServeMux()
			dashboard.Mount(bridgeMux, bridgeOpts)
			// Wrap every /api/ route with rebind + auth.
			httpServer.Handle("/api/", httpx.GuardRebind(allowed, httpx.BearerGuard(auth.Load, bridgeMux)))
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
