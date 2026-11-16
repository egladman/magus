package server

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	internalmcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/daemon"
)

// publishTrailBase resolves the server-wide activity-trail base and publishes it, so
// background-job recording and the maintenance scheduler land in the same trail the MCP
// handler writes and the ActivityService reads.
//
// ResolveCacheDir is the narrow path: it reads config without discovering projects or
// evaluating magusfiles, so the value is the bridge Magus's CacheDir without paying a
// workspace load to learn it, which is what lets this run ahead of the MCP gate. A root
// that will not resolve leaves the base empty, and every writer already treats an empty
// base as "drop the record, best-effort".
func (h *Host) publishTrailBase(ctx context.Context) {
	root, err := workspace.FindRoot("")
	if err != nil {
		slog.WarnContext(ctx, "no workspace root; background jobs and scheduled maintenance will not be recorded", attr.Error(err))
		return
	}
	loc, err := h.Place(root)
	if err != nil {
		slog.WarnContext(ctx, "cache or state dir unresolvable; background jobs and scheduled maintenance will not be recorded", attr.Error(err))
		return
	}
	h.trailBase = loc.CacheDir
	h.jobStore = job.NewStore(loc)
}

// StartBridge opens the server's own workspace, keeps its graph and symbol indexes
// current, and serves MCP, the console and the APIs over loopback HTTP, and MCP and the APIs
// on the server's own socket, when mcp.enabled allows. Called from
// the server command. cancel is the CancelFunc for the server's context; it is called if
// the HTTP server exits for any reason other than ctx cancellation, so the server shuts
// down rather than running on with MCP unavailable.
//
// The watch belongs to the server, not to MCP: turning off one integration must not turn
// off symbol indexing, the same coupling that once left the trail base empty.
func (h *Host) StartBridge(ctx context.Context, cancel context.CancelFunc) {
	tel := h.provider
	// Before any gate, deliberately. The trail base is the workspace cache dir and has
	// nothing to do with MCP, but it used to be published below the MCP gate, so
	// mcp.enabled: false left it empty, the maintenance scheduler bailed at every tick,
	// and no background job was ever recorded.
	h.publishTrailBase(ctx)
	mcpOn := h.Config.MCP.Enabled == nil || *h.Config.MCP.Enabled
	addr, err := netip.ParseAddrPort(MCPAddress(h.Config.MCP))
	if mcpOn && err != nil {
		slog.ErrorContext(ctx, "MCP skipped: invalid MCP address", attr.Error(err))
		mcpOn = false
	}
	// The bridge Magus MUST share the server's single provider (WithProvider) so the
	// /dashboard derived metrics (GetMetrics / StreamMetrics) read the counters that
	// per-workspace registry builds actually recorded, not this bridge's own empty
	// ManualReader. If no shared provider was supplied, fall back to a bridge-local
	// collector so the endpoint is still non-empty; one-shot CLI runs leave metrics off.
	opts := []magus.Option{magus.WithMetricsCollection()}
	if tel != nil {
		opts[0] = workspace.WithTelemetryProvider(tel)
	}
	if h.registry != nil && h.registry.broker != nil && h.Config.Broker.Resolved() != types.BrokerOff {
		opts = append(opts, magus.WithBroker(h.registry.broker))
	}
	m, err := h.Load(ctx, "", opts...)
	if err != nil {
		root, rerr := workspace.FindRoot("")
		if !mcpOn || rerr != nil || h.registry == nil {
			slog.WarnContext(ctx, "workspace unavailable; no MCP, console or watch for it", attr.Error(err))
			return
		}
		// Serve anyway: a console and an agent that can connect and read the diagnostic
		// beat a server with nothing listening. The registry holds the failure and retries
		// once a source changes; every route is served when that load succeeds.
		slog.WarnContext(ctx, "workspace failed to load; serving its failure until a source changes",
			slog.String("root", root), attr.Error(err))
		h.registry.failBridge(root, err)
		h.httpAddr.Store(addr.String())
		go h.serveUnloadedBridge(ctx, cancel, root, addr)
		return
	}
	// Register this bridge workspace in the per-workspace registry the WorkspaceLister reports,
	// so /readyz counts the server's own workspace as loaded instead of waiting for an
	// adopted run to populate the pool. Without this the server ran two workspace pools and
	// readyz reported "no workspaces loaded" even after a live MCP query used this same Magus.
	if h.registry != nil {
		h.registry.adoptBridge(m.Root(), m)
	}
	h.startWatch(ctx, m)
	if !mcpOn {
		return
	}
	h.httpAddr.Store(addr.String())
	h.serveBridge(ctx, cancel, m, addr)
}

