//go:build mcp

// Package mcp implements the MCP (Model Context Protocol) server for magus.
// It is started alongside the daemon (`magus server start`) and serves over
// Streamable HTTP so multiple MCP clients can connect concurrently.
//
// Every tool call stamps context.WithValue markers via origin.WithContext so
// downstream goroutines (cache, spell) can attribute work to an agent
// origin. A banner log line is emitted before and after each tool call so
// the human watching magus's stderr can immediately see when an agent triggers
// an operation.
package mcp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/mcp/auth"
	"github.com/egladman/magus/internal/webbridge"
)

// DefaultAddress is the default host:port for the MCP Streamable HTTP server.
const DefaultAddress = "127.0.0.1:7391"

// defaultAddrPort is the parsed form of DefaultAddress, used by httpAddr().
var defaultAddrPort = netip.MustParseAddrPort(DefaultAddress)

// serverInstructions is the system-level hint sent to the client during
// the initialize handshake.
const serverInstructions = `You are connected to a magus workspace.
magus is a build orchestrator for multi-language monorepos.

Workflow:
  1. magus_list_projects   — see what projects exist
  2. magus_list_targets    — see available build targets per project
  3. magus_run_target      — run build/test/lint/format/generate/ci
  4. magus_run_affected    — run a target on only VCS-changed projects
  5. magus_describe_project — explain why a project is affected by VCS changes
  6. magus_doctor          — validate the workspace health
  7. magus_status          — inspect the live concurrency pool
  8. magus_affected_plan   — emit a CI shard plan for the affected set
  9. magus_config_get      — view the resolved workspace config (read-only)
 10. magus_tail_log        — retrieve the captured build log for a project
 11. magus_where           — resolve a fuzzy project name to its absolute path

Config mutation is intentionally not exposed. Use the magus CLI for that.`

// agentFromRequest extracts the client name/version from an initialize request.
func agentFromRequest(req *mcp.InitializeRequest) string {
	name := req.Params.ClientInfo.Name
	ver := req.Params.ClientInfo.Version
	if ver != "" {
		name = name + "/" + ver
	}
	if name == "" {
		return "unknown"
	}
	return name
}

// buildServer constructs the MCPServer with the standard magus options and
// registers all tools. The three transports (newServer, ServeHTTP, ServeStdio)
// share this so the server name, instructions, capabilities, recovery, and tool
// set can never drift between them; each caller supplies only the
// transport-specific hooks (agent tracking) and the agentFn used at tool-call time.
func buildServer(opts ServerOptions, log *slog.Logger, hooks *mcpserver.Hooks, agentFn func(context.Context) string) *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer(
		"magus", opts.Version,
		mcpserver.WithInstructions(serverInstructions),
		mcpserver.WithToolCapabilities(false),
		mcpserver.WithHooks(hooks),
		mcpserver.WithRecovery(),
	)
	registerTools(srv, opts, log, agentFn)
	return srv
}

// newServer constructs and registers tools on a new MCPServer. agentFn is called
// with the current request ctx to resolve the agent client identity at tool-call
// time so each handler's wrap closure captures the right per-session value.
func newServer(opts ServerOptions, log *slog.Logger, agentFn func(context.Context) string) *mcpserver.MCPServer {
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeInitialize(func(_ context.Context, _ any, req *mcp.InitializeRequest) {
		agent := agentFromRequest(req)
		log.Info("[AGENT] client connected", slog.String("agent", agent))
	})
	return buildServer(opts, log, hooks, agentFn)
}

