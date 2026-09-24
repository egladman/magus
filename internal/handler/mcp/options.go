package mcp

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// Options configures a magus MCP server, served by ServeStdio or built via HTTPHandler
// (server mode, assembled by internal/serverhttp).
type Options struct {
	// Magus is the opened workspace handle. Required. Pass the result of
	// magus.Open; the MCP server does not open its own instance so the
	// workspace cache stays shared with the CLI.
	Magus *magus.Magus

	// Logger is used for startup, shutdown, and per-request banner messages.
	// Nil falls back to slog.Default().
	Logger *slog.Logger

	// Version is embedded in the MCP server info reply sent during the
	// initialize handshake.
	Version string

	// Build is the running binary's full identity (version, commit, date), reported on
	// the status wire so the console shows which server it is talking to.
	Build types.BuildInfo

	// Config is the resolved workspace configuration. Used to check the
	// server address for the magus_status tool.
	Config config.Config

	// HTTPAddr is the parsed address for server HTTP serving. Defaults to defaultAddrPort
	// when zero. Callers parse the config string once and pass the result here
	// so internal/mcp never re-parses a raw string.
	HTTPAddr netip.AddrPort

	// HealthRoutes is an optional set of HTTP routes to mount alongside /mcp
	// on the same HTTP server and listener. Keys are URL paths (e.g. "/healthz",
	// "/livez", "/readyz") and values are the handlers to invoke. When nil or
	// empty no extra routes are registered.
	HealthRoutes map[string]http.Handler

	// StatusBase carries the static portions of a status report (telemetry,
	// cache, build-tag flags) for the console's StatusService.
	// Populated by the caller (cmd/magus) because it owns the selfUpdateCompiled
	// build-tag constant and the config-to-status
	// converters. When zero-valued the bridge returns an empty telemetry/cache
	// block but still serves the live pool state.
	StatusBase types.StatusBase

	// DiffSessions is the server's shared diff-session store, the SAME one the console's
	// /api/v1/diff and /api/v1/diff/session routes use. Sharing it is what makes pairing work:
	// the person opens a diff in the console and the agent joins the session they started,
	// rather than each side holding a private opinion of the changeset.
	//
	// Nil disables magus_diff, which is the honest state for a server with no workspace.
	DiffSessions *changeset.Store

	// Jobs is the server's shared job store, the SAME one JobService reads.
	// Sharing it is what makes the Store's mutex mean
	// anything: two Stores over one file each hold their own lock, so the in-process
	// serialization the store documents would hold only while nothing wrote concurrently.
	//
	// Nil builds a private one, which is correct for a single-door server (the stdio MCP
	// process) and wrong for the server, where the server sets it.
	Jobs *job.Store

	// Unavailable, with a nil Magus, is why the workspace is not loaded. Every tool stays
	// listed and answers it as a tool error, so an agent reads the diagnostic rather than
	// finding no server. Read per call: the answer moves from failed to loading.
	Unavailable func() error
}

func (o Options) validate() error {
	if o.Magus == nil && o.Unavailable == nil {
		return errors.New("mcp: Options.Magus or Options.Unavailable is required")
	}
	return nil
}

func (o Options) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}

func (o Options) httpAddr() netip.AddrPort {
	if o.HTTPAddr.IsValid() {
		return o.HTTPAddr
	}
	return defaultAddrPort
}

// SiteOrigin is the scheme://host origin of the hosted Graph Explorer that
// cmd/magus/graph.go names as defaultExploreURL; keep the two in sync. internal/server
// allows it as the bridge's CORS origin.
const SiteOrigin = "https://eli.gladman.cc"
