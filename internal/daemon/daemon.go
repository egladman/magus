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

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/handler"
	activityhandler "github.com/egladman/magus/internal/handler/activity"
	attentionhandler "github.com/egladman/magus/internal/handler/attention"
	diffhandler "github.com/egladman/magus/internal/handler/diff"
	graphhandler "github.com/egladman/magus/internal/handler/graph"
	insighthandler "github.com/egladman/magus/internal/handler/insight"
	jobhandler "github.com/egladman/magus/internal/handler/job"
	mcp "github.com/egladman/magus/internal/handler/mcp"
	memoryhandler "github.com/egladman/magus/internal/handler/memory"
	metricshandler "github.com/egladman/magus/internal/handler/metrics"
	noteshandler "github.com/egladman/magus/internal/handler/notes"
	planhandler "github.com/egladman/magus/internal/handler/plan"
	"github.com/egladman/magus/internal/handler/status"
	tokenhandler "github.com/egladman/magus/internal/handler/token"
	toolhandler "github.com/egladman/magus/internal/handler/tool"
	"github.com/egladman/magus/internal/handler/trailrpc"
	viewer "github.com/egladman/magus/internal/handler/viewer"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/share"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/proto/gen/go/magus/activity/v1alpha1/activityv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1/graphv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/insight/v1alpha1/insightv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/job/v1alpha1/jobv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/memory/v1alpha1/memoryv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/metrics/v1alpha1/metricsv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/notes/v1alpha1/notesv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/status/v1alpha1/statusv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/token/v1alpha1/tokenv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/tool/v1alpha1/toolv1alpha1connect"
	"github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1/viewerv1alpha1connect"
	"github.com/egladman/magus/types"
)

// connectReadMax bounds every Connect request body the daemon accepts. connect-go
// otherwise reads an unbounded message into memory, so a connector-token MCP client or
// a LAN share viewer could POST a multi-gigabyte body and OOM the daemon. It shares
// handler.MaxWireBodyBytes with the raw-JSON routes so both wire surfaces cap alike.
var connectReadMax = connect.WithReadMaxBytes(handler.MaxWireBodyBytes)

// Daemon assembles and runs the daemon HTTP server from a set of MCP server
// options. It satisfies magus.Daemon.
type Daemon struct {
	opts       mcp.Options
	runs       func() []types.StatusRun
	services   func() []types.StatusService
	workspaces func() []activityhandler.Workspace
	// unloaded is set by NewUnloaded: the workspace failed, so only the surfaces that
	// need none are served.
	unloaded *Unloaded
	// onMounted receives every route pattern once mounting is done, before the listener
	// serves; the route-enumeration test reads the mux through it.
	onMounted func(patterns []string)
}

// Option customizes a Daemon.
type Option func(*Daemon)

// WithRuns supplies the daemon's live-run source (the run registry's Snapshot). When
// set, the StatusService (GetStatus/StreamStatus) and the status SSE frame carry the per-target
// execution state of every adopted run alongside the pool: the same status surface, more live state.
func WithRuns(fn func() []types.StatusRun) Option {
	return func(d *Daemon) { d.runs = fn }
}

// WithServices supplies the daemon's hosted-services source (the service registry's
// Snapshot). When set, the StatusService and the status SSE frame carry the long-running
// shared services the daemon is keeping warm alongside the pool and runs.
func WithServices(fn func() []types.StatusService) Option {
	return func(d *Daemon) { d.services = fn }
}