// startWatch keeps m's knowledge graph and symbol indexes current for the server's life
// and lists m's root among the roots the server reports watching.
func (h *Host) startWatch(ctx context.Context, m *magus.Magus) {
	if watchWorkspace(ctx, m) {
		h.watching(m.Root())
	}
}

// serveUnloadedBridge serves the server's endpoints for a workspace that failed to load
// until the registry reports it ACTIVE, then hands the listener to serveBridge, which serves
// every route over that workspace.
func (h *Host) serveUnloadedBridge(ctx context.Context, cancel context.CancelFunc, root string, addr netip.AddrPort) {
	status := serverSnapshot(h.serverAddr())
	srvCtx, stop := context.WithCancel(ctx)
	defer stop()
	d := NewUnloaded(internalmcp.Options{
		Logger:     slog.Default(),
		Version:    h.Version,
		Build:      daemon.BuildInfo(h.Build()),
		Config:     h.Config,
		HTTPAddr:   addr,
		StatusBase: h.StatusBase(),
		HealthRoutes: healthRoutes(status, readinessExtras{
			services: bridgeServices,
		}),
	}, Unloaded{
		Root: root,
		Err:  func() rpcerr.Error { return h.registry.unavailable(root) },
	}, h.bridgeServerOptions()...)
	done := make(chan error, 1)
	go func() { done <- d.Serve(srvCtx) }()
	active := make(chan *magus.Magus, 1)
	go func() { active <- h.registry.awaitActive(srvCtx, root) }()
	select {
	case err := <-done:
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "MCP HTTP server failed; initiating server shutdown", attr.Error(err))
			cancel()
		}
	case m := <-active:
		stop()
		<-done // the listener is released before serveBridge binds it
		if m == nil {
			return
		}
		slog.InfoContext(ctx, "workspace loaded; serving every route", slog.String("root", root))
		h.startWatch(ctx, m)
		h.serveBridge(ctx, cancel, m, addr)
	}
}

// bridgeServices is the hosted-services source the console and /readyz read: the broker's,
// since the broker hosts them. Nil when no broker answers. Asking holds nothing, so it
// never keeps the broker awake.
func bridgeServices() []daemon.Service {
	if st := bridgeBroker(); st != nil {
		return st.Services
	}
	return nil
}

