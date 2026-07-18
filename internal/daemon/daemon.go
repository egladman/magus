// Package daemon assembles the magus daemon HTTP server: it mounts the MCP
// Streamable-HTTP handler, the k8s health routes, and the browser Graph
// Explorer console onto one loopback listener, applying the shared bearer
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
	"net/url"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file/watch"
	activityhandler "github.com/egladman/magus/internal/handler/activity"
	graphhandler "github.com/egladman/magus/internal/handler/graph"
	jobhandler "github.com/egladman/magus/internal/handler/job"
	mcp "github.com/egladman/magus/internal/handler/mcp"
	metricshandler "github.com/egladman/magus/internal/handler/metrics"
	"github.com/egladman/magus/internal/handler/status"
	viewer "github.com/egladman/magus/internal/handler/viewer"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/share"
	"github.com/egladman/magus/proto/gen/go/magus/activity/v1/activityv1connect"
	"github.com/egladman/magus/proto/gen/go/magus/job/v1/jobv1connect"
	"github.com/egladman/magus/proto/gen/go/magus/metrics/v1/metricsv1connect"
	"github.com/egladman/magus/types"
)

// Daemon assembles and runs the daemon HTTP server from a set of MCP server
// options. It satisfies magus.Daemon.
type Daemon struct {
	opts     mcp.Options
	runs     func() []types.StatusRun
	services func() []types.StatusService
}

// Option customizes a Daemon.
type Option func(*Daemon)

// WithRuns supplies the daemon's live-run source (the run registry's Snapshot). When
// set, /api/v1/status and the status SSE frame carry the per-target execution state of every
// adopted run alongside the pool - the same status surface, more live state.
func WithRuns(fn func() []types.StatusRun) Option {
	return func(d *Daemon) { d.runs = fn }
}

// WithServices supplies the daemon's hosted-services source (the service registry's
// Snapshot). When set, /api/v1/status and the status SSE frame carry the long-running
// shared services the daemon is keeping warm alongside the pool and runs.
func WithServices(fn func() []types.StatusService) Option {
	return func(d *Daemon) { d.services = fn }
}

