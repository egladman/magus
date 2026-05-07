//go:build mcp

package mcp

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
)

// ServerOptions configures a magus MCP server started via ServeHTTP or ServeStdio.
type ServerOptions struct {
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

	// HTTPAddr is the parsed address for ServeHTTP. Defaults to defaultAddrPort
	// when zero. Callers parse the config string once and pass the result here
	// so internal/mcp never re-parses a raw string.
	HTTPAddr netip.AddrPort

	// HealthRoutes is an optional set of HTTP routes to mount alongside /mcp
	// on the same HTTP server and listener. Keys are URL paths (e.g. "/healthz",
	// "/livez", "/readyz") and values are the handlers to invoke. When nil or
	// empty no extra routes are registered.
	HealthRoutes map[string]http.Handler
}

func (o ServerOptions) validate() error {
	if o.Magus == nil {
		return errors.New("mcp: ServerOptions.Magus is required")
	}
	return nil
}

func (o ServerOptions) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}

func (o ServerOptions) httpAddr() netip.AddrPort {
	if o.HTTPAddr.IsValid() {
		return o.HTTPAddr
	}
	return defaultAddrPort
}
