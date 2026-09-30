// Package mcp implements the MCP (Model Context Protocol) server for magus. It
// serves over stdio for the one host that launched `magus mcp` (ServeStdio), and
// over Streamable HTTP in `magus server` (HTTPHandler) so several clients can share
// one long-lived server.
//
// Every tool call stamps context.WithValue markers via origin.WithContext so
// downstream goroutines (cache, spell) can attribute work to the MCP client
// that asked. A banner log line is emitted before and after each tool call so
// the human watching magus's stderr can immediately see when an agent triggers
// an operation.
package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
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

// baggageHeader is the W3C Baggage header, the HTTP form of the BAGGAGE variable a
// process reads its lease from, so a remote caller states its lease in the same grammar.
const baggageHeader = "Baggage"

// callerKey keys the lease an HTTP request stamped for its caller, "" when it sent none.
type callerKey struct{}

// withCallerLease returns ctx carrying the lease the request's baggage header named.
// Stamped on every request rather than at initialize: the header is the call's own claim,
// and a session outlives any one caller's lease.
func withCallerLease(ctx context.Context, lease string) context.Context {
	return context.WithValue(ctx, callerKey{}, lease)
}

// callerActor is who a tool call is made by, as the transport it arrived on says.
//
// Over stdio the caller is the process the host launched in its own checkout, with its own
// environment, so it is this process: ok is false and the door acts as the CLI does, through
// job.ActingActor. Over HTTP the server is shared and its own lease is nobody's caller: the
// actor is the lease the request stamped, or job.Actor.Unstamped when it stamped none. A
// credential that is neither stdio nor absent means HTTP even if the stamp is missing, so a
// route that skipped the context func fails closed rather than lending the server's lease.
func callerActor(ctx context.Context) (actor job.Actor, ok bool) {
	if lease, stamped := ctx.Value(callerKey{}).(string); stamped {
		if lease == "" {
			return job.Actor{Unstamped: true}, true
		}
		return job.Actor{Lease: lease}, true
	}
	switch trail.CredentialFromContext(ctx).Kind {
	case "", types.KindStdio:
		return job.Actor{}, false
	}
	return job.Actor{Unstamped: true}, true
}

// unknownOrigin is the fallback identity for a tool call whose session was never
// seen at initialize time (e.g. a race, or a client that skipped the handshake).
var unknownOrigin = origin.Client{Name: "unknown"}

// sseHeartbeat is how often the Streamable-HTTP server pings an open GET (SSE)
// stream. mark3labs disables heartbeats by default; enabling them keeps a
// long-lived server-to-client stream from being closed by an idle timeout, a
// no-op on a pure loopback client, but the correct default the moment the
// endpoint is reached through any proxy or gateway (e.g. a non-loopback bind).
const sseHeartbeat = 30 * time.Second

// DefaultAddress is the default host:port for the MCP Streamable HTTP server.
const DefaultAddress = "127.0.0.1:7391"

// defaultAddrPort is the parsed form of DefaultAddress, used by httpAddr().
var defaultAddrPort = netip.MustParseAddrPort(DefaultAddress)

// toolColumn is the width the tool-name column occupies in the instruction
// listing, so every "- description" starts at the same offset whatever the name.
const toolColumn = 24

// toolLine renders one listing row: two spaces, the tool name padded to
// toolColumn, then the dash and its description.
func toolLine(t hint.ToolName, desc string) string {
	return fmt.Sprintf("  %-*s- %s", toolColumn, t, desc)
}

// clientMembers are the magus\ members the instructions name, by their std
// descriptor names; member renders them as Buzz spells them.
// TestServerInstructionsNameRealMembers holds each to a declared member.
var clientMembers = []string{
	"projects", "targets", "query", "explain", "path", "refs", "stats", "describe_file", "where",
	"affected", "impact", "run", "clean", "output", "insight", "doctor", "job", "vcs",
}

