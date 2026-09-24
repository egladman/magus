package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// serverProvider is the single observability provider the server shares between its
// per-workspace registry and its bridge Magus. startServer builds it once (it runs before
// the command handler); startBridge then adopts it. Same process, sequential, so the write
// happens-before the read.
var serverProvider observability.Provider

// serverRegistry is the server's per-workspace registry. It is the single source of truth
// the WorkspaceLister reports, so startBridge publishes the bridge workspace into it
// (reg.adoptBridge) and /readyz sees the server's own workspace as loaded, not just the
// workspaces adopted runs populated. Same process, sequential.
var serverRegistry *wsRegistry

// procServer is the server's proc listener. startServer publishes it so serverStart's
// blocking loop can select on its Done channel: an RPC-driven `server stop` closes it,
// and without observing that the process would keep running after its listener was gone.
var procServer *proc.Server

// serverRuns is the server's live-run registry: a capture handler folded into every
// adopted dispatch's journal, tracking per-target execution state. The console service
// reads its Snapshot so the StatusService and the status SSE report active runs.
var serverRuns *console.RunRegistry

// serverTrailBase is the ONE server-wide activity-trail location: the bridge Magus's cache
// dir, the same base the MCP handler writes to and the ActivityService reads from.
// publishServerTrailBase sets it before anything MCP-gated, since the location is a cache
// dir rather than anything MCP owns; the proc OnJobDone callback reads it so every producer
// (MCP calls, background jobs) appends to a single trail, disambiguated by Event.Workspace.
// Empty only until startup reaches that call, or where the root does not resolve, so a job
// completing in that window is dropped best-effort.
var serverTrailBase string

// serverStarted is when this process became the server.
var serverStarted time.Time

// serverHTTPAddr is the HTTP listener the server serves MCP and the console on, empty while
// it serves none. The proc Status RPC reads it on every request.
var serverHTTPAddr atomic.Value // string

// serverWatch is the workspace roots whose graph and symbol indexes the server keeps
// current.
var serverWatch struct {
	mu    sync.Mutex
	roots []string
}

func watching(root string) {
	serverWatch.mu.Lock()
	defer serverWatch.mu.Unlock()
	if !slices.Contains(serverWatch.roots, root) {
		serverWatch.roots = append(serverWatch.roots, root)
	}
}

// serverInfo is the server's own report, read on every Status RPC.
func serverInfo(sock string) *types.StatusServer {
	exe, _ := os.Executable()
	st := &types.StatusServer{
		PID:        os.Getpid(),
		Version:    version,
		Socket:     sock,
		Executable: exe,
		StartTime:  serverStarted,
		Listeners:  []types.StatusListener{{Kind: types.ListenerSocket, Address: sock}},
	}
	if a, _ := serverHTTPAddr.Load().(string); a != "" {
		st.Listeners = append(st.Listeners, types.StatusListener{Kind: types.ListenerHTTP, Address: a})
	}
	serverWatch.mu.Lock()
	st.Watch = slices.Clone(serverWatch.roots)
	serverWatch.mu.Unlock()
	return st
}

// startServer builds the server's proc listener, the per-workspace registry and the
// shared telemetry provider for `magus server`. When cfg.Daemon.Workspaces is non-empty it
// eagerly loads the declared workspaces and applies landlock.
//
// The server holds no host capacity and hosts no services: it is a broker client like any
// run. Its workspaces share one broker client, which dials only when an adopted run takes
// a claim, so an idle server never keeps the broker awake.
func startServer(ctx context.Context, cfg config.Config, rc runConfig) {
	serverStarted = time.Now()
	n := cfg.Concurrency
	if n <= 0 {
		n = cache.ProfileConcurrency(cfg.ConcurrencyProfile)
	}
	lim := cache.NewLimiter(n)

	ttl := cfg.Daemon.IdleTTL
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
	telCfg := observability.ConfigFromTelemetry(cfg.Telemetry, version, "")
	telCfg.LocalCollect = true
	sharedTel, terr := otlp.New(ctx, telCfg)
	if terr != nil {
		slog.Warn("server: telemetry init failed; dashboard metrics disabled", slog.String("error", terr.Error()))
		sharedTel, _ = otlp.New(ctx, observability.Config{})
	}
	serverProvider = sharedTel

	// The live-run registry taps every adopted dispatch (threaded onto its context below) and
	// backs the dashboard's active-runs view via the console service.
	serverRuns = console.NewRunRegistry()

	brokerClient := broker.NewClient(broker.DefaultAddr(), broker.WithIdentity(os.Args, version))
	declared := resolveDeclaredWorkspaces(cfg.Daemon.Workspaces, os.Getenv("MAGUS_DAEMON_WORKSPACES"))
	reg := newWSRegistry(ctx, lim, brokerClient, ttl, sharedTel)
	reg.setDeclared(declared)
	serverRegistry = reg // publish so startBridge can adopt the bridge workspace into it

	if len(declared) > 0 {
		if err := reg.preloadAndApplySandbox(ctx, declared); err != nil {
			slog.Error("server: workspace union setup failed", slog.String("error", err.Error()))
			return
		}
		reg.warmInBackground(ctx, declared)
	}

	var addr string
	srv, err := proc.New(proc.Options{
		Handler: func(hctx context.Context, args []string) error {
			root := proc.RootFromContext(hctx)
			if root == "" {
				cwd := proc.CwdFromContext(hctx)
				r, rerr := magus.FindRoot(cwd)
				if rerr != nil {
					return fmt.Errorf("proc: cannot locate workspace root from %s: %w", cwd, rerr)
				}
				root = r
			}
			// An adopted run takes claims like any run, and a run is what starts the
			// broker, so the server starts one on the same terms; quietly, since the
			// person reading this is not watching the server's log.
			if globalCfg.Broker.Resolved() != types.BrokerOff {
				ensureBroker(hctx)
			}
			// Fold this adopted run's journal into the live-run registry so the dashboard
			// sees its per-target execution state. BeginInvocation (in run/affected) reads
			// the sink off the context and attaches it as an extra capture handler.
			hctx = console.WithRunSink(hctx, serverRuns)
			return reg.dispatch(hctx, root, rc, args)
		},
		OnJobDone:       recordJobActivity,
		WorkspaceLister: reg.status,
		ConfigReloader:  reg.evictAll,
		Server:          func() *types.StatusServer { return serverInfo(addr) },
		Context:         ctx,
		Limiter:         lim,
		Version:         version,
		Address:         cfg.Daemon.Address,
	})
	if err != nil {
		slog.Error("server: init failed", slog.String("error", err.Error()))
		return
	}
	addr = srv.Addr()
	_ = os.Setenv("MAGUS_DAEMON_SOCKET", addr)
	if err := srv.Start(); err != nil {
		_ = os.Unsetenv("MAGUS_DAEMON_SOCKET")
		slog.Error("server: start failed", slog.String("error", err.Error()))
		return
	}
	procServer = srv // publish so serverStart's blocking loop unblocks on an RPC shutdown
	go func() {
		// Tear down on either path: a signal (ctx cancelled via NotifyContext) or an RPC
		// `server stop` (which calls srv.Close, closing srv.Done). Waiting only on ctx.Done
		// missed the RPC path (srv.Close cancels the listener's own context, not this one),
		// so a stopped server leaked its warm workspaces.
		select {
		case <-ctx.Done():
		case <-srv.Done():
		}
		// Drain in-flight handlers (srv.Close waits on connWg) before reg.close so a
		// workspace can't be closed under an in-flight build. Close is idempotent.
		srv.Close()
		reg.close()
		_ = brokerClient.Close()
	}()
}