// WithActivityWorkspaces supplies the daemon's live workspace set (the same per-workspace
// registry snapshot that feeds the proc Status RPC), so the activity view merges every loaded
// workspace's trail instead of only the bridge workspace's. Sharing one source keeps the activity
// view and the status view from disagreeing about which workspaces exist. When unset, the
// activity view still serves the bridge workspace's own trail.
func WithActivityWorkspaces(fn func() []activityhandler.Workspace) Option {
	return func(d *Daemon) { d.workspaces = fn }
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

// activityWorkspaces is the trail source the ActivityService reads: the bridge workspace plus
// every workspace the registry reports loaded, deduplicated by cache dir (the registry adopts the
// bridge workspace too, and reading one trail twice would double every event on the page). The
// bridge workspace is unconditional so a daemon whose registry is empty (a single-workspace
// daemon, or a bridge started without the multi-workspace server) still serves its own trail.
func (s *Daemon) activityWorkspaces() func() []activityhandler.Workspace {
	bridge := activityhandler.Workspace{Root: s.opts.Magus.Root(), CacheDir: s.opts.Magus.CacheDir()}
	return func() []activityhandler.Workspace {
		out := []activityhandler.Workspace{bridge}
		seen := map[string]bool{bridge.CacheDir: true}
		if s.workspaces == nil {
			return out
		}
		for _, w := range s.workspaces() {
			if seen[w.CacheDir] {
				continue
			}
			seen[w.CacheDir] = true
			out = append(out, w)
		}
		return out
	}
}

// Unloaded is the workspace a daemon serves while it is not loaded: failed, or loading
// again after a source changed.
type Unloaded struct {
	// Root is the workspace root; the static console is resolved beneath it.
	Root string
	// Err is what a call needing the workspace answers right now: FAILED_PRECONDITION
	// while it is failed, UNAVAILABLE while it loads. Read per request.
	Err func() rpcerr.Error
}

// NewUnloaded returns a Daemon for a workspace that did not load. It serves what needs no
// workspace (health, the status stream, the console shell, /mcp's tool list) and answers
// every workspace call with u.Err, so a client learns why instead of finding nothing
// listening. opts.Magus is ignored.
func NewUnloaded(opts mcp.Options, u Unloaded, options ...Option) *Daemon {
	opts.Magus = nil
	d := New(opts, options...)
	d.unloaded = &u
	return d
}

// Serve starts the daemon HTTP server, blocking until ctx is cancelled or the
// server fails. Multiple MCP clients can connect concurrently.
func (s *Daemon) Serve(ctx context.Context) error {
	if s.unloaded != nil {
		return s.serveUnloaded(ctx)
	}
	opts := s.opts
	log, addr, err := s.prepare(ctx)
	if err != nil {
		return err
	}

	// ONE job store for the whole daemon, built before the MCP handler so the magus_job
	// tool and the JobService below hold the same object. Two stores over one file each
	// take their own mutex, and the merge Update performs under a single acquisition then
	// serializes against nothing.
	if opts.Jobs == nil && opts.Magus != nil {
		opts.Jobs = job.NewStore(job.Location{CacheDir: opts.Magus.CacheDir(), Root: opts.Magus.Root()})
	}

	// Build the MCP handler (validates opts and wires session tracking). No
	// routes or listener are mounted here; that is this package's job.
	mcpHandler, err := mcp.HTTPHandler(opts)
	if err != nil {
		return err
	}

	f, err := s.mount(addr, mcpHandler)
	if err != nil {
		return err
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
			log.WarnContext(ctx, "[BRIDGE] refusing to mount console on non-loopback address; set console.enabled: false to suppress this warning",
				slog.String("addr", addr.String()))
		} else {
			// Start a file watcher for SSE graph-invalidation events. Non-fatal:
			// if the watcher cannot start, the SSE stream emits only heartbeats.
			var inv <-chan struct{}
			var jobFeed *console.JobFeed
			// RelativeIgnore, not BuiltinIgnore: the predicate skips any dot segment in the
			// path, which is right for a directory inside the tree and wrong for one the
			// tree sits inside. A workspace opened at <repo>/.claude/worktrees/<name> has a
			// dot segment above its root, so every file in it matched and this watcher
			// reported nothing at all, with nothing logged and nothing failing.
			bWatcher, werr := watch.New(ctx,
				watch.WithRoot(opts.Magus.Root()),
				watch.WithIgnore(watch.RelativeIgnore(opts.Magus.Root(), watch.BuiltinIgnore)),
			)
			if werr != nil {
				log.WarnContext(ctx, "[BRIDGE] file watcher unavailable; /api/v1/events will emit heartbeats only",
					slog.String("error", werr.Error()))
			} else {
				// ONE consumer of the watcher, two audiences: the SSE stream's graph
				// invalidation and the activity feed's attributed file changes. Two read
				// loops over one channel would take alternate batches.
				jobFeed = console.NewJobFeed(ctx, bWatcher, opts.Magus.Root(), func() []types.Job {
					rows, err := opts.Jobs.List()
					if err != nil {
						return nil
					}
					return rows
				})
				inv = jobFeed.Invalidate()
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
			// call (fetches AND the SSE event stream, a fetch()-based reader, not
			// an EventSource) with an Authorization header, so the token never
			// rides in the URL. CORS still advertises the Authorization header for
			// the cross-origin preflight.
			// Read handlers are built ONCE and reused for two audiences: the loopback
			// bridge mux below (behind rebind + cli/connector bearer), and the on-demand
			// LAN "share to phone" listener (behind a per-session read-only share token,
			// see shareGuarded). Building them once keeps the two surfaces serving the
			// identical read logic.
			outputStore := cache.NewOutputStore(opts.Magus.CacheDir())
			eventsH := status.NewEventsHandler(svc, opts.Build, nil, inv, 0, 0, log)
			insightH := insighthandler.NewHandler(svc, log)
			patchH := diffhandler.NewPatchHandler(svc, log)
			contextH := diffhandler.NewContextHandler(opts.Magus.Root(), svc, log)
			// The daemon-wide session store, constructed by the caller so the console routes
			// below and the magus_diff MCP tool read the SAME object: that sharing is the
			// pairing. A caller that supplied none gets a local one rather than a nil panic;
			// pairing is then per-process, which is the honest degradation.
			diffSessions := opts.DiffSessions
			if diffSessions == nil {
				diffSessions = changeset.NewStore(opts.Magus.CacheDir())
			}
			diffRoot := opts.Magus.Root()
			diffH := diffhandler.NewHandler(svc, diffSessions, diffRoot, log)
			diffOpts := diffhandler.SessionOptions{
				Sessions:  diffSessions,
				Workspace: svc,
				Root:      diffRoot,
				CacheDir:  opts.Magus.CacheDir(),
				Telemetry: opts.Magus.Telemetry(),
			}
			diffSessionH := diffhandler.NewSessionHandler(diffOpts, log)
			diffReviewH := diffhandler.NewReviewHandler(svc, log)
			// The session store lets the review response say which threads the reader has not
			// seen before; without it the conversation still serves, just unmarked.
			diffReviewH.Sessions = diffSessions
			diffReviewH.Root = opts.Magus.Root()
			diffBranchesH := diffhandler.NewBranchesHandler(svc, log)
			diffRunH := diffhandler.NewRunHandler(svc, opts.Magus.CacheDir(), opts.Version, log)
			// The DERIVED plan: the target DAG the engine computes for plain work. It reads
			// the same two sources the console already trusts (the service for structure and
			// live pool state, the output store for each node's last outcome and its ref), so
			// it introduces no third notion of what ran.
			planH := planhandler.NewHandler(svc, outputStore, opts.Magus.Root(), log)
			// The attention queue: blocks agents raised that are waiting on a person. Read off
			// the per-repository session store, which is keyed on repo identity rather than the
			// checkout, so the console lists what `magus session attention` lists from any worktree.
			attentionH := attentionhandler.NewHandler(opts.Magus.Root(), opts.Version, log, opts.Magus.Telemetry())

			bridgeMux := http.NewServeMux()
			// The JSON /api/v1/status route is GONE: the typed StatusService Connect route
			// (magus.status.v1alpha1.StatusService/GetStatus, mounted below) is its full replacement:
			// it serves the same live snapshot plus observing_since and config on the wire contract,
			// and the console reads it there now.
			bridgeMux.Handle("/api/v1/events", f.cors(eventsH))
			bridgeMux.Handle("/api/v1/graph", f.cors(graphhandler.NewGraphHandler(svc, log)))
			// In-daemon insight: the four VCS-history lenses (cached scan) plus the folded-in
			// run-outcome volatility lens, all under the single "volatility" key of InsightView.
			// Plain JSON over the same /api guards as the rest.
			bridgeMux.Handle("/api/v1/insight", f.cors(insightH))
			// Diff surface: the working tree's uncommitted changes as one unified patch.
			// Loopback-only, alongside the other /api reads: deliberately NOT added to the LAN
			// share subset below, because a working diff is unreviewed source and a share link
			// is handed to a phone.
			bridgeMux.Handle("/api/v1/diff/patch", f.cors(patchH))
			bridgeMux.Handle("/api/v1/diff/context", f.cors(contextH))
			// The annotation half: role, blast radius, changed-symbol reach, coverage. Split
			// from /api/v1/diff/patch because it is far more expensive; see Handler.
			bridgeMux.Handle("/api/v1/diff", f.cors(diffH))
			// The human's half of a paired review. Reachable only from the console and the
			// CLI, which is what lets it stamp every write as human without trusting the
			// payload: an agent reaches the session through MCP, never through here.
			bridgeMux.Handle("/api/v1/diff/session", f.cors(diffSessionH))
			// Which review this branch has open, and what colleagues have already said on it.
			// Its own route because it crosses the network to a forge: a reader must never wait
			// on somebody else's outage to see their own diff.
			bridgeMux.Handle("/api/v1/diff/review", f.cors(diffReviewH))
			// The other branches changing these files. Its own route because it forks per branch:
			// a reader must not wait on it to see their own diff, and it reads only what has
			// already been fetched rather than going to the network for more.
			bridgeMux.Handle("/api/v1/diff/branches", f.cors(diffBranchesH))
			// Does this still pass? Asked of the machine the code is on, which is the one review
			// question a forge structurally cannot answer. Loopback only and MUTATING (it starts
			// work), so it sits with the diff routes rather than in the LAN share subset, and the
			// work it can start is bounded by what the magusfile declares.
			bridgeMux.Handle("/api/v1/diff/run", f.cors(diffRunH))
			// Human run view: every plain run has a plan, and an agent-declared one is not the
			// only shape worth showing. Loopback only, unlike the run browser's ViewerService:
			// this one names every target in the workspace, which a share link handed to a phone
			// has no business enumerating.
			bridgeMux.Handle("/api/v1/plan", f.cors(planH))
			// Attention queue: GET lists the open requests, POST disposes one. The write is a
			// PERSON closing a block through their own surface (docs/doctrine.md, "Manual on
			// purpose"), which is why it sits here on the loopback bridge and NOT in the LAN
			// share subset below: a share link is handed to a phone, and disposing a request
			// is exactly the judgment a link cannot be trusted with.
			bridgeMux.Handle("/api/v1/attention", f.cors(attentionH))
			// Wrap every /api/ route with rebind + CORS + header-only bearer auth.
			// CORS sits outside BearerGuard so a tokenless OPTIONS preflight from the
			// hosted PWA is answered here rather than 401'd; siteAllowed admits that
			// Origin past the rebind check. Inner cors() on each bridgeMux handler is
			// redundant for responses that reach the mux and harmless if both fire.
			f.server.Handle("/api/", f.siteGuarded(rpcerr.FormatJSON, auth.VerifyConsoleBearer, bridgeMux))

			// shareGuarded is the exact read surface the LAN share listener exposes,
			// each entry guarded per-session by the share token (share.Manager wraps
			// them). It is deliberately a subset of the loopback bridge: NO /api/v1/graph,
			// NO /mcp, NO mutating JobService: a leaked share link reaches only these
			// read routes. The two Connect read services (activity, metrics) are added
			// to this map below, where their handlers are built.
			shareGuarded := map[string]share.Route{
				"/api/v1/events":  {Handler: eventsH, Format: rpcerr.FormatJSON},
				"/api/v1/insight": {Handler: insightH, Format: rpcerr.FormatJSON},
			}

			// Derived-metrics Connect service for the /dashboard. Mounted only when the
			// bridge Magus collects metrics locally. The daemon shares one provider across
			// its bridge Magus and every per-workspace registry Magus (WithProvider), so this
			// collector sees the counts those builds actually recorded; a disabled/export-only
			// provider yields no collector and the mount is skipped. The Connect route lives at its own /magus.metrics.v1alpha1.*
			// prefix (not under /api/), so it gets the same rebind + bearer + CORS guards
			// applied directly rather than via the bridge mux.
			if coll, ok := opts.Magus.MetricsCollector(); ok {
				metricsSvc := metricshandler.NewService(coll, svc)
				metricsSvc.Start(ctx)
				mPath, mHandler := metricsv1alpha1connect.NewMetricsServiceHandler(metricsSvc, connectReadMax)

				// The dashboard is a cross-origin browser client (served from the hosted site).
				// CORS wraps BearerGuard (not the reverse) so the browser's tokenless OPTIONS
				// preflight is answered here rather than 401'd by the bearer check; the actual
				// POST still carries and is verified against the bearer token. /mcp stays on
				// the loopback-only accept-list.
				f.server.Handle(mPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleReadBearer, mHandler))
				// MetricsService is a read-only stream, so it joins the share read surface.
				shareGuarded[mPath] = share.Route{Handler: mHandler, Format: rpcerr.FormatConnect}
				log.InfoContext(ctx, "[BRIDGE] metrics service mounted", slog.String("path", mPath))
			} else {
				log.InfoContext(ctx, "[BRIDGE] metrics service off (workspace not collecting metrics)")
			}

			// Activity-trail Connect service for the /dashboard + log viewer: recent agent
			// and governance activity, read-only over every loaded workspace's trail. Mounted
			// with the same cross-origin guards as metrics (the dashboard is a hosted-site
			// browser client) and unconditionally: the trail is readable even when metrics are off.
			// The two extra sources WatchActivityEvents merges with the trail: the job plan
			// (so a file change can be attributed to the write path that covers it, and a job's
			// recorded runs reach the feed) and the watcher fan-out above. Both degrade to
			// nothing rather than failing the mount: without them the stream still serves
			// the guard's observations, which is what this service served before.
			activitySvc := activityhandler.NewService(s.activityWorkspaces(),
				activityhandler.WithJobs(func() []types.Job {
					rows, jerr := opts.Jobs.List()
					if jerr != nil {
						return nil
					}
					return rows
				}))
			if jobFeed != nil {
				activityhandler.WithFileChanges(jobFeed.Subscribe)(activitySvc)
			}
			activityPath, activityHandler := activityv1alpha1connect.NewActivityServiceHandler(activitySvc, connectReadMax)
			f.server.Handle(activityPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleReadBearer, activityHandler))
			// ActivityService.ListActivityEvents is read-only, so it joins the share read surface.
			shareGuarded[activityPath] = share.Route{Handler: activityHandler, Format: rpcerr.FormatConnect}
			log.InfoContext(ctx, "[BRIDGE] activity service mounted", slog.String("path", activityPath))

			// Status Connect service: the typed convergence of the JSON /api/v1/status route
			// onto the wire contract (magus.status.v1alpha1.Status). GetStatus is the one-shot the
			// dashboard reads; StreamStatus is the typed twin of the base64-SSE status frame.
			// Same cross-origin guards as the other read services (the dashboard is a hosted-site
			// browser client) and read-only, so it joins the share read surface too.
			statusPath, statusConnectHandler := statusv1alpha1connect.NewStatusServiceHandler(status.NewConnectService(svc, opts.Build, log), connectReadMax)
			f.server.Handle(statusPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleReadBearer, statusConnectHandler))
			shareGuarded[statusPath] = share.Route{Handler: statusConnectHandler, Format: rpcerr.FormatConnect}
			log.InfoContext(ctx, "[BRIDGE] status service mounted", slog.String("path", statusPath))

			// Tool Connect service: the toolchain view (which binaries this workspace's
			// spells drive, what each reported, and the window it is held to).
			//
			// Deliberately NOT in shareGuarded, unlike every other read service here.
			// Read-only is not the bar for that surface: every other entry answers from
			// memory or disk, and this one EXECS argv the spells declare. A share is a
			// token handed to a phone on the LAN, not a remote handle for spawning
			// processes on the operator's machine. The console reaches it over the
			// authenticated loopback route.
			toolPath, toolConnectHandler := toolv1alpha1connect.NewToolServiceHandler(toolhandler.NewService(opts.Magus), connectReadMax)
			f.server.Handle(toolPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleReadBearer, toolConnectHandler))
			log.InfoContext(ctx, "[BRIDGE] tool service mounted", slog.String("path", toolPath))

			// Insight Connect service: the typed twin of the JSON /api/v1/insight route, reading
			// the SAME cached scan through the same console service. The console dashboard reads
			// it here; the JSON route stays mounted above for its documented non-console callers.
			// Same cross-origin guards as the other read services, and read-only, so it joins the
			// share read surface too: the LAN "share to phone" dashboard renders insight, and it
			// reaches it over this route now rather than the JSON one.
			insightPath, insightConnectHandler := insightv1alpha1connect.NewInsightServiceHandler(insighthandler.NewService(svc), connectReadMax)
			f.server.Handle(insightPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleReadBearer, insightConnectHandler))
			shareGuarded[insightPath] = share.Route{Handler: insightConnectHandler, Format: rpcerr.FormatConnect}
			log.InfoContext(ctx, "[BRIDGE] insight service mounted", slog.String("path", insightPath))

			// Viewer Connect service: the typed twin of the JSON run-browser routes this
			// replaced (/api/v1/outputs, /output, /runs, /run; retired in 7ce1896d4, see
			// docs/concepts/compatibility.md), reading the SAME two stores.
			//
			// Read-only, so it takes the read bearer and joins the share surface the way its
			// retired JSON twins did: a shared phone renders the run browser, and it must keep
			// reaching the same runs whichever route the page settles on.
			var viewerOpts []viewer.Option
			if opts.Magus != nil {
				viewerOpts = append(viewerOpts, viewer.WithSessionRoot(opts.Magus.Root()))
			}
			viewerPath, viewerConnectHandler := viewerv1alpha1connect.NewViewerServiceHandler(viewer.NewService(outputStore, outputStore, viewerOpts...), connectReadMax)
			f.server.Handle(viewerPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleReadBearer, viewerConnectHandler))
			shareGuarded[viewerPath] = share.Route{Handler: viewerConnectHandler, Format: rpcerr.FormatConnect}
			log.InfoContext(ctx, "[BRIDGE] viewer service mounted", slog.String("path", viewerPath))

			// events and insight are ALSO mounted here individually, on the viewer-accepting
			// guard. They are already reachable through the /api/ mux above, but that mux is
			// mixed (it carries the diff session's mutating ops), so it must stay on the write
			// tier. Registering these two paths explicitly gives a viewer token the same surface
			// without widening the mux: net/http prefers the longer pattern, so /api/v1/events
			// wins over /api/.
			//
			// They are the SAME handlers shareGuarded hands the LAN listener, so "what a viewer
			// may see" has one definition and cannot drift between a phone and a loopback tab.
			// siteAllowed + cors outside bearer matches the /api/ mount: a hosted-PWA Origin
			// must clear rebind, and OPTIONS must not hit the bearer check first.
			for path, h := range map[string]http.Handler{
				"/api/v1/events":  eventsH,
				"/api/v1/insight": insightH,
			} {
				f.server.Handle(path, f.siteGuarded(rpcerr.FormatJSON, auth.VerifyConsoleReadBearer, h))
			}

			// Job control service: the daemon's one MUTATING console surface (submit graph sync,
			// rotate the activity trail, clear the cache). Mounted behind the same bearer guard and
			// cross-origin allowance as the read services (never unauthenticated), so a browser
			// client can trigger maintenance without the daemon exposing an open action endpoint.
			jobPath, jobHandler := jobv1alpha1connect.NewJobServiceHandler(jobhandler.NewService(opts.Magus, opts.Version, opts.Jobs), connectReadMax)
			f.server.Handle(jobPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleBearer, jobHandler))
			log.InfoContext(ctx, "[BRIDGE] job service mounted", slog.String("path", jobPath))

			// Share to phone: POST /api/v1/share opens an on-demand, time-boxed LAN
			// listener serving shareGuarded (the read surface) under a fresh read-only
			// token. The trigger is loopback-only (RequireLoopbackPeer, atop the
			// loopback-bound listener) and requires the existing cli/connector bearer:
			// only the local, already-authenticated console can open a share. CORS wraps
			// the bearer so the console's cross-origin POST preflight is answered here.
			// The manager's parent is ctx, so every open share listener is torn down on
			// daemon shutdown; Close is a belt-and-suspenders immediate teardown.
			// WithTrailDir records a "share link opened" activity event on the first
			// request from each remote device, so the console can surface that a phone
			// connected. The trail is the same workspace cache base the ActivityService reads.
			shareMgr := share.NewManager(ctx, 0, log, share.WithTrailDir(opts.Magus.CacheDir()))
			defer shareMgr.Close()
			consoleDir, ok := resolveConsoleDir(opts.Magus.Root())
			if !ok {
				log.WarnContext(ctx, "[SHARE] built console not found; share to phone will report it needs a console build",
					slog.String("root", opts.Magus.Root()))
			}
			shareH := s.newShareHandler(shareMgr, consoleDir, shareGuarded, log)
			// siteAllowed so a hosted-PWA Origin clears rebind; RequireLoopbackPeer still
			// keeps the peer on loopback so only the local browser can open a share.
			f.server.Handle("/api/v1/share", httpx.GuardRebind(rpcerr.FormatJSON, f.siteAllowed, f.cors(httpx.RequireLoopbackPeer(
				httpx.BearerGuard(rpcerr.FormatJSON, auth.VerifyConsoleBearer, shareH)))))
			log.InfoContext(ctx, "[SHARE] share endpoint mounted", slog.String("path", "/api/v1/share"), slog.Bool("console_ready", ok))

			// Static console on loopback: serve the built PWA at /console/ from the SAME
			// resolved dir the LAN share listener uses (consoleDir), so a minted daemon-origin
			// link (http://127.0.0.1:<port>/console/<surface>/) loads the app straight off this
			// daemon. console.StaticHandler is the ONE implementation both listeners share: it
			// adds the SPA fallback so the clean /console/<surface>/ surface paths resolve to the
			// shell, and a strict CSP on the HTML. Static serving stays unauthenticated by design
			// - the app shell is not a secret; it reads the bearer token from the URL fragment and
			// replays it on the guarded /api and Connect routes above. The shell is ALL it
			// serves: StaticHandler refuses any file outside its shell allowlist, because the
			// build also writes the hosted demo's graph JSON (this repo's knowledge graph, notes
			// included) into the same dir. It is wrapped in the same GuardRebind the rest of the
			// loopback surface uses, so a forged cross-origin Host cannot reach it. Mounted only
			// when a build was found; otherwise the daemon still runs (MCP + data routes) and
			// /console/ just 404s until a console is built.
			if ok {
				f.server.Handle("/console/", httpx.GuardRebind(rpcerr.FormatJSON, f.allowed, console.StaticHandler(consoleDir)))
				log.InfoContext(ctx, "[BRIDGE] static console mounted", slog.String("path", "/console/"), slog.String("dir", consoleDir))
			}

			// Token management service: the typed surface the console Settings UI uses to LIST,
			// CREATE, and REVOKE console and viewer tokens, and to see/revoke the active share
			// token. CreateToken can mint (it is not view-and-revoke only), but only the two
			// scopes a browser has any business minting (console, console-read); the operator and
			// connector classes are refused there by the handler itself (internal/handler/token),
			// regardless of who is asking. What guards against a compromised browser forging a
			// durable /mcp credential is the GUARD tier below, not an absence of a mint path. It
			// is a second door onto the same connector store the CLI writes and the same shareMgr
			// the share endpoint drives, never a second store.
			//
			// The mount enforces the three-tier credential hierarchy at the GUARD, so the handler
			// stays dumb:
			//   - operator token (built-in cli credential): the ONLY accepted bearer here
			//     (VerifyCLIBearer, not the generic VerifyBearer). Whoever holds it owns the daemon,
			//     so every token op, mint included, is operator-tier.
			//   - connector token (MCP client): valid on /mcp and the console data services, but
			//     rejected on this mount; a client credential must never mint or revoke another
			//     credential (privilege self-replication).
			//   - share token (read-only viewer): only ever valid on the LAN share listener; this
			//     service is deliberately NOT in shareGuarded, so a shared phone can never reach
			//     the token-management surface.
			// siteAllowed admits the hosted PWA Origin past rebind (Settings runs from
			// eli.gladman.cc against the local daemon). VerifyCLIBearer still refuses every
			// non-operator credential. The operator/built-in token is additionally unreachable
			// through the handler itself: it is bootstrap-only, managed SOLELY by the CLI, and
			// structurally invisible+immutable to this service (it lives in a store the handler
			// never opens, so it is neither listed, mintable, nor revocable here), preventing
			// lockout.
			// The audit interceptor classifies every RPC on this service by its leading verb
			// (internal/handler/trailrpc) and records the mutating ones (CreateToken and
			// RevokeToken today) to the trail, so a browser-reachable mint or revoke is always
			// audited. The record names the credential the bearer guard verified, never a
			// caller-supplied field. Reads (ListTokens) are not recorded. See
			// internal/handler/trailrpc for the pattern and the arch-test ratchet that keeps it
			// honest.
			tokenAudit := connect.WithInterceptors(trailrpc.Interceptor(opts.Magus.CacheDir(), trail.KindTokenLifecycle))
			tokenPath, tokenHandler := tokenv1alpha1connect.NewTokenServiceHandler(tokenhandler.NewService(shareMgr), tokenAudit, connectReadMax)
			f.server.Handle(tokenPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyCLIBearer, tokenHandler))
			log.InfoContext(ctx, "[BRIDGE] token service mounted", slog.String("path", tokenPath))

			// Memory management service: the typed surface the console Settings UI uses to LIST,
			// READ, EDIT, and DELETE the durable magus_memory files (status, progress, decisions).
			// It is a second door onto the EXACT on-disk files the magus_memory MCP tool writes,
			// never a second store: the human edit/delete surface is the safety valve against agent
			// memory growing unbounded (it is append-heavy and never rotated by default). Mounted on
			// the loopback listener behind the standard bearer guard and deliberately NOT in
			// shareGuarded: memory is the operator's own working notes, not a read surface for a
			// shared phone view. Its content is agent-written and must be rendered as text, never as
			// trusted HTML.
			// Audit every memory RPC to the trail, READS included (WithAuditReads): unlike the token
			// service, inspecting the agent's own working notes is itself worth recording, so List/Get
			// are audited alongside the edits, each naming the credential that made it. The agent/MCP
			// door onto the same files is audited separately.
			memoryAudit := connect.WithInterceptors(trailrpc.Interceptor(opts.Magus.CacheDir(), trail.KindMemory, trailrpc.WithAuditReads()))
			memoryPath, memoryHandler := memoryv1alpha1connect.NewMemoryServiceHandler(memoryhandler.NewService(opts.Magus), memoryAudit, connectReadMax)
			f.server.Handle(memoryPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleBearer, memoryHandler))
			log.InfoContext(ctx, "[BRIDGE] memory service mounted", slog.String("path", memoryPath))

			// Notes service: the typed surface the console's Notes view uses to READ the
			// workspace's human-authored notes. Read-only by construction (the contract has no
			// write RPC), because a note's value is the guarantee that a person wrote it, and a
			// browser write would put an unattributable author on the one node class nothing in
			// the repository corroborates. The way in stays `magus notes edit`, in an editor,
			// committed under the author's name.
			//
			// Deliberately NOT in shareGuarded, and here that is not a preference: the private
			// store is by definition not shared with anyone and may live anywhere on disk, so a
			// LAN share listener must never be able to reach it. Mounted on the loopback
			// listener behind the standard bearer guard like the memory service beside it.
			// Audits READS (WithAuditReads) for the same reason memory does, sharpened by the
			// private store: this is the only door that serves notes nothing else attributes.
			notesAudit := connect.WithInterceptors(trailrpc.Interceptor(opts.Magus.CacheDir(), trail.KindNotes, trailrpc.WithAuditReads()))
			notesPath, notesHandler := notesv1alpha1connect.NewNotesServiceHandler(noteshandler.NewService(opts.Magus, opts.Config), notesAudit, connectReadMax)
			f.server.Handle(notesPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleReadBearer, notesHandler))
			log.InfoContext(ctx, "[BRIDGE] notes service mounted", slog.String("path", notesPath))

			// Graph service: the typed surface for the knowledge graph's own verbs (query,
			// resolve, explain, path, stats). It exists so the browser stops reimplementing them;
			// the Graph Explorer's filter was a second, divergent copy of the query grammar,
			// scoring by raw degree over a payload /api/v1/graph had already sent whole. That
			// route stays: a bulk subgraph document is a different job from ranked retrieval.
			//
			// Deliberately NOT in shareGuarded, for the same reason /api/v1/graph is not: a
			// leaked share link must not reach the workspace's structure. No audit interceptor
			// either: unlike notes and memory, nothing here is attributable to a person, and the
			// same facts are already served unaudited over /api/v1/graph.
			//
			// VerifyConsoleBearer, NOT the read-tier verifier its read-only contract would
			// suggest. This service and /api/v1/graph are two doors onto ONE body of data, and
			// /api/ is mounted at the write tier, so the read tier here would let a viewer
			// credential page the whole graph through QueryNodes after being refused the bulk
			// route, which is a hole rather than a convenience. The tiers move together or the
			// weaker one decides.
			graphPath, graphServiceHandler := graphv1alpha1connect.NewGraphServiceHandler(graphhandler.NewService(opts.Magus), connectReadMax)
			f.server.Handle(graphPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleBearer, graphServiceHandler))
			log.InfoContext(ctx, "[BRIDGE] graph service mounted", slog.String("path", graphPath))

			log.InfoContext(ctx, "[BRIDGE] console mounted", slog.String("addr", addr.String()))
		}
	}

	return s.run(ctx, log, f.server)
}

