// Package serverhttp is the HTTP composition of `magus server`: it mounts the MCP
// Streamable-HTTP handler, the k8s health routes, and the browser Graph
// Explorer console onto one loopback listener, applying the shared bearer
// and DNS-rebind guards. It is the composition point that ties together
// internal/handler/mcp, internal/httpx, and internal/service/console so
// neither the handler/mcp package nor the root magus package has to.
//
// The CLI injects a *Server into the root magus package via
// magus.SetServer; magus.Serve then delegates here. That indirection
// keeps magus free of an import cycle (serverhttp depends on magus, not vice versa).
package serverhttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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

// connectReadMax bounds every Connect request body the server accepts. connect-go
// otherwise reads an unbounded message into memory, so a connector-token MCP client or
// a LAN share viewer could POST a multi-gigabyte body and OOM the server. It shares
// handler.MaxWireBodyBytes with the raw-JSON routes so both wire surfaces cap alike.
var connectReadMax = connect.WithReadMaxBytes(handler.MaxWireBodyBytes)

// Server assembles and runs the server's HTTP listener from a set of MCP server
// options. It satisfies magus.Server.
type Server struct {
	opts       mcp.Options
	runs       func() []types.StatusRun
	broker     func() *types.StatusBroker
	workspaces func() []activityhandler.Workspace
	// unloaded is set by NewUnloaded: the workspace failed, so only the surfaces that
	// need none are served.
	unloaded *Unloaded
	// onMounted receives every route pattern and each guarded pattern's Need once mounting
	// is done, before the listener serves; the route tests read the mux through it.
	onMounted func(patterns []string, needs map[string]types.Need)
}

// Option customizes a Server.
type Option func(*Server)

// WithRuns supplies the server's live-run source (the run registry's Snapshot). When
// set, the StatusService (GetStatus/StreamStatus) and the status SSE frame carry the per-target
// execution state of every adopted run alongside the pool: the same status surface, more live state.
func WithRuns(fn func() []types.StatusRun) Option {
	return func(d *Server) { d.runs = fn }
}

// WithBrokerStatus supplies the broker's status: its capacity, the claims holding it and the
// shared services it keeps warm. When set, the StatusService and the status SSE frame
// carry them alongside the pool and runs. fn returns nil when no broker answers.
func WithBrokerStatus(fn func() *types.StatusBroker) Option {
	return func(d *Server) { d.broker = fn }
}

// WithActivityWorkspaces supplies the server's live workspace set (the same per-workspace
// registry snapshot that feeds the proc Status RPC), so the activity view merges every loaded
// workspace's trail instead of only the bridge workspace's. Sharing one source keeps the activity
// view and the status view from disagreeing about which workspaces exist. When unset, the
// activity view still serves the bridge workspace's own trail.
func WithActivityWorkspaces(fn func() []activityhandler.Workspace) Option {
	return func(d *Server) { d.workspaces = fn }
}

// New returns a Server that will serve the MCP endpoint (plus health routes and
// the console) described by opts.
func New(opts mcp.Options, options ...Option) *Server {
	d := &Server{opts: opts}
	for _, o := range options {
		o(d)
	}
	return d
}

