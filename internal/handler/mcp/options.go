package mcp

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// Options configures a magus MCP server built via HTTPHandler
// (daemon mode, assembled by internal/daemon) or served via ServeStdio.
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

	// Config is the resolved workspace configuration. Used to check the
	// daemon address for the magus_status tool.
	Config config.Config

	// HTTPAddr is the parsed address for daemon HTTP serving. Defaults to defaultAddrPort
	// when zero. Callers parse the config string once and pass the result here
	// so internal/mcp never re-parses a raw string.
	HTTPAddr netip.AddrPort

	// HealthRoutes is an optional set of HTTP routes to mount alongside /mcp
	// on the same HTTP server and listener. Keys are URL paths (e.g. "/healthz",
	// "/livez", "/readyz") and values are the handlers to invoke. When nil or
	// empty no extra routes are registered.
	HealthRoutes map[string]http.Handler

	// StatusBase carries the static portions of a status report (telemetry,
	// cache, build-tag flags) for the web bridge's /api/v1/status handler.
	// Populated by the caller (cmd/magus) because it owns the selfUpdateCompiled
	// build-tag constant and the config-to-status
	// converters. When zero-valued the bridge returns an empty telemetry/cache
	// block but still serves the live pool state.
	StatusBase types.StatusBase
}

func (o Options) validate() error {
	if o.Magus == nil {
		return errors.New("mcp: Options.Magus is required")
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

// defaultExploreURL mirrors cmd/magus/graph_open.go:defaultExploreURL.
// Duplicated here to avoid importing cmd/magus; keep in sync.
const defaultExploreURL = "https://eli.gladman.cc/magus/graph/"

// SiteOrigin returns the scheme://host origin of the hosted Graph Explorer.
// Used by internal/daemon to set the bridge's CORS allowed origin.
func (o Options) SiteOrigin() (string, error) {
	u, err := url.Parse(defaultExploreURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", nil
	}
	return u.Scheme + "://" + u.Host, nil
}