func (s *Daemon) run(ctx context.Context, log *slog.Logger, httpServer *httpx.Server) error {
	if s.onMounted != nil {
		s.onMounted(httpServer.Patterns())
	}
	log.InfoContext(ctx, "[AGENT] HTTP server starting", slog.String("addr", httpServer.Addr().String()))
	if err := httpServer.Serve(ctx); err != nil {
		log.WarnContext(ctx, "[AGENT] shutdown error", slog.String("error", err.Error()))
		return err
	}
	return nil
}

// prepare resolves the logger and bind address from the exported option fields, mirroring
// the fallbacks the handler package applies internally, and provisions the cli token.
func (s *Daemon) prepare(ctx context.Context) (*slog.Logger, netip.AddrPort, error) {
	log := s.opts.Logger
	if log == nil {
		log = slog.Default()
	}
	addr := s.opts.HTTPAddr
	if !addr.IsValid() {
		addr = netip.MustParseAddrPort(mcp.DefaultAddress)
	}

	// Provision the retrievable cli token before serving. Fail closed: if it
	// can't be loaded or generated, the MCP endpoint never comes up. Both surface
	// guards re-evaluate their verifier on each request, re-reading the cli token
	// (and, for /mcp, the named connector store) from disk, so a rotate, create, or
	// revoke takes effect without a daemon restart.
	if _, err := auth.Resolve(ctx, log); err != nil {
		return nil, addr, err
	}

	// A non-loopback bind (e.g. MAGUS_MCP_ADDRESS=0.0.0.0 for k8s health probes)
	// serves /mcp over plaintext HTTP, so the bearer token crosses the network in
	// the clear. The MCP transport spec says remote HTTP should use TLS; warn so an
	// operator fronts it with TLS or a tunnel rather than exposing a cleartext token.
	if !addr.Addr().IsLoopback() {
		log.WarnContext(ctx, "[AGENT] MCP is bound to a non-loopback address; the bearer token is sent in cleartext over HTTP - front it with TLS or a tunnel",
			slog.String("addr", addr.String()))
	}
	return log, addr, nil
}