// activityWorkspaces is the trail source the ActivityService reads: the bridge workspace plus
// every workspace the registry reports loaded, deduplicated by cache dir (the registry adopts the
// bridge workspace too, and reading one trail twice would double every event on the page). The
// bridge workspace is unconditional so a server whose registry is empty (a single-workspace
// server, or a bridge started without the multi-workspace server) still serves its own trail.
func (s *Server) activityWorkspaces() func() []activityhandler.Workspace {
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

// Unloaded is the workspace a server serves while it is not loaded: failed, or loading
// again after a source changed.
type Unloaded struct {
	// Root is the workspace root; the static console is resolved beneath it.
	Root string
	// Err is what a call needing the workspace answers right now: FAILED_PRECONDITION
	// while it is failed, UNAVAILABLE while it loads. Read per request.
	Err func() rpcerr.Error
}

// NewUnloaded returns a Server for a workspace that did not load. It serves what needs no
// workspace (health, the status stream, the console shell, /mcp's tool list) and answers
// every workspace call with u.Err, so a client learns why instead of finding nothing
// listening. opts.Magus is ignored.
func NewUnloaded(opts mcp.Options, u Unloaded, options ...Option) *Server {
	opts.Magus = nil
	d := New(opts, options...)
	d.unloaded = &u
	return d
}

// Serve starts the HTTP listener, blocking until ctx is cancelled or the
// server fails. Multiple MCP clients can connect concurrently.
func (s *Server) Serve(ctx context.Context) error {
	if s.unloaded != nil {
		return s.serveUnloaded(ctx)
	}
	opts := s.opts
	log, addr, err := s.prepare(ctx)
	if err != nil {
		return err
	}

	// ONE job store for the whole server, built before the MCP handler so the magus_job
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
			// source is set, the status report also carries the server's runs.
			var svcOpts []console.Option
			if s.runs != nil {
				svcOpts = append(svcOpts, console.WithRuns(s.runs))
			}
			if s.broker != nil {
				svcOpts = append(svcOpts, console.WithBrokerStatus(s.broker))
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
			// bridge mux below (behind rebind + the console bearer guard), and the on-demand
			// LAN "share to phone" listener (behind a per-session read-only share token,
			// see shareGuarded). Building them once keeps the two surfaces serving the
			// identical read logic.
			outputStore := cache.NewOutputStore(opts.Magus.CacheDir())
			eventsH := status.NewEventsHandler(svc, opts.Build, nil, inv, 0, 0, log)
			insightH := insighthandler.NewHandler(svc, log)
			patchH := diffhandler.NewPatchHandler(svc, log)
			contextH := diffhandler.NewContextHandler(opts.Magus.Root(), svc, log)
			// The server-wide session store, constructed by the caller so the console routes
			// below and the magus_diff MCP tool read the SAME object: that sharing is the
			// pairing. A caller that supplied none gets a local one rather than a nil panic;
			// pairing is then per-process, which is the honest degradation.
			diffSessions := opts.DiffSessions
			if diffSessions == nil {
				diffSessions = changeset.NewStore(opts.Magus.CacheDir())
			}
			diffRoot := opts.Magus.Root()
			diffH := diffhandler.NewHandler(svc, diffSessions, diffRoot, log)
			diffOpts := diffhandler.ReviewOptions{
				Sessions:  diffSessions,
				Workspace: svc,
				Root:      diffRoot,
				CacheDir:  opts.Magus.CacheDir(),
				Telemetry: opts.Magus.Telemetry(),
			}
			diffReviewH := diffhandler.NewReviewHandler(diffOpts, log)
			diffLookupH := diffhandler.NewReviewLookupHandler(svc, log)
			// The review store lets the lookup response say which threads the reader has not
			// seen before; without it the conversation still serves, just unmarked.
			diffLookupH.Sessions = diffSessions
			diffLookupH.Root = opts.Magus.Root()
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

			// Every /api/v1 route is mounted on its own, behind the Need apiNeeds names for it,
			// with rebind + CORS + header-only bearer auth. CORS sits outside the bearer guard so
			// a tokenless OPTIONS preflight from the hosted PWA is answered rather than 401'd.
			//
			// The JSON /api/v1/status route is GONE: StatusService/GetStatus replaced it.
			// The diff routes are loopback-only and never in the LAN share subset: a working diff
			// is unreviewed source and a share link is handed to a phone. /api/v1/diff/session is
			// the review route's half of a paired review, reachable only from the console and the
			// CLI, which is what lets it stamp every write as unattributed. /api/v1/diff/run
			// starts work, bounded by what the magusfile declares. /api/v1/plan names every target,
			// and /api/v1/attention disposes a block a person owns, so neither is shared either.
			f.api("/api/v1/events", eventsH)
			f.api("/api/v1/graph", graphhandler.NewGraphHandler(svc, log))
			f.api("/api/v1/insight", insightH)
			f.api("/api/v1/diff/patch", patchH)
			f.api("/api/v1/diff/context", contextH)
			f.api("/api/v1/diff", diffH)
			f.api("/api/v1/diff/session", diffReviewH)
			f.api("/api/v1/diff/review", diffLookupH)
			f.api("/api/v1/diff/branches", diffBranchesH)
			f.api("/api/v1/diff/run", diffRunH)
			f.api("/api/v1/plan", planH)
			f.api("/api/v1/attention", attentionH)
			f.api("/api/", http.NotFoundHandler())

			// shareGuarded is the exact read surface the LAN share listener exposes, each entry
			// guarded per link by the share token and held to the same Needs as here. It is a
			// subset of the loopback surface: NO /api/v1/graph, NO /mcp, NO JobService.
			shareGuarded := map[string]share.Route{
				"/api/v1/events":  {Handler: eventsH, Format: rpcerr.FormatJSON, Needs: apiNeedsFor("/api/v1/events")},
				"/api/v1/insight": {Handler: insightH, Format: rpcerr.FormatJSON, Needs: apiNeedsFor("/api/v1/insight")},
			}

			// Derived-metrics Connect service for the /dashboard. Mounted only when the
			// bridge Magus collects metrics locally. The server shares one provider across
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
				f.service(mPath, mHandler)
				// MetricsService is a read-only stream, so it joins the share read surface.
				shareGuarded[mPath] = serviceRoute(mPath, mHandler)
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
			f.service(activityPath, activityHandler)
			// ActivityService is read-only, so it joins the share read surface.
			shareGuarded[activityPath] = serviceRoute(activityPath, activityHandler)
			log.InfoContext(ctx, "[BRIDGE] activity service mounted", slog.String("path", activityPath))

			// Status Connect service: the typed convergence of the JSON /api/v1/status route
			// onto the wire contract (magus.status.v1alpha1.Status). GetStatus is the one-shot the
			// dashboard reads; StreamStatus is the typed twin of the base64-SSE status frame.
			// Same cross-origin guards as the other read services (the dashboard is a hosted-site
			// browser client) and read-only, so it joins the share read surface too.
			statusPath, statusConnectHandler := statusv1alpha1connect.NewStatusServiceHandler(status.NewConnectService(svc, opts.Build, log), connectReadMax)
			f.service(statusPath, statusConnectHandler)
			shareGuarded[statusPath] = serviceRoute(statusPath, statusConnectHandler)
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
			f.service(toolPath, toolConnectHandler)
			log.InfoContext(ctx, "[BRIDGE] tool service mounted", slog.String("path", toolPath))

			// Insight Connect service: the typed twin of the JSON /api/v1/insight route, reading
			// the SAME cached scan through the same console service. The console dashboard reads
			// it here; the JSON route stays mounted above for its documented non-console callers.
			// Same cross-origin guards as the other read services, and read-only, so it joins the
			// share read surface too: the LAN "share to phone" dashboard renders insight, and it
			// reaches it over this route now rather than the JSON one.
			insightPath, insightConnectHandler := insightv1alpha1connect.NewInsightServiceHandler(insighthandler.NewService(svc), connectReadMax)
			f.service(insightPath, insightConnectHandler)
			shareGuarded[insightPath] = serviceRoute(insightPath, insightConnectHandler)
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
			f.service(viewerPath, viewerConnectHandler)
			shareGuarded[viewerPath] = serviceRoute(viewerPath, viewerConnectHandler)
			log.InfoContext(ctx, "[BRIDGE] viewer service mounted", slog.String("path", viewerPath))

			// Job control service: the server's one MUTATING console surface (submit graph sync,
			// rotate the activity trail, clear the cache). Mounted behind the same bearer guard and
			// cross-origin allowance as the read services (never unauthenticated), so a browser
			// client can trigger maintenance without the server exposing an open action endpoint.
			jobPath, jobHandler := jobv1alpha1connect.NewJobServiceHandler(jobhandler.NewService(opts.Magus, opts.Version, opts.Jobs), connectReadMax)
			f.service(jobPath, jobHandler)
			log.InfoContext(ctx, "[BRIDGE] job service mounted", slog.String("path", jobPath))

			// Share to phone: POST /api/v1/share opens an on-demand, time-boxed LAN
			// listener serving shareGuarded (the read surface) under a fresh read-only
			// token. The trigger is loopback-only (RequireLoopbackPeer, atop the
			// loopback-bound listener) and needs console=write: only the local,
			// already-authenticated console can open a share. CORS wraps
			// the bearer so the console's cross-origin POST preflight is answered here.
			// The manager's parent is ctx, so every open share listener is torn down on
			// server shutdown; Close is a belt-and-suspenders immediate teardown.
			// WithTrailDir records a "share link opened" activity event on the first
			// request from each remote device, so the console can surface that a phone
			// connected. The trail is the same workspace cache base the ActivityService reads.
			shareMgr := share.NewManager(ctx, log, share.WithTrailDir(opts.Magus.CacheDir()))
			defer shareMgr.Close()
			consoleDir, ok := resolveConsoleDir(opts.Magus.Root())
			if !ok {
				log.WarnContext(ctx, "[SHARE] built console not found; share to phone will report it needs a console build",
					slog.String("root", opts.Magus.Root()))
			}
			shareH := s.newShareHandler(shareMgr, consoleDir, shareGuarded, opts.Magus.CacheDir(), log)
			// RequireLoopbackPeer keeps the peer on loopback so only the local browser can open a
			// share, whatever the listener is bound to.
			f.api("/api/v1/share", httpx.RequireLoopbackPeer(shareH))
			// A console link's one-time code is traded for its token here. The code is the
			// credential, so there is no bearer guard; the handler refuses a missing, wrong,
			// expired or used code with 401.
			f.server.Handle("/api/v1/token/exchange", httpx.GuardRebind(rpcerr.FormatJSON, f.siteAllowed, f.cors(
				httpx.RequireLoopbackPeer(newExchangeHandler(opts.Magus.CacheDir(), log)))))
			log.InfoContext(ctx, "[SHARE] share endpoint mounted", slog.String("path", "/api/v1/share"), slog.Bool("console_ready", ok))

			// Static console on loopback: serve the built PWA at /console/ from the SAME
			// resolved dir the LAN share listener uses (consoleDir), so a minted server-origin
			// link (http://127.0.0.1:<port>/console/<surface>/) loads the app straight off this
			// server. console.StaticHandler is the ONE implementation both listeners share: it
			// adds the SPA fallback so the clean /console/<surface>/ surface paths resolve to the
			// shell, and a strict CSP on the HTML. Static serving stays unauthenticated by design
			// - the app shell is not a secret; it reads the bearer token from the URL fragment and
			// replays it on the guarded /api and Connect routes above. The shell is ALL it
			// serves: StaticHandler refuses any file outside its shell allowlist, because the
			// build also writes the hosted demo's graph JSON (this repo's knowledge graph, notes
			// included) into the same dir. It is wrapped in the same GuardRebind the rest of the
			// loopback surface uses, so a forged cross-origin Host cannot reach it. Mounted only
			// when a build was found; otherwise the server still runs (MCP + data routes) and
			// /console/ just 404s until a console is built.
			if ok {
				f.server.Handle("/console/", httpx.GuardRebind(rpcerr.FormatJSON, f.allowed, console.StaticHandler(consoleDir)))
				log.InfoContext(ctx, "[BRIDGE] static console mounted", slog.String("path", "/console/"), slog.String("dir", consoleDir))
			}

			// Token management service: the console Settings UI lists, mints and revokes console
			// and viewer tokens here, and sees and revokes the active share. It needs tokens=write,
			// which only the operator grant holds, and every mint it makes is checked against the
			// caller's own grant in auth.Store.Mint, so neither the mount nor the handler is the
			// only thing standing between a token and a wider one. The operator token lives in a
			// file the handler never opens, so it can be neither listed nor revoked here, and the
			// Settings UI cannot lock the operator out. Not in shareGuarded: a share link never
			// reaches token management.
			// The audit interceptor classifies every RPC on this service by its leading verb
			// (internal/handler/trailrpc) and records the mutating ones (CreateToken and
			// RevokeToken today) to the trail, so a browser-reachable mint or revoke is always
			// audited. The record names the credential the bearer guard verified, never a
			// caller-supplied field. Reads (ListTokens) are not recorded. See
			// internal/handler/trailrpc for the pattern and the arch-test ratchet that keeps it
			// honest.
			tokenAudit := connect.WithInterceptors(trailrpc.Interceptor(opts.Magus.CacheDir(), trail.KindTokenLifecycle,
				trailrpc.WithSubject(tokenhandler.AuditSubject)))
			tokenPath, tokenHandler := tokenv1alpha1connect.NewTokenServiceHandler(tokenhandler.NewService(shareMgr), tokenAudit, connectReadMax)
			f.service(tokenPath, tokenHandler)
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
			f.service(memoryPath, memoryHandler)
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
			f.service(notesPath, notesHandler)
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
			// same facts are already served unaudited over /api/v1/graph. The two doors onto that
			// one body of data hold the same need, console=read.
			graphPath, graphServiceHandler := graphv1alpha1connect.NewGraphServiceHandler(graphhandler.NewService(opts.Magus), connectReadMax)
			f.service(graphPath, graphServiceHandler)
			log.InfoContext(ctx, "[BRIDGE] graph service mounted", slog.String("path", graphPath))

			log.InfoContext(ctx, "[BRIDGE] console mounted", slog.String("addr", addr.String()))
		}
	}

	return s.run(ctx, log, f)
}