// bridgeBroker is the broker's status for the console, nil when none answers.
func bridgeBroker() *daemon.Broker {
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
func (h *Host) bridgeServerOptions() []Option {
	var opts []Option
	// The live-run registry (built by startServer) backs the dashboard's runs view;
	// without it the status report simply omits runs.
	if h.runs != nil {
		opts = append(opts, WithRuns(h.runs.Snapshot))
	}
	// The broker's status backs the dashboard's capacity and services views the same way.
	opts = append(opts, WithBrokerStatus(bridgeBroker))
	// The activity view is server-wide, so it reads every loaded workspace's trail, not just this
	// bridge's: an agent hook runs as a short-lived client outside the server and writes to ITS
	// workspace's cache dir, so a bridge-only view misses every other workspace's agent activity.
	// Same registry the WorkspaceLister reports from.
	if h.registry != nil {
		opts = append(opts, WithActivityWorkspaces(h.registry.activityWorkspaces))
	}
	// The server's own socket carries MCP and the APIs beside its control routes.
	if h.procSrv != nil {
		opts = append(opts, WithSocket(h.procSrv))
	}
	return opts
}

// serveBridge serves MCP, the console and the APIs over HTTP for the loaded bridge
// workspace m. The watch is started separately (startWatch), since it outlives MCP.
func (h *Host) serveBridge(ctx context.Context, cancel context.CancelFunc, m *magus.Magus, addr netip.AddrPort) {
	// Capture the server's own socket now (set by startServer) so the health handlers
	// query this server, not whatever a per-request discovery scan happens to find.
	status := serverSnapshot(h.serverAddr())
	srv := New(internalmcp.Options{
		Magus:      m,
		Logger:     slog.Default(),
		Version:    h.Version,
		Build:      daemon.BuildInfo(h.Build()),
		Config:     h.Config,
		HTTPAddr:   addr,
		StatusBase: h.StatusBase(),
		// ONE diff-session store for the whole server, constructed here because this is
		// where the server's dependencies are assembled. The console's /api/v1/diff routes
		// and the diff MCP tool both read it, and that sharing IS the pairing: a
		// person opens a diff, an agent joins the session they started.
		DiffSessions: changeset.NewStore(m.CacheDir()),
		// The same store the OnJobDone callback completes rows in, so the server's own
		// jobs and the delegated ones are one book with one lock.
		Jobs: h.jobStore,
		// Health endpoints share this HTTP server so k8s probes hit the
		// same port as MCP. Set MAGUS_MCP_ADDRESS=0.0.0.0:7391 (or mcp.address)
		// so the kubelet can reach them (default 127.0.0.1 is pod-local).
		HealthRoutes: healthRoutes(status, readinessExtras{
			symbolIndexes:  m.SymbolIndexStatus,
			services:       bridgeServices,
			knowledgeGraph: m.KnowledgeGraphHealthy,
		}),
	}, h.bridgeServerOptions()...)
	go func() {
		err := srv.Serve(ctx)
		if err != nil && ctx.Err() == nil {
			// Serve exiting due to ctx cancellation is normal shutdown.
			// Any other error means MCP is gone while the server is still up —
			// clients would receive no response indefinitely. Cancel the server
			// context to trigger a clean restart by the process supervisor.
			slog.ErrorContext(ctx, "MCP HTTP server failed; initiating server shutdown", attr.Error(err))
			cancel()
		}
	}()
}

// serverAddr is where the health handlers ask: the server's own socket when startup set it,
// else where the configuration says it listens.
func (h *Host) serverAddr() string {
	return cmp.Or(os.Getenv(proc.SocketEnv), h.Config.Server.Address, proc.ServerDefaultAddr())
}

// readCacheBytes caps the knowledge read cache the server keeps, measured as the
// on-disk size of the shards behind it. This repository's whole store is about 250 MiB.
const readCacheBytes = 512 << 20

// HostDeps is what the process that runs the server gives its Host: the configuration it
// resolved, and the pieces of the command line a workspace is opened and served through.
type HostDeps struct {
	Config  config.Config
	Version string
	// Build is this binary's identity, which the MCP handlers report.
	Build func() types.MagusBuild
	// StatusBase is the part of the status report only the command line can fill in.
	StatusBase func() daemon.Base
	// Load opens the server's own bridge workspace.
	Load func(ctx context.Context, rootOverride string, extra ...magus.Option) (*magus.Magus, error)
	// Place locates root's job store.
	Place func(root string) (job.Location, error)
	// BrokerClient is the broker client the server and its workspaces dial through.
	BrokerClient func(announce bool, opts ...broker.ClientOption) *broker.Client
	// ServeRead answers one graph read from an open workspace.
	ServeRead func(ctx context.Context, m *magus.Magus, cfg config.Config, verb string, request []byte) ([]byte, error)
	// Registry says how the server opens a workspace and runs what it is handed.
	Registry RegistryDeps
}

// Host is the state of a running `magus server`: its proc listener, its per-workspace
// registry, the shared telemetry provider and the live-run registry. The server is one
// process and builds one Host, so the fields are written by Start before the command
// handler reads them.
type Host struct {
	HostDeps

	provider observability.Provider
	registry *wsRegistry
	procSrv  *proc.Server
	runs     *console.RunRegistry
	started  time.Time
	// trailBase is the ONE server-wide activity-trail location: the bridge Magus's cache
	// dir, the same base the MCP handler writes to and the ActivityService reads from.
	// publishTrailBase sets it before anything MCP-gated, since the location is a cache
	// dir rather than anything MCP owns; the proc OnJobDone callback reads it so every
	// producer (MCP calls, background jobs) appends to a single trail, disambiguated by
	// Event.Workspace. Empty only until startup reaches that call, or where the root does
	// not resolve, so a job completing in that window is dropped best-effort.
	trailBase string
	// jobStore is the server's ONE job store, published beside trailBase and for the same
	// reason: the OnJobDone callback needs it, and a second Store over one file would hold
	// its own mutex and serialize against nothing.
	jobStore *job.Store
	httpAddr atomic.Value // string; the HTTP listener, empty while the server serves none
	watch    struct {
		mu    sync.Mutex
		roots []string
	}
	stop    func()
	stopped <-chan struct{}
}

// NewHost is a Host that has not started.
func NewHost(deps HostDeps) *Host { return &Host{HostDeps: deps, stop: func() {}} }

// TrailBase is the server-wide activity-trail location, "" until the bridge publishes it.
func (h *Host) TrailBase() string { return h.trailBase }

// Done is closed when the proc listener is gone, nil before Start has one. An RPC-driven
// `server stop` closes it, and a command that waited only on its signals would keep running
// after its listener was gone.
func (h *Host) Done() <-chan struct{} {
	if h.procSrv == nil {
		return nil
	}
	return h.procSrv.Done()
}

// Stop tears the server down in the background: its socket, the runs it adopted, its
// workspaces. Stopped closes once they have unwound.
func (h *Host) Stop() { go h.stop() }

// Stopped is closed when Stop has finished, nil before Start has built the server.
func (h *Host) Stopped() <-chan struct{} { return h.stopped }

// Hold loads the workspace for root and holds it against eviction until release is called.
func (h *Host) Hold(root string) (m *magus.Magus, release func(), err error) {
	if h.registry == nil {
		return nil, nil, fmt.Errorf("the server has not started")
	}
	return h.registry.hold(root)
}

// Reload drops the workspaces no run holds, so the next command against each reads
// magus.yaml as it stands. It reports how many were dropped and how many were busy.
func (h *Host) Reload() (dropped, busy int) {
	if h.registry == nil {
		return 0, 0
	}
	return h.registry.evictAll()
}

func (h *Host) watching(root string) {
	h.watch.mu.Lock()
	defer h.watch.mu.Unlock()
	if !slices.Contains(h.watch.roots, root) {
		h.watch.roots = append(h.watch.roots, root)
	}
}

// info is the server's own report, read on every Status RPC.
func (h *Host) info(sock string) *daemon.Server {
	exe, _ := os.Executable()
	st := &daemon.Server{
		PID:        os.Getpid(),
		Version:    h.Version,
		Socket:     sock,
		Executable: exe,
		StartTime:  h.started,
		Listeners:  []daemon.Listener{{Kind: daemon.ListenerSocket, Address: sock}},
	}
	if a, _ := h.httpAddr.Load().(string); a != "" {
		st.Listeners = append(st.Listeners, daemon.Listener{Kind: daemon.ListenerHTTP, Address: a})
	}
	h.watch.mu.Lock()
	st.Watch = slices.Clone(h.watch.roots)
	h.watch.mu.Unlock()
	return st
}

// Start builds the server's proc listener, the per-workspace registry and the shared
// telemetry provider for `magus server`. When the configuration declares workspaces it
// eagerly loads them.
//
// The server holds no host capacity and hosts no services: it is a broker client like any
// run. Its workspaces share one broker client, which dials only when an adopted run takes
// a claim, so an idle server never keeps the broker awake.
func (h *Host) Start(ctx context.Context) {
	cfg := h.Config
	// The BAGGAGE of whoever started the daemon names nobody it serves. Every run it
	// adopts restamps its client's lease, and work no client stamped acts under none.
	ctx = types.WithJobLease(ctx, "")
	h.started = time.Now()
	knowledge.SetReadCacheLimit(readCacheBytes)
	n := cfg.Concurrency
	if n <= 0 {
		n = cache.ProfileConcurrency(cfg.ConcurrencyProfile)
	}
	lim := cache.NewLimiter(n)

	ttl := cfg.Server.IdleTTL
	if ttl <= 0 {
		ttl = defaultIdleTTL
	}

	// Build the ONE observability provider the whole server shares: every per-workspace
	// registry Magus AND the bridge Magus adopt this same instance via WithProvider, so a
	// build routed to any workspace records into the same instruments the /dashboard reads,
	// and workspace eviction never discards accumulated counters. The provider is owned by
	// the server process, not any workspace, and is never shut down (magus.Close does not
	// touch it), so sharing it carries no double-shutdown hazard. On init failure fall back
	// to a disabled provider so the server still starts.
	telCfg := observability.ConfigFromTelemetry(cfg.Telemetry, h.Version, "")
	telCfg.LocalCollect = true
	sharedTel, terr := otlp.New(ctx, telCfg)
	if terr != nil {
		slog.With(attr.Component("server")).WarnContext(ctx, "telemetry init failed; dashboard metrics disabled", attr.Error(terr))
		sharedTel, _ = otlp.New(ctx, observability.Config{}) // the empty config builds the disabled provider, which cannot fail
	}
	h.provider = sharedTel

	// The live-run registry taps every adopted dispatch (threaded onto its context below) and
	// backs the dashboard's active-runs view via the console service.
	h.runs = console.NewRunRegistry()

	brokerClient := h.BrokerClient(false, broker.WithLease(""))
	declared := ResolveDeclaredWorkspaces(ctx, cfg.Server.Workspaces, os.Getenv("MAGUS_SERVER_WORKSPACES"))
	reg := newRegistry(ctx, lim, brokerClient, ttl, sharedTel, h.Registry)
	reg.setDeclared(declared)
	h.registry = reg // publish so the bridge can adopt its workspace into it

	if len(declared) > 0 {
		reg.warmInBackground(ctx, declared)
	}

	var addr string
	srv, err := proc.New(ctx, proc.Options{
		Handler: func(hctx context.Context, args []string) error {
			root := proc.RootFromContext(hctx)
			if root == "" {
				cwd := proc.CurrentWorkingDirectoryFromContext(hctx)
				r, rerr := workspace.FindRoot(cwd)
				if rerr != nil {
					return fmt.Errorf("locate workspace root from %s: %w", cwd, rerr)
				}
				root = r
			}
			// Fold this adopted run's journal into the live-run registry so the dashboard
			// sees its per-target execution state. BeginInvocation (in run/affected) reads
			// the sink off the context and attaches it as an extra capture handler.
			hctx = console.WithRunSink(hctx, h.runs)
			return reg.dispatch(hctx, root, args)
		},
		Read:            graphReads(reg, h.ServeRead),
		OnJobDone:       h.recordJobActivity,
		WorkspaceLister: reg.status,
		ConfigReloader:  reg.evictAll,
		Server:          func() *daemon.Server { return h.info(addr) },
		Limiter:         lim,
		Version:         h.Version,
		Address:         cfg.Server.Address,
		// The command answers them: SIGHUP reloads, and a stop is bounded by shutdown_grace.
		CallerOwnsSignals: true,
	})
	if err != nil {
		slog.With(attr.Component("server")).ErrorContext(ctx, "init failed", attr.Error(err))
		return
	}
	addr = srv.Addr()
	_ = os.Setenv(proc.SocketEnv, addr)       // Setenv fails only on an invalid key, and these are constants
	_ = os.Setenv(proc.TokenEnv, srv.Token()) // Setenv fails only on an invalid key, and these are constants
	if err := srv.Start(ctx); err != nil {
		_ = os.Unsetenv(proc.SocketEnv) // Unsetenv fails only on an invalid key, and these are constants
		_ = os.Unsetenv(proc.TokenEnv)  // Unsetenv fails only on an invalid key, and these are constants
		slog.With(attr.Component("server")).ErrorContext(ctx, "start failed", attr.Error(err))
		return
	}
	h.procSrv = srv // publish so the command's blocking loop unblocks on an RPC shutdown
	var once sync.Once
	stopped := make(chan struct{})
	h.stopped = stopped
	h.stop = func() {
		once.Do(func() {
			// Drain in-flight handlers (srv.Close cancels them and waits on connWg) before
			// reg.close so a workspace can't be closed under an in-flight build.
			srv.Close()
			reg.close()
			_ = brokerClient.Close() // shutting down: the broker drops a lease whose client is gone
			close(stopped)
		})
	}
	go func() {
		// An RPC `server stop` closes srv.Done without touching ctx, and a server that
		// waited only on ctx leaked its warm workspaces.
		select {
		case <-ctx.Done():
		case <-srv.Done():
		}
		h.stop()
	}()
}

// graphReads answers the CLI's graph reads from the workspaces reg holds, opening one on
// its first read like any other request. serve answers a read from an open workspace.
func graphReads(reg *wsRegistry, serve func(ctx context.Context, m *magus.Magus, cfg config.Config, verb string, request []byte) ([]byte, error)) func(ctx context.Context, root, verb string, request []byte) ([]byte, error) {
	return func(ctx context.Context, root, verb string, request []byte) ([]byte, error) {
		if root == "" {
			return nil, fmt.Errorf("%w: a read names no workspace", proc.ErrNotAdoptable)
		}
		// Resolved per read rather than kept from the open, so the read honors magus.yaml
		// as it is now, as the client's own read would.
		cfg, err := LoadWorkspaceConfig(root)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", proc.ErrNotAdoptable, err)
		}
		e, err := reg.acquire(root)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", proc.ErrNotAdoptable, err)
		}
		defer reg.release(e)
		return serve(ctx, e.m, cfg, verb, request)
	}
}

