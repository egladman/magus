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

// Fail answers a route whose source failed: 500 with a body naming only what failed. err
// can carry the server's absolute paths, so it goes to the log and never to the client.
func (b Base) Fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	b.Log.ErrorContext(r.Context(), what+" failed", slog.String("path", r.URL.Path), slog.String("error", err.Error()))
	http.Error(w, what+" failed", http.StatusInternalServerError)
}

// New builds a Base wrapping serve, defaulting Log to slog.Default() when nil.
func New(serve http.HandlerFunc, log *slog.Logger) Base {
	if log == nil {
		log = slog.Default()
	}
	return Base{Handler: serve, Log: log}
}