func (s *Server) run(ctx context.Context, log *slog.Logger, f *frame) error {
	if err := errors.Join(f.errs...); err != nil {
		return err
	}
	httpServer := f.server
	if s.onMounted != nil {
		s.onMounted(httpServer.Patterns(), maps.Clone(f.needs))
	}
	log.InfoContext(ctx, "[AGENT] HTTP server starting", slog.String("addr", httpServer.Addr().String()))
	if err := httpServer.Serve(ctx); err != nil {
		log.WarnContext(ctx, "[AGENT] shutdown error", slog.String("error", err.Error()))
		return err
	}
	return nil
}

// prepare resolves the logger and bind address from the exported option fields, mirroring
// the fallbacks the handler package applies internally, and provisions the operator token.
func (s *Server) prepare(ctx context.Context) (*slog.Logger, netip.AddrPort, error) {
	log := s.opts.Logger
	if log == nil {
		log = slog.Default()
	}
	addr := s.opts.HTTPAddr
	if !addr.IsValid() {
		addr = netip.MustParseAddrPort(mcp.DefaultAddress)
	}

	// A non-loopback bind (MAGUS_MCP_ADDRESS=0.0.0.0 for k8s health probes, say) serves every
	// bearer token over plaintext HTTP, so it is an explicit opt-in, never a warning.
	if !addr.Addr().IsLoopback() && !s.opts.Config.MCP.InsecureBind {
		return nil, addr, fmt.Errorf("server: mcp.address %s is not loopback, and a non-loopback listener sends bearer tokens in cleartext; front it with TLS or a tunnel and set mcp.insecure_bind: true (MAGUS_MCP_INSECURE_BIND=true), or bind 127.0.0.1", addr)
	}

	// Fail closed: without an operator token the server never serves. Every guard re-reads
	// the operator file and the token store per request, so a rotate, mint or revoke takes
	// effect without a restart.
	if _, err := auth.EnsureOperator(ctx, log); err != nil {
		return nil, addr, err
	}
	return log, addr, nil
}