// recordJobActivity appends a KIND_JOB event to the server-wide activity trail after a background
// job (reindex, graph build, VCS refresh) completes. It is the proc OnJobDone callback. The event
// carries the job's workspace root (resolved from the context the same way the run handler does,
// since the context holds the caller's cwd, not necessarily the root) so the single trail stays
// disambiguated. Best-effort: an unresolvable root or an unset trail base (bridge not up) drops
// the record. Ts is the job's start, completion minus its measured duration.
func (h *Host) recordJobActivity(ctx context.Context, args []string, dur time.Duration, err error) {
	base := h.trailBase
	if base == "" {
		return
	}
	root := proc.RootFromContext(ctx)
	if root == "" {
		resolved, ferr := workspace.FindRoot(proc.CurrentWorkingDirectoryFromContext(ctx))
		if ferr != nil {
			return
		}
		root = resolved
	}
	ev := trail.Event{
		Ts:         time.Now().Add(-dur).UnixMilli(),
		Kind:       trail.KindJob,
		Origin:     types.Origin{EntryPoint: types.EntryPointServer},
		Workspace:  root,
		Action:     job.ActionString(args),
		Outcome:    trail.OutcomeOK,
		DurationMs: dur.Milliseconds(),
	}
	if err != nil {
		ev.Outcome = trail.OutcomeError
		ev.Error = err.Error()
	}
	trail.Append(ctx, base, ev)
	completeJobRow(ctx, h.jobStore, args, dur, err)
}

