//go:build mcp

package mcp

import (
	"context"
	"log/slog"
)

type ctxKey int

const keyLogger ctxKey = iota

// withLogger attaches a request-scoped slog.Logger to ctx. toolLogger retrieves it.
func withLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, keyLogger, log)
}

// toolLogger returns the request-scoped logger when present, falling back to
// slog.Default(). Call this in tool handlers when surfacing sub-step errors
// so they appear within the agent-request's visual bracket in stderr.
func toolLogger(ctx context.Context) *slog.Logger {
	if log, ok := ctx.Value(keyLogger).(*slog.Logger); ok && log != nil {
		return log
	}
	return slog.Default()
}