// frame is the listener and the guard chains every route mounts behind. needs records each
// guarded path's Need as it is mounted, and errs every mount that could not be guarded, which
// stops the server before it serves.
type frame struct {
	server      *httpx.Server
	allowed     httpx.AllowedSet
	siteAllowed httpx.AllowedSet
	cors        func(http.Handler) http.Handler
	needs       map[string]types.Need
	errs        []error
}

// guard mounts h at pattern behind rebind, then (for a console route) CORS, so a tokenless
// OPTIONS preflight is answered while a hosted-PWA Origin still clears rebind first, then the
// bearer guard holding each path to its need. Every guarded mount goes through here.
func (f *frame) guard(pattern string, format rpcerr.Format, needs map[string]types.Need, console bool, h http.Handler) {
	g, err := httpx.ProcedureGuard(format, auth.Verify, needs, h)
	if err != nil {
		f.errs = append(f.errs, fmt.Errorf("server: %s: %w", pattern, err))
		return
	}
	maps.Copy(f.needs, needs)
	if !console {
		f.server.Handle(pattern, httpx.GuardRebind(format, f.allowed, g))
		return
	}
	f.server.Handle(pattern, httpx.GuardRebind(format, f.siteAllowed, f.cors(g)))
}

// api mounts a JSON console route at path behind the Need apiNeeds names for it.
func (f *frame) api(path string, h http.Handler) {
	needs, err := apiNeedsOf(path)
	if err != nil {
		f.errs = append(f.errs, err)
		return
	}
	f.guard(path, rpcerr.FormatJSON, needs, true, h)
}