// completeJobRow finishes a catalog job's row: where it now stands, what the run cost, and
// whether it worked. The invocation id is not here to record (this callback is handed argv,
// duration and error only), so it merges into the row the submit left.
//
// Best-effort, like the trail append above it. The store refuses a write from a checkout
// bound to a lease, and background maintenance must not fail because a worker holds this one.
func completeJobRow(ctx context.Context, store *job.Store, args []string, dur time.Duration, jobErr error) {
	if store == nil {
		return
	}
	i := slices.IndexFunc(job.All(), func(j job.CatalogEntry) bool { return slices.Equal(j.Argv, args) })
	if i < 0 {
		return // an adopted run rather than one of the server's own, so there is no row
	}
	catalog := job.All()[i]
	ctx = trail.ContextWithEntryPoint(ctx, types.EntryPointServer)
	if _, err := store.Update(ctx, catalog.Name, func(row *types.Job) {
		row.State = types.StatePass
		if jobErr != nil {
			row.State = types.StateFail
		}
		if row.LastRun == nil {
			row.LastRun = &types.JobRun{}
		}
		row.LastRun.Ended = time.Now().UnixMilli()
		row.LastRun.DurationMs = dur.Milliseconds()
		row.LastRun.OK = jobErr == nil
		row.LastRun.Error = ""
		if jobErr != nil {
			row.LastRun.Error = jobErr.Error()
		}
	}); err != nil {
		slog.DebugContext(ctx, "completing the job's row failed",
			slog.String("job", catalog.Name), attr.Error(err))
	}
}