// member renders one magus\ member as a script calls it.
func member(name string) string { return `magus\` + std.CamelCase(name) }

func members(names ...string) string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = member(name)
	}
	return strings.Join(out, ", ")
}

// serverInstructions is the system-level hint sent to the client during
// the initialize handshake. Every tool name is rendered from a hint.ToolName
// constant rather than spelled out, so a rename is a compile error here instead
// of a block of prose that quietly names tools the server no longer registers.
var serverInstructions = strings.Join([]string{
	"You are connected to a magus workspace.",
	"magus is a build orchestrator for multi-language monorepos.",
	"",
	"The workspace is the " + hint.ToolClient.String() + " tool: a Buzz program that imports \"magus\" and calls its members (" + members(clientMembers...) + "). " +
		"Call magus\\describeModule(\"magus\") for the signatures. The tools below are the operations that module does not cover.",
	"",
	toolLine(hint.ToolClient, "run Buzz against the magus client and return its value"),
	toolLine(hint.ToolBuzz, "transform JSON with Buzz; no workspace access"),
	toolLine(hint.ToolStatus, "inspect the live concurrency pool"),
	toolLine(hint.ToolConfig, "view the resolved workspace config (read-only)"),
	toolLine(hint.ToolDiff, "join the review session a person has open: state, comment, suggest, resolve"),
	toolLine(hint.ToolConsole, "return a local console link when a person asks to see it"),
	"",
	"Typical flow:",
	"  Discover through " + hint.ToolClient.String() + " with the typed members (" + members("projects", "targets", "query", "describe_file") + "); they return records, not CLI text.",
	"  Run through " + hint.ToolClient.String() + " (" + members("affected", "run") + "); " + member("output") + " fetches a captured log by its ref.",
	"  Health: " + hint.ToolClient.String() + " (" + member("doctor") + "), " + hint.ToolStatus.String() + ", " + hint.ToolConfig.String() + ".",
	"",
	"Config mutation is intentionally not exposed. Use the magus CLI for that.",
}, "\n")

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
// registers all tools: the server name, instructions, capabilities, recovery,
// and tool set live in one place. The caller supplies only the transport-specific
// hooks (agent tracking) and the originFn used at tool-call time.
func buildServer(opts Options, log *slog.Logger, hooks *mcpserver.Hooks, originFn func(context.Context) origin.Client) *mcpserver.MCPServer {
	tasks := newTaskRuns()
	taskHooks := &mcpserver.TaskHooks{}
	tasks.hook(hooks, taskHooks)
	srv := mcpserver.NewMCPServer(
		"magus", opts.Version,
		mcpserver.WithInstructions(serverInstructions),
		mcpserver.WithToolCapabilities(false),
		mcpserver.WithTaskCapabilities(true, true, true),
		mcpserver.WithTaskHooks(taskHooks),
		mcpserver.WithHooks(hooks),
		mcpserver.WithRecovery(),
	)
	// The activity trail is an append-only JSONL sidecar under the cache dir (next to the
	// journal run logs). Writes are stateless (open/append/close per event). Rotate here trims
	// it once at construction; keeping it bounded thereafter belongs to the server's
	// rotate-activities maintenance job, which is the ONLY trigger: a second one driven off
	// this wrapper's own append counter would bound MCP traffic while leaving every other
	// producer (agent hooks especially) unbounded. An empty cacheDir makes every trail call a
	// no-op, so a read-only or dirless workspace never blocks serving.
	var cacheDir string
	if opts.Magus != nil {
		cacheDir = opts.Magus.CacheDir()
	}
	trail.Rotate(cacheDir)
	registerTools(srv, opts, log, originFn, cacheDir, tasks)
	return srv
}

// ServeStdio serves MCP on in and out, one JSON-RPC message per line, until in reaches EOF
// or ctx ends; either is a clean stop and returns nil. out carries protocol frames only, so
// the caller must keep every other write off it.
//
// Every call is admitted as types.CredentialStdio: the caller is the process that launched
// this one, so there is no bearer to verify, and authorize still holds each tool to ToolNeed.
func ServeStdio(ctx context.Context, opts Options, in io.Reader, out io.Writer) error {
	if err := opts.validate(); err != nil {
		return err
	}
	log := opts.logger()

	// One stdio process serves one client, so its name is a single value rather than a
	// per-session map.
	var client atomic.Pointer[origin.Client]
	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeInitialize(func(hCtx context.Context, _ any, req *mcp.InitializeRequest) {
		o := origin.Client{Name: agentFromRequest(req)}
		client.Store(&o)
		log.InfoContext(hCtx, "[AGENT] client connected", slog.String("agent", o.Name))
	})
	originFn := func(context.Context) origin.Client {
		if o := client.Load(); o != nil {
			return *o
		}
		return unknownOrigin
	}

	stdio := mcpserver.NewStdioServer(buildServer(opts, log, hooks, originFn))
	stdio.SetErrorLogger(slog.NewLogLogger(log.Handler(), slog.LevelError))
	stdio.SetContextFunc(func(ctx context.Context) context.Context {
		return trail.ContextWithCredential(ctx, types.CredentialStdio)
	})
	if err := stdio.Listen(ctx, in, out); err != nil && ctx.Err() == nil {
		return fmt.Errorf("mcp: serve stdio: %w", err)
	}
	return nil
}

// HTTPHandler builds the MCP Streamable-HTTP handler for server mode: it
// validates opts, wires per-session origin tracking, and returns the bare MCP
// handler. It mounts no routes and opens no listener: the server package owns
// the HTTP server assembly (guards, health routes, console) so this package
// need not depend on the httpx server core, the dashboard bridge, or the file
// watcher. The returned handler is a path-agnostic http.Handler; the server
// mounts it at /mcp, matching the path StreamableHTTPServer's own Start() would use.
//
// Each request's baggage header names the lease its caller acts under; see callerActor.
func HTTPHandler(opts Options) (http.Handler, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	log := opts.logger()

	// sessionOrigins maps an MCP session id to its origin.Client (clientInfo + User-Agent),
	// populated by the BeforeInitialize hook and cleaned up on session unregister.
	// This avoids the server-wide atomic.Value which would race across concurrent
	// clients.
	var sessionOrigins sync.Map

	hooks := &mcpserver.Hooks{}
	hooks.AddBeforeInitialize(func(hCtx context.Context, _ any, req *mcp.InitializeRequest) {
		// clientInfo comes off the initialize params; the User-Agent was stashed
		// on hCtx by the WithHTTPContextFunc below, which runs per HTTP request
		// before the message (and thus this hook) is dispatched.
		o := origin.Client{Name: agentFromRequest(req), UserAgent: userAgentFromContext(hCtx)}
		if session := mcpserver.ClientSessionFromContext(hCtx); session != nil {
			sessionOrigins.Store(session.SessionID(), o)
		}
		attrs := []any{slog.String("agent", o.Name)}
		if o.UserAgent != "" { // omit an empty field so the line stays clean over headerless clients
			attrs = append(attrs, slog.String("user_agent", o.UserAgent))
		}
		log.InfoContext(hCtx, "[AGENT] client connected", attrs...)
	})
	hooks.AddOnUnregisterSession(func(_ context.Context, session mcpserver.ClientSession) {
		sessionOrigins.Delete(session.SessionID())
	})

	originFn := func(tCtx context.Context) origin.Client {
		if session := mcpserver.ClientSessionFromContext(tCtx); session != nil {
			// Comma-ok on the assertion too: fall back to unknownOrigin rather than
			// panic if a non-Client value is ever stored under a session id.
			if v, ok := sessionOrigins.Load(session.SessionID()); ok {
				if o, ok := v.(origin.Client); ok {
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
		// context so the initialize hook can capture it. stdio has no equivalent
		// (it carries no request headers), so this signal is HTTP-only by nature.
		mcpserver.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return withCallerLease(withUserAgent(ctx, r.Header.Get("User-Agent")), trail.LeaseFromBaggage(r.Header.Get(baggageHeader)))
		}),
	), nil
}