// service mounts a Connect service at its path ("/<package>.<Service>/") behind the Need of
// each of its procedures.
func (f *frame) service(path string, h http.Handler) {
	needs, err := serviceNeeds(path)
	if err != nil {
		f.errs = append(f.errs, err)
		return
	}
	f.guard(path, rpcerr.FormatConnect, needs, true, h)
}

// serviceRoute is a Connect service as the share listener serves it, held to the same Needs.
// A service missing from procedureNeeds yields no Needs, which the listener refuses to guard.
func serviceRoute(path string, h http.Handler) share.Route {
	needs, _ := serviceNeeds(path)
	return share.Route{Handler: h, Format: rpcerr.FormatConnect, Needs: needs}
}

// mount binds the listener and mounts /mcp and the health routes, the surface every
// server serves whether or not its workspace loaded.
func (s *Server) mount(addr netip.AddrPort, mcpHandler http.Handler) (*frame, error) {
	// Serve the MCP Streamable-HTTP handler and any health routes from one
	// mux/listener so health probes share the MCP port: no second http.Server.
	//
	// httpx.GuardRebind and the bearer guard are applied only to /mcp. Health
	// routes are left unguarded so container orchestrators can probe them
	// freely. The rebind check runs outermost so a forged cross-origin browser
	// request is rejected before the bearer token is even examined; the bearer
	// guard then enforces the shared secret on everything that gets past it.
	f := &frame{allowed: httpx.AllowedHosts(addr), needs: map[string]types.Need{}}
	httpServer, err := httpx.NewServer(addr)
	if err != nil {
		return nil, err
	}
	f.server = httpServer
	// Cap the MCP body too: the connector-token client reaches /mcp, not the Connect
	// services, and mark3labs' streamable handler reads the body with an uncapped
	// io.ReadAll, so the connectReadMax above does not cover this surface.
	cappedMCP := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.LimitRequestBody(w, r)
		mcpHandler.ServeHTTP(w, r)
	})
	f.guard("/mcp", rpcerr.FormatJSON, map[string]types.Need{"/mcp": needMCP}, false, cappedMCP)

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