// MCPAddress returns the MCP host:port for a given MCP config, falling back to the
// default when unset. It takes the config explicitly (rather than reading globalCfg)
// so callers that already hold a config.MCP (including tests) resolve the address
// without touching package state.
func MCPAddress(mcp config.MCP) string {
	if mcp.Address != "" {
		return mcp.Address
	}
	return internalmcp.DefaultAddress
}

// WriteMCPUsage is `magus mcp --help` for cfg. The server's transports are named as cfg leaves them:
// no HTTP URL is printed when the server would not serve one.
func WriteMCPUsage(w io.Writer, cfg config.MCP) {
	fmt.Fprintln(w, "Usage: magus mcp")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Serve MCP over stdin and stdout for the agent host that launched this process,")
	fmt.Fprintln(w, "against the workspace it was launched in. No server and no token: the caller")
	fmt.Fprintln(w, "is the local process the host started, holding mcp=write. It stops when the")
	fmt.Fprintln(w, "host closes stdin.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Register it with an agent host as a project-scoped stdio server, so each")
	fmt.Fprintln(w, "checkout's agent reaches that checkout at its own build:")
	fmt.Fprintln(w, "  command  ./magus")
	fmt.Fprintln(w, `  args     ["mcp"]`)
	fmt.Fprintln(w, "magus describe harness <host> prints the registration for a known host.")
	fmt.Fprintln(w, "")
	switch {
	case cfg.Enabled != nil && !*cfg.Enabled:
		fmt.Fprintln(w, "magus server start serves no MCP here (mcp.enabled=false); stdio above is the")
		fmt.Fprintln(w, "only transport.")
	case !cfg.HTTPEnabled():
		fmt.Fprintln(w, "MCP over HTTP is off here (mcp.http=false). Besides stdio above, magus server")
		fmt.Fprintln(w, "start serves MCP at /mcp on its own unix socket, admitting processes that run")
		fmt.Fprintln(w, "as you without a token. It answers from the checkout and build that started it.")
	default:
		fmt.Fprintln(w, "magus server start also serves MCP over Streamable HTTP, for the console and")
		fmt.Fprintln(w, "clients with no shell. It answers from the checkout and build that started it:")
		fmt.Fprintf(w, "  url        http://%s/mcp\n", MCPAddress(cfg))
		fmt.Fprintln(w, "  auth       Authorization: Bearer <token>, minted with")
		fmt.Fprintln(w, "             magus config mcp connector create --name <client> --expires 366d")
		fmt.Fprintln(w, "  check      magus server status --probe=liveness,mcp")
	}
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Per-client configuration: docs/guides/integrations/mcp.md")
}

