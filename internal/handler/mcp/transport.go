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
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/trail"
)

// uaCtxKey keys the client's HTTP User-Agent in a request context. It is set by
// the Streamable-HTTP transport's WithHTTPContextFunc before any hook runs, so
// the initialize hook can read it back and fold it into the session identity.
type uaCtxKey struct{}

// withUserAgent returns ctx carrying the client's raw HTTP User-Agent header.
func withUserAgent(ctx context.Context, ua string) context.Context {
	return context.WithValue(ctx, uaCtxKey{}, ua)
}

// userAgentFromContext returns the User-Agent stashed by withUserAgent, or ""
// over stdio or before the context func has run.
func userAgentFromContext(ctx context.Context) string {
	ua, _ := ctx.Value(uaCtxKey{}).(string)
	return ua
}

// unknownOrigin is the fallback identity for a tool call whose session was never
// seen at initialize time (e.g. a race, or a client that skipped the handshake).
var unknownOrigin = origin.Origin{Agent: "unknown"}

// sseHeartbeat is how often the Streamable-HTTP server pings an open GET (SSE)
// stream. mark3labs disables heartbeats by default; enabling them keeps a
// long-lived server-to-client stream from being closed by an idle timeout - a
// no-op on a pure loopback client, but the correct default the moment the
// endpoint is reached through any proxy or gateway (e.g. a non-loopback bind).
const sseHeartbeat = 30 * time.Second

// DefaultAddress is the default host:port for the MCP Streamable HTTP server.
const DefaultAddress = "127.0.0.1:7391"

// defaultAddrPort is the parsed form of DefaultAddress, used by httpAddr().
var defaultAddrPort = netip.MustParseAddrPort(DefaultAddress)