// serveUnloaded is Serve for a workspace that did not load. Every route a loaded server
// guards stays behind the same guard and the same Needs, so the failure (which names files)
// is shown only to a caller that could have read the workspace anyway. StatusService is
// served; every other service and /api/ route answers with the load error.
func (s *Server) serveUnloaded(ctx context.Context) error {
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
		return s.run(ctx, log, f)
	}

	var svcOpts []console.Option
	if s.runs != nil {
		svcOpts = append(svcOpts, console.WithRuns(s.runs))
	}
	if s.broker != nil {
		svcOpts = append(svcOpts, console.WithBrokerStatus(s.broker))
	}
	// A nil workspace is what console.Service reads the pool alone from: the pool carries
	// this workspace's state and error, which is the thing to show.
	svc := console.NewService(nil, opts.Config, opts.StatusBase, opts.Version, svcOpts...)
	statusPath, statusHandler := statusv1alpha1connect.NewStatusServiceHandler(status.NewConnectService(svc, opts.Build, log), connectReadMax)
	f.service(statusPath, statusHandler)

	refuseConnect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rpcerr.FormatConnect.Write(w, r, u.Err())
	})
	for name := range procedureNeeds {
		if path := "/" + string(name) + "/"; path != statusPath {
			f.service(path, refuseConnect)
		}
	}
	refuseJSON := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rpcerr.FormatJSON.Write(w, r, u.Err())
	})
	for path := range apiNeeds {
		f.api(path, refuseJSON)
	}
	if consoleDir, ok := resolveConsoleDir(u.Root); ok {
		f.server.Handle("/console/", httpx.GuardRebind(rpcerr.FormatJSON, f.allowed, console.StaticHandler(consoleDir)))
	}
	log.WarnContext(ctx, "[BRIDGE] workspace not loaded; serving status and the console only",
		slog.String("root", u.Root), slog.String("error", u.Err().Message))
	return s.run(ctx, log, f)
}