// ServeHTTP starts the MCP server as a Streamable HTTP server, blocking until
// ctx is cancelled or the server fails. This is the transport used in daemon
// mode — multiple MCP clients can connect concurrently.
func ServeHTTP(ctx context.Context, opts ServerOptions) error {
	if err := opts.validate(); err != nil {
		return err
	}
	log := opts.logger()
	addr := opts.httpAddr()

	// Provision the bearer token before serving. Fail closed: if it can't be
	// loaded or generated, the MCP endpoint never comes up. Guard re-reads the
	// token via auth.Load on each request, so token rotations take effect
	// without a daemon restart.
	if _, err := auth.Resolve(log); err != nil {
		return err
	}

	// sessionAgents maps sessionID → agent client identifier, populated by the
	// BeforeInitialize hook and cleaned up on session unregister. This avoids
	// the server-wide atomic.Value which would race across concurrent clients.
	var sessionAgents sync.Map

	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeInitialize(func(hCtx context.Context, _ any, req *mcp.InitializeRequest) {
		agent := agentFromRequest(req)
		if session := mcpserver.ClientSessionFromContext(hCtx); session != nil {
			sessionAgents.Store(session.SessionID(), agent)
		}
		log.Info("[AGENT] client connected", slog.String("agent", agent))
	})
	hooks.AddOnUnregisterSession(func(_ context.Context, session mcpserver.ClientSession) {
		sessionAgents.Delete(session.SessionID())
	})

	agentFn := func(tCtx context.Context) string {
		if session := mcpserver.ClientSessionFromContext(tCtx); session != nil {
			if v, ok := sessionAgents.Load(session.SessionID()); ok {
				return v.(string)
			}
		}
		return "unknown"
	}

	srv := buildServer(opts, log, hooks, agentFn)

	// Serve the MCP Streamable-HTTP handler and any health routes from one
	// mux/listener so health probes share the MCP port — no second
	// http.Server. StreamableHTTPServer is a path-agnostic http.Handler;
	// mounting it at /mcp matches the path its own Start() would use.
	//
	// dnsRebindGuard and authGuard are applied only to /mcp. Health routes are
	// left unguarded so container orchestrators can probe them freely. The
	// rebind check runs outermost so a forged cross-origin browser request is
	// rejected before the bearer token is even examined; authGuard then enforces
	// the shared secret on everything that gets past it.
	mcpHandler := mcpserver.NewStreamableHTTPServer(srv)
	allowed := allowedHosts(addr)
	mux := http.NewServeMux()
	mux.Handle("/mcp", dnsRebindGuard(allowed, auth.Guard(auth.Load, mcpHandler)))
	for path, h := range opts.HealthRoutes {
		mux.Handle(path, h)
	}

	// Web bridge: three frozen GET routes for the browser Graph Explorer.
	// Mounted only when:
	//   1. bridge.enabled is unset or true (opt-out via bridge.enabled: false)
	//   2. The bind address is loopback (non-loopback binding refuses the mount)
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
				inv = webbridge.WatchInvalidate(ctx, bWatcher)
				go func() {
					<-ctx.Done()
					_ = bWatcher.Close()
				}()
			}

			siteOrigin, _ := opts.siteOrigin()
			bridgeOpts := webbridge.Options{
				Magus:           opts.Magus,
				Config:          opts.Config,
				Addr:            addr,
				SiteOrigin:      siteOrigin,
				GraphInvalidate: inv,
			}
			// The bridge routes share the same auth and DNS-rebind middleware as /mcp.
			bridgeMux := http.NewServeMux()
			webbridge.Mount(bridgeMux, bridgeOpts)
			// Wrap every /api/ route with rebind + auth.
			mux.Handle("/api/", dnsRebindGuard(allowed, auth.Guard(auth.Load, bridgeMux)))
			log.Info("[BRIDGE] web bridge mounted", slog.String("addr", addr.String()))
		}
	}

	httpServer := &http.Server{Addr: addr.String(), Handler: mux}

	errCh := make(chan error, 1)
	go func() {
		log.Info("[AGENT] HTTP server starting", slog.String("addr", addr.String()))
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutCtx); err != nil {
			log.Warn("[AGENT] shutdown error", slog.String("error", err.Error()))
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// ServeStdio runs the magus MCP server over standard I/O, blocking until
// stdin closes or the context is cancelled. Kept for testing and scripted
// smoke-checks; daemon mode uses ServeHTTP instead.
func ServeStdio(ctx context.Context, opts ServerOptions) error {
	if err := opts.validate(); err != nil {
		return err
	}
	log := opts.logger()

	// Stdio is single-client; track the agent atomically so the BeforeInitialize
	// write and the agentFn reads across worker goroutines are race-free.
	var currentAgent atomic.Value
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeInitialize(func(_ context.Context, _ any, req *mcp.InitializeRequest) {
		agent := agentFromRequest(req)
		currentAgent.Store(agent)
		log.Info("[AGENT] client connected", slog.String("agent", agent))
	})

	agentFn := func(_ context.Context) string {
		if v, ok := currentAgent.Load().(string); ok && v != "" {
			return v
		}
		return "unknown"
	}

	srv := buildServer(opts, log, hooks, agentFn)

	log.Info("[AGENT] stdio server started")
	return mcpserver.NewStdioServer(srv).Listen(ctx, os.Stdin, os.Stdout)
}