// serverInstructions is the system-level hint sent to the client during
// the initialize handshake.
const serverInstructions = `You are connected to a magus workspace.
magus is a build orchestrator for multi-language monorepos.

Discover:
  magus_describe          - list spells, targets, projects, workspaces, or mcp_tools
  magus_where             - resolve a fuzzy project name to its absolute path
  magus_config_get        - view the resolved workspace config (read-only)

Run:
  magus_run_target        - run build/test/lint/format/generate/ci
  magus_run_affected      - run a target on only VCS-changed projects
  magus_affected_plan     - emit a CI shard plan for the affected set
  magus_affected_explain  - explain why a project is affected by VCS changes

Inspect:
  magus_doctor            - validate the workspace health
  magus_status            - inspect the live concurrency pool
  magus_tail_log          - retrieve the captured build log for a project
  magus_output            - fetch a target-output blob by its reference id
  magus_insight           - VCS history lenses (hotspots, ownership, trend)

Knowledge graph:
  magus_query             - search the target/spell/symbol graph
  magus_explain           - explain a single node and its relationships
  magus_path              - find a path between two graph nodes
  magus_refs              - list files that reference a symbol
  magus_stats             - summarize graph composition

Typical flow:
  Discover first: magus_describe (list spells/targets/projects/workspaces), magus_where (resolve a fuzzy project name to a path).
  Then act: magus_run_target / magus_run_affected; magus_affected_plan (CI shard plan), magus_affected_explain (why a project is affected).
  After a run: magus_output (fetch a target's captured output by its ref), magus_tail_log (latest cache log for a project).
  Understand the graph: magus_query (search) -> magus_explain (a node's edges and provenance) -> magus_path (shortest path); magus_refs (symbol defs and refs); magus_stats (graph shape).
  Health and meta: magus_status, magus_doctor, magus_config_get, magus_scratchpad.

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
// transport-specific hooks (agent tracking) and the originFn used at tool-call time.
func buildServer(opts Options, log *slog.Logger, hooks *mcpserver.Hooks, originFn func(context.Context) origin.Origin) *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer(
		"magus", opts.Version,
		mcpserver.WithInstructions(serverInstructions),
		mcpserver.WithToolCapabilities(false),
		mcpserver.WithHooks(hooks),
		mcpserver.WithRecovery(),
	)
	// The activity trail is an append-only JSONL sidecar under the cache dir (next to the
	// journal run logs). Writes are stateless (open/append/close per event). Rotate here trims
	// it once at construction; wrap then appends per tool call and drives a periodic rotate off
	// the append count (trail.RotateEvery) so a long-lived daemon's trail stays bounded rather
	// than only at boot. An empty cacheDir makes every trail call a no-op, so a read-only or
	// dirless workspace never blocks serving.
	var cacheDir string
	if opts.Magus != nil {
		cacheDir = opts.Magus.CacheDir()
	}
	trail.Rotate(cacheDir)
	registerTools(srv, opts, log, originFn, cacheDir)
	return srv
}

// newServer constructs and registers tools on a new MCPServer. originFn is called
// with the current request ctx to resolve the caller identity at tool-call
// time so each handler's wrap closure captures the right per-session value.
func newServer(opts Options, log *slog.Logger, originFn func(context.Context) origin.Origin) *mcpserver.MCPServer {
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeInitialize(func(_ context.Context, _ any, req *mcp.InitializeRequest) {
		agent := agentFromRequest(req)
		log.Info("[AGENT] client connected", slog.String("agent", agent))
	})
	return buildServer(opts, log, hooks, originFn)
}

// HTTPHandler builds the MCP Streamable-HTTP handler for daemon mode: it
// validates opts, wires per-session origin tracking, and returns the bare MCP
// handler. It mounts no routes and opens no listener - the daemon package owns
// the HTTP server assembly (guards, health routes, console) so this package
// need not depend on the httpx server core, the dashboard bridge, or the file
// watcher. The returned handler is a path-agnostic http.Handler; the daemon
// mounts it at /mcp, matching the path StreamableHTTPServer's own Start() would use.
func HTTPHandler(opts Options) (http.Handler, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	log := opts.logger()

	// sessionOrigins maps sessionID → origin.Origin (clientInfo + User-Agent),
	// populated by the BeforeInitialize hook and cleaned up on session unregister.
	// This avoids the server-wide atomic.Value which would race across concurrent
	// clients.
	var sessionOrigins sync.Map

	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeInitialize(func(hCtx context.Context, _ any, req *mcp.InitializeRequest) {
		// clientInfo comes off the initialize params; the User-Agent was stashed
		// on hCtx by the WithHTTPContextFunc below, which runs per HTTP request
		// before the message (and thus this hook) is dispatched.
		o := origin.Origin{Agent: agentFromRequest(req), UserAgent: userAgentFromContext(hCtx)}
		if session := mcpserver.ClientSessionFromContext(hCtx); session != nil {
			sessionOrigins.Store(session.SessionID(), o)
		}
		attrs := []any{slog.String("agent", o.Agent)}
		if o.UserAgent != "" { // omit an empty field so the line stays clean over headerless clients
			attrs = append(attrs, slog.String("user_agent", o.UserAgent))
		}
		log.Info("[AGENT] client connected", attrs...)
	})
	hooks.AddOnUnregisterSession(func(_ context.Context, session mcpserver.ClientSession) {
		sessionOrigins.Delete(session.SessionID())
	})

	originFn := func(tCtx context.Context) origin.Origin {
		if session := mcpserver.ClientSessionFromContext(tCtx); session != nil {
			// Comma-ok on the assertion too: fall back to unknownOrigin rather than
			// panic if a non-Origin value is ever stored under a session id.
			if v, ok := sessionOrigins.Load(session.SessionID()); ok {
				if o, ok := v.(origin.Origin); ok {
					return o
				}
			}
		}
		return unknownOrigin
	}

	srv := buildServer(opts, log, hooks, originFn)
	return mcpserver.NewStreamableHTTPServer(srv,
		mcpserver.WithHeartbeatInterval(sseHeartbeat),
		// Lift the client's User-Agent off the live *http.Request into the request
		// context so the initialize hook can capture it. stdio has no equivalent -
		// it carries no request headers - so this signal is HTTP-only by nature.
		mcpserver.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return withUserAgent(ctx, r.Header.Get("User-Agent"))
		}),
	), nil
}

// ServeStdio runs the magus MCP server over standard I/O, blocking until
// stdin closes or the context is cancelled. Kept for testing and scripted
// smoke-checks; daemon mode uses ServeHTTP instead.
func ServeStdio(ctx context.Context, opts Options) error {
	if err := opts.validate(); err != nil {
		return err
	}
	log := opts.logger()

	// Stdio is single-client; track the origin atomically so the BeforeInitialize
	// write and the originFn reads across worker goroutines are race-free. Stdio
	// carries no request headers, so UserAgent is always empty here.
	var currentOrigin atomic.Value
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeInitialize(func(_ context.Context, _ any, req *mcp.InitializeRequest) {
		agent := agentFromRequest(req)
		currentOrigin.Store(origin.Origin{Agent: agent})
		log.Info("[AGENT] client connected", slog.String("agent", agent))
	})

	originFn := func(_ context.Context) origin.Origin {
		if o, ok := currentOrigin.Load().(origin.Origin); ok && o.Agent != "" {
			return o
		}
		return unknownOrigin
	}

	srv := buildServer(opts, log, hooks, originFn)

	log.Info("[AGENT] stdio server started")
	return mcpserver.NewStdioServer(srv).Listen(ctx, os.Stdin, os.Stdout)
}