// StdioIdentity is what ServeMCPStdio reports about the process serving: its configuration
// and the build it answers for.
type StdioIdentity struct {
	Config  config.Config
	Version string
	Build   func() types.MagusBuild
}

// ServeMCPStdio serves MCP for m with wire as the protocol's output. For as long as it
// serves, os.Stdout points at os.Stderr, so a stray print or a child process handed
// os.Stdout lands on stderr rather than between two frames a host is parsing.
//
// It keeps m's graph and symbol indexes current while it serves, as the server does for
// its own workspace: a session lives as long as the agent's, so graph reads answer warm.
func ServeMCPStdio(ctx context.Context, m *magus.Magus, in io.Reader, wire io.Writer, id StdioIdentity) error {
	addr, err := netip.ParseAddrPort(MCPAddress(id.Config.MCP))
	if err != nil {
		return fmt.Errorf("invalid mcp.address: %w", err)
	}
	stdout := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = stdout }()

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	watchWorkspace(ctx, m)

	slog.InfoContext(ctx, fmt.Sprintf("serving MCP over stdio for %s in-process, so a stopped daemon cannot break it; close stdin or press Ctrl+C to stop (magus mcp --help shows how to register it)", m.Root()),
		attr.Notice(""), attr.Component("magus"))
	return internalmcp.ServeStdio(ctx, internalmcp.Options{
		Magus:    m,
		Logger:   slog.Default(),
		Version:  id.Version,
		Build:    daemon.BuildInfo(id.Build()),
		Config:   id.Config,
		HTTPAddr: addr,
		// The store the server and `magus diff` use, so an agent joins the session a person
		// already has open.
		DiffSessions: changeset.NewStore(m.CacheDir()),
	}, in, wire)
}

// watchWorkspace keeps m's knowledge graph and symbol indexes current until ctx ends: the
// warm graph is built at start and rebuilt in the background after each source change, so
// queries answer from memory without re-parsing every magusfile per call, and the indexer
// re-runs SCIP in the background, throttled, starting with every index already stale at
// start. Neither is fatal: queries fall back to a cache-first rebuild, and symbols go
// stale until a manual `magus run ::scip`. It reports whether the symbol indexer started.
func watchWorkspace(ctx context.Context, m *magus.Magus) bool {
	if _, werr := m.WatchKnowledgeGraph(ctx); werr != nil {
		slog.WarnContext(ctx, "knowledge-graph watcher unavailable; queries will rebuild per call", attr.Error(werr))
	}
	if _, werr := m.WatchSymbolIndexing(ctx); werr != nil {
		slog.WarnContext(ctx, "symbol auto-indexer unavailable; symbol indexes will not refresh automatically", attr.Error(werr))
		return false
	}
	return true
}
