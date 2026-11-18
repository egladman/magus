package main

import (
	"context"
	"io"
	"os/signal"
	"syscall"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interp/mcpclient"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/secrets"
)

// runClientWorker is the private re-exec entry point for the MCP client tool.
// The server ends a call by signalling the worker's process group with SIGTERM;
// cancelling ctx on it is what stops the magus runs the script started, which
// sit in process groups of their own.
func runClientWorker(ctx context.Context, in io.Reader, out, errOut io.Writer) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer stop()
	ctx = secrets.WithResolver(ctx, recorder.NewSecretResolver())
	return mcpclient.Serve(ctx, in, out, errOut, func(ctx context.Context) (context.Context, error) {
		return mcpclient.OpenWorkspace(ctx, errOut, openClientWorkspace)
	})
}

// openClientWorkspace publishes cfg, which loadMagus reads, and loads root.
func openClientWorkspace(ctx context.Context, cfg config.Config, root string) (mcpclient.Held, error) {
	globalCfg = cfg
	m, err := loadMagus(ctx, root)
	if err != nil {
		return mcpclient.Held{}, err
	}
	return mcpclient.Held{Repository: viewInternals(m), ApplySandbox: m.ApplySandbox, CacheDir: m.CacheDir()}, nil
}
