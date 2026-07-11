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
)

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
func buildServer(opts Options, log *slog.Logger, hooks *mcpserver.Hooks, agentFn func(context.Context) string) *mcpserver.MCPServer {
	srv := mcpserver.NewMCPServer(
		"magus", opts.Version,
		mcpserver.WithInstructions(serverInstructions),
		mcpserver.WithToolCapabilities(false),
		mcpserver.WithHooks(hooks),
		mcpserver.WithRecovery(),
	)
	// The audit log is a process-lifetime append-only JSONL sidecar under the cache
	// dir (next to the journal run logs). Opening is best-effort - a nil log is a no-op
	// - so a read-only or dirless workspace never blocks tool serving.
	var cacheDir string
	if opts.Magus != nil {
		cacheDir = opts.Magus.CacheDir()
	}
	audit := openAuditLog(cacheDir)
	registerTools(srv, opts, log, agentFn, audit)
	return srv
}

// newServer constructs and registers tools on a new MCPServer. agentFn is called
// with the current request ctx to resolve the agent client identity at tool-call
// time so each handler's wrap closure captures the right per-session value.
func newServer(opts Options, log *slog.Logger, agentFn func(context.Context) string) *mcpserver.MCPServer {
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeInitialize(func(_ context.Context, _ any, req *mcp.InitializeRequest) {
		agent := agentFromRequest(req)
		log.Info("[AGENT] client connected", slog.String("agent", agent))
	})
	return buildServer(opts, log, hooks, agentFn)
}

// HTTPHandler builds the MCP Streamable-HTTP handler for daemon mode: it
// validates opts, wires per-session agent tracking, and returns the bare MCP
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
	return mcpserver.NewStreamableHTTPServer(srv, mcpserver.WithHeartbeatInterval(sseHeartbeat)), nil
}

// ServeStdio runs the magus MCP server over standard I/O, blocking until
// stdin closes or the context is cancelled. Kept for testing and scripted
// smoke-checks; daemon mode uses ServeHTTP instead.
func ServeStdio(ctx context.Context, opts Options) error {
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
