package main

import (
	"context"
	"log/slog"

	"github.com/egladman/magus/libs/secrets"
)

// withSecrets gives ctx the resolver redaction now refuses to run without; main
// installs one on every real path.
func withSecrets(ctx context.Context) context.Context {
	return secrets.WithResolver(ctx, secrets.NewResolver())
}

// resolverHandler adds the resolver to the context a record is handled under.
// slog.Info hands its handler a bare background context, which the pretty and plain
// handlers cannot redact under.
type resolverHandler struct{ slog.Handler }

func (h resolverHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.Handler.Handle(withSecrets(ctx), r)
}

func (h resolverHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return resolverHandler{h.Handler.WithAttrs(attrs)}
}

func (h resolverHandler) WithGroup(name string) slog.Handler {
	return resolverHandler{h.Handler.WithGroup(name)}
}