// frame is the listener and the guard chains every route mounts behind.
type frame struct {
	server      *httpx.Server
	allowed     httpx.AllowedSet
	siteAllowed httpx.AllowedSet
	cors        func(http.Handler) http.Handler
}

// siteGuarded is the chain every console data route mounts behind. CORS sits between
// rebind and bearer so a tokenless OPTIONS preflight is answered, while a hosted-PWA
// Origin still clears rebind first. format is the mounted handler's own protocol.
func (f frame) siteGuarded(format rpcerr.Format, verify func(string) (string, bool), h http.Handler) http.Handler {
	return httpx.GuardRebind(format, f.siteAllowed, f.cors(httpx.BearerGuard(format, verify, h)))
}

// mount binds the listener and mounts /mcp and the health routes, the surface every
// daemon serves whether or not its workspace loaded.
func (s *Daemon) mount(addr netip.AddrPort, mcpHandler http.Handler) (frame, error) {
	// Serve the MCP Streamable-HTTP handler and any health routes from one
	// mux/listener so health probes share the MCP port: no second http.Server.
	//
	// httpx.GuardRebind and the bearer guard are applied only to /mcp. Health
	// routes are left unguarded so container orchestrators can probe them
	// freely. The rebind check runs outermost so a forged cross-origin browser
	// request is rejected before the bearer token is even examined; the bearer
	// guard then enforces the shared secret on everything that gets past it.
	f := frame{allowed: httpx.AllowedHosts(addr)}
	httpServer, err := httpx.NewServer(addr)
	if err != nil {
		return f, err
	}
	f.server = httpServer
	// Cap the MCP body too: the connector-token client reaches /mcp, not the Connect
	// services, and mark3labs' streamable handler reads the body with an uncapped
	// io.ReadAll, so the connectReadMax above does not cover this surface.
	cappedMCP := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.LimitRequestBody(w, r)
		mcpHandler.ServeHTTP(w, r)
	})
	httpServer.Handle("/mcp", httpx.GuardRebind(rpcerr.FormatJSON, f.allowed, httpx.BearerGuard(rpcerr.FormatJSON, auth.VerifyMCPBearer, cappedMCP)))

	// CORS allows the hosted explorer origin plus the two loopback origins derived from
	// the server port. Built here (not only inside the console block) so /livez and
	// /readyz get the same allow-list even when the console mount is disabled: a browser
	// client (the console PWA) needs to read them cross-origin, but they stay otherwise
	// unguarded (no rebind check, no bearer token), so an orchestrator can still probe them
	// freely. CORSAllow itself only ever reflects an allow-listed Origin, never "*", so this
	// widens readability, not who may write.
	siteOrigin := mcp.SiteOrigin
	port := addr.Port()
	f.cors = httpx.CORSAllow(
		siteOrigin,
		fmt.Sprintf("http://localhost:%d", port),
		fmt.Sprintf("http://127.0.0.1:%d", port),
	)
	// siteAllowed widens the DNS-rebind accept-list for console browser routes so the
	// hosted PWA Origin (eli.gladman.cc) is not 403'd before CORS can answer. /mcp keeps
	// the loopback-only set.
	f.siteAllowed = f.allowed
	if u, uerr := url.Parse(siteOrigin); uerr == nil && u.Host != "" {
		f.siteAllowed = f.allowed.Allow(u.Host)
	}
	for path, h := range s.opts.HealthRoutes {
		httpServer.Handle(path, f.cors(h))
	}
	return f, nil
}