// New returns a Daemon that will serve the MCP endpoint (plus health routes and
// the console) described by opts.
func New(opts mcp.Options, options ...Option) *Daemon {
	d := &Daemon{opts: opts}
	for _, o := range options {
		o(d)
	}
	return d
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

	// A non-loopback bind (e.g. MAGUS_MCP_ADDRESS=0.0.0.0 for k8s health probes)
	// serves /mcp over plaintext HTTP, so the bearer token crosses the network in
	// the clear. The MCP transport spec says remote HTTP should use TLS; warn so an
	// operator fronts it with TLS or a tunnel rather than exposing a cleartext token.
	if !addr.Addr().IsLoopback() {
		log.Warn("[AGENT] MCP is bound to a non-loopback address; the bearer token is sent in cleartext over HTTP - front it with TLS or a tunnel",
			slog.String("addr", addr.String()))
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

	// CORS allows the hosted explorer origin plus the two loopback origins derived from
	// the server port. Built here (not only inside the console block below) so /livez and
	// /readyz get the same allow-list even when the console mount is disabled: a browser
	// client (the console PWA) needs to read them cross-origin, but they stay otherwise
	// unguarded - no rebind check, no bearer token - so an orchestrator can still probe them
	// freely. CORSAllow itself only ever reflects an allow-listed Origin, never "*", so this
	// widens readability, not who may write.
	siteOrigin, _ := opts.SiteOrigin()
	port := addr.Port()
	cors := httpx.CORSAllow(
		siteOrigin,
		fmt.Sprintf("http://localhost:%d", port),
		fmt.Sprintf("http://127.0.0.1:%d", port),
	)
	for path, h := range opts.HealthRoutes {
		httpServer.Handle(path, cors(h))
	}

	// Console: three frozen GET routes for the browser Graph Explorer.
	// Mounted only when:
	//   1. console.enabled is unset or true (opt-out via console.enabled: false)
	//   2. The bind address is loopback (non-loopback binding refuses the mount)
	//
	// addr is always a numeric IP:port here because the mcp_address config
	// validator calls netip.ParseAddrPort, which rejects hostnames. IsLoopback
	// therefore always compares against a resolved IP, never a hostname, so
	// the loopback gate is sound: addr.Addr().IsLoopback() is exact.
	if opts.Config.Console.Enabled == nil || *opts.Config.Console.Enabled {
		if !addr.Addr().IsLoopback() {
			log.Warn("[BRIDGE] refusing to mount console on non-loopback address; set console.enabled: false to suppress this warning",
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
			// hold narrow interfaces satisfied by it and own all wire encoding. When a live-run
			// source is set, the status report also carries the daemon's runs.
			var svcOpts []console.Option
			if s.runs != nil {
				svcOpts = append(svcOpts, console.WithRuns(s.runs))
			}
			if s.services != nil {
				svcOpts = append(svcOpts, console.WithServices(s.services))
			}
			svc := console.NewService(opts.Magus, opts.Config, opts.StatusBase, opts.Version, svcOpts...)

			// cors (siteOrigin + the two loopback origins for this port) was built above,
			// before the health-route loop, so it is shared rather than rebuilt here.

			// The bridge routes share the same auth and DNS-rebind middleware as
			// /mcp, header-only included: the explorer authenticates every /api
			// call - fetches AND the SSE event stream (a fetch()-based reader, not
			// an EventSource) - with an Authorization header, so the token never
			// rides in the URL. CORS still advertises the Authorization header for
			// the cross-origin preflight.
			// Read handlers are built ONCE and reused for two audiences: the loopback
			// bridge mux below (behind rebind + cli/connector bearer), and the on-demand
			// LAN "share to phone" listener (behind a per-session read-only share token,
			// see shareGuarded). Building them once keeps the two surfaces serving the
			// identical read logic.
			outputStore := cache.NewOutputStore(opts.Magus.CacheDir())
			statusH := status.NewStatusHandler(svc, opts.Build, log)
			eventsH := status.NewEventsHandler(svc, opts.Build, nil, inv, 0, 0, log)
			insightH := status.NewInsightHandler(svc, log)
			outputsH := viewer.NewOutputsHandler(outputStore, log)
			outputH := viewer.NewOutputHandler(outputStore, log)

			bridgeMux := http.NewServeMux()
			bridgeMux.Handle("/api/v1/status", cors(statusH))
			bridgeMux.Handle("/api/v1/events", cors(eventsH))
			bridgeMux.Handle("/api/v1/graph", cors(graphhandler.NewGraphHandler(svc, log)))
			// In-daemon insight: the four VCS-history lenses (cached scan) plus the folded-in
			// run-outcome volatility lens, all under the single "volatility" key of InsightView.
			// Plain JSON over the same /api guards as the rest.
			bridgeMux.Handle("/api/v1/insight", cors(insightH))
			// Run browser: the log viewer's tree lists prior runs (/api/v1/outputs) and loads any one's
			// verbatim captured output (/api/v1/output?ref=). The store is constructed off the cache dir
			// per request (a shallow keep-last-K scan), matching the other read-only /api JSON routes.
			bridgeMux.Handle("/api/v1/outputs", cors(outputsH))
			bridgeMux.Handle("/api/v1/output", cors(outputH))
			// Wrap every /api/ route with rebind + header-only bearer auth.
			httpServer.Handle("/api/", httpx.GuardRebind(allowed, httpx.BearerGuard(auth.VerifyBearer, bridgeMux)))

			// shareGuarded is the exact read surface the LAN share listener exposes,
			// each entry guarded per-session by the share token (share.Manager wraps
			// them). It is deliberately a subset of the loopback bridge: NO /api/v1/graph,
			// NO /mcp, NO mutating JobService - a leaked share link reaches only these
			// read routes. The two Connect read services (activity, metrics) are added
			// to this map below, where their handlers are built.
			shareGuarded := map[string]http.Handler{
				"/api/v1/status":  statusH,
				"/api/v1/events":  eventsH,
				"/api/v1/insight": insightH,
				"/api/v1/outputs": outputsH,
				"/api/v1/output":  outputH,
			}

			// Derived-metrics Connect service for the /dashboard. Mounted only when the
			// bridge Magus collects metrics locally. The daemon shares one provider across
			// its bridge Magus and every per-workspace registry Magus (WithProvider), so this
			// collector sees the counts those builds actually recorded; a disabled/export-only
			// provider yields no collector and the mount is skipped. The Connect route lives at its own /magus.metrics.v1.*
			// prefix (not under /api/), so it gets the same rebind + bearer + CORS guards
			// applied directly rather than via the bridge mux.
			if coll, ok := opts.Magus.MetricsCollector(); ok {
				metricsSvc := metricshandler.NewService(coll, svc)
				metricsSvc.Start(ctx)
				mPath, mHandler := metricsv1connect.NewMetricsServiceHandler(metricsSvc)

				// The dashboard is a cross-origin browser client (served from the hosted site),
				// so the DNS-rebind accept-list must include the site origin, not just loopback.
				// Widen a COPY of allowed for this route only; /mcp and /api keep their loopback
				// posture. CORS wraps BearerGuard (not the reverse) so the browser's tokenless
				// OPTIONS preflight is answered here rather than 401'd by the bearer check; the
				// actual POST still carries and is verified against the bearer token.
				metricsAllowed := allowed
				if u, uerr := url.Parse(siteOrigin); uerr == nil && u.Host != "" {
					metricsAllowed = allowed.Allow(u.Host)
				}
				httpServer.Handle(mPath, httpx.GuardRebind(metricsAllowed, cors(httpx.BearerGuard(auth.VerifyBearer, mHandler))))
				// MetricsService is a read-only stream, so it joins the share read surface.
				shareGuarded[mPath] = mHandler
				log.Info("[BRIDGE] metrics service mounted", slog.String("path", mPath))
			} else {
				log.Info("[BRIDGE] metrics service off (workspace not collecting metrics)")
			}

			// Activity-trail Connect service for the /dashboard + log viewer: recent agent
			// and governance activity, read-only over the workspace trail. Mounted with the
			// same cross-origin guards as metrics (the dashboard is a hosted-site browser
			// client) and unconditionally - the trail is readable even when metrics are off.
			activityPath, activityHandler := activityv1connect.NewActivityServiceHandler(activityhandler.NewService(opts.Magus.CacheDir()))
			activityAllowed := allowed
			if u, uerr := url.Parse(siteOrigin); uerr == nil && u.Host != "" {
				activityAllowed = allowed.Allow(u.Host)
			}
			httpServer.Handle(activityPath, httpx.GuardRebind(activityAllowed, cors(httpx.BearerGuard(auth.VerifyBearer, activityHandler))))
			// ActivityService.ListActivity is read-only, so it joins the share read surface.
			shareGuarded[activityPath] = activityHandler
			log.Info("[BRIDGE] activity service mounted", slog.String("path", activityPath))

			// Job control service: the daemon's one MUTATING console surface (submit graph sync,
			// rotate the activity trail, clear the cache). Mounted behind the same bearer guard and
			// cross-origin allowance as the read services - never unauthenticated - so a browser
			// client can trigger maintenance without the daemon exposing an open action endpoint.
			jobPath, jobHandler := jobv1connect.NewJobServiceHandler(jobhandler.NewService(opts.Magus, opts.Version))
			httpServer.Handle(jobPath, httpx.GuardRebind(activityAllowed, cors(httpx.BearerGuard(auth.VerifyBearer, jobHandler))))
			log.Info("[BRIDGE] job service mounted", slog.String("path", jobPath))

			// Share to phone: POST /api/v1/share opens an on-demand, time-boxed LAN
			// listener serving shareGuarded (the read surface) under a fresh read-only
			// token. The trigger is loopback-only (RequireLoopbackPeer, atop the
			// loopback-bound listener) and requires the existing cli/connector bearer -
			// only the local, already-authenticated console can open a share. CORS wraps
			// the bearer so the console's cross-origin POST preflight is answered here.
			// The manager's parent is ctx, so every open share listener is torn down on
			// daemon shutdown; Close is a belt-and-suspenders immediate teardown.
			shareMgr := share.NewManager(ctx, 0, log)
			defer shareMgr.Close()
			consoleDir, ok := resolveConsoleDir(opts.Magus.Root())
			if !ok {
				log.Warn("[SHARE] built console not found; share to phone will report it needs a console build",
					slog.String("root", opts.Magus.Root()))
			}
			shareH := s.newShareHandler(shareMgr, consoleDir, shareGuarded, log)
			httpServer.Handle("/api/v1/share", httpx.GuardRebind(allowed, cors(httpx.RequireLoopbackPeer(httpx.BearerGuard(auth.VerifyBearer, shareH)))))
			log.Info("[SHARE] share endpoint mounted", slog.String("path", "/api/v1/share"), slog.Bool("console_ready", ok))

			log.Info("[BRIDGE] console mounted", slog.String("addr", addr.String()))
		}
	}

	log.Info("[AGENT] HTTP server starting", slog.String("addr", httpServer.Addr().String()))
	if err := httpServer.Serve(ctx); err != nil {
		log.Warn("[AGENT] shutdown error", slog.String("error", err.Error()))
		return err
	}
	return nil
}
