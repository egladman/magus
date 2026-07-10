package handler

import (
	"log/slog"
	"net/http"
)

// Base is the embedded core of every HTTP route handler: the http.Handler that
// actually serves the route, plus a request-scoped logger the handler can use.
// Embedding it makes each handler a concrete named type (not a bare interface)
// that callers can hold, reference, and log through. Construct it with New so the
// logger is never nil.
type Base struct {
	http.Handler
	Log *slog.Logger
}

// New builds a Base wrapping serve, defaulting Log to slog.Default() when nil.
func New(serve http.HandlerFunc, log *slog.Logger) Base {
	if log == nil {
		log = slog.Default()
	}
	return Base{Handler: serve, Log: log}
}