// workspaceServices are the Connect services that read the loaded workspace. An unloaded
// daemon answers each with the workspace's error; StatusService alone is served.
var workspaceServices = []string{
	activityv1alpha1connect.ActivityServiceName,
	graphv1alpha1connect.GraphServiceName,
	insightv1alpha1connect.InsightServiceName,
	jobv1alpha1connect.JobServiceName,
	memoryv1alpha1connect.MemoryServiceName,
	metricsv1alpha1connect.MetricsServiceName,
	notesv1alpha1connect.NotesServiceName,
	tokenv1alpha1connect.TokenServiceName,
	toolv1alpha1connect.ToolServiceName,
	viewerv1alpha1connect.ViewerServiceName,
}

// serveUnloaded is Serve for a workspace that did not load. Every route a loaded daemon
// guards stays behind the same guard, so the failure (which names files) is shown only to
// a caller that could have read the workspace anyway.
func (s *Daemon) serveUnloaded(ctx context.Context) error {
	opts, u := s.opts, s.unloaded
	log, addr, err := s.prepare(ctx)
	if err != nil {
		return err
	}
	opts.Unavailable = func() error { return u.Err() }
	mcpHandler, err := mcp.HTTPHandler(opts)
	if err != nil {
		return err
	}
	f, err := s.mount(addr, mcpHandler)
	if err != nil {
		return err
	}
	if opts.Config.Console.Enabled != nil && !*opts.Config.Console.Enabled || !addr.Addr().IsLoopback() {
		return s.run(ctx, log, f.server)
	}

	var svcOpts []console.Option
	if s.runs != nil {
		svcOpts = append(svcOpts, console.WithRuns(s.runs))
	}
	if s.services != nil {
		svcOpts = append(svcOpts, console.WithServices(s.services))
	}
	// A nil workspace is what console.Service reads the pool alone from: the pool carries
	// this workspace's state and error, which is the thing to show.
	svc := console.NewService(nil, opts.Config, opts.StatusBase, opts.Version, svcOpts...)
	statusPath, statusHandler := statusv1alpha1connect.NewStatusServiceHandler(status.NewConnectService(svc, opts.Build, log), connectReadMax)
	f.server.Handle(statusPath, f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleReadBearer, statusHandler))

	refuseConnect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rpcerr.FormatConnect.Write(w, r, u.Err())
	})
	for _, name := range workspaceServices {
		f.server.Handle("/"+name+"/", f.siteGuarded(rpcerr.FormatConnect, auth.VerifyConsoleReadBearer, refuseConnect))
	}
	f.server.Handle("/api/", f.siteGuarded(rpcerr.FormatJSON, auth.VerifyConsoleReadBearer, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rpcerr.FormatJSON.Write(w, r, u.Err())
	})))
	if consoleDir, ok := resolveConsoleDir(u.Root); ok {
		f.server.Handle("/console/", httpx.GuardRebind(rpcerr.FormatJSON, f.allowed, console.StaticHandler(consoleDir)))
	}
	log.WarnContext(ctx, "[BRIDGE] workspace not loaded; serving status and the console only",
		slog.String("root", u.Root), slog.String("error", u.Err().Message))
	return s.run(ctx, log, f.server)
}
