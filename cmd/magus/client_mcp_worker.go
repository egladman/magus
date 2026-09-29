package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/interp/mcpclient"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// runClientWorker is the private re-exec entry point for the MCP client tool.
// The server ends a call by signalling the worker's process group with SIGTERM;
// cancelling ctx on it is what stops the magus runs the script started, which
// sit in process groups of their own.
func runClientWorker(ctx context.Context, in io.Reader, out, errOut io.Writer) int {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer stop()
	return mcpclient.Serve(ctx, in, out, errOut, func(ctx context.Context) (context.Context, error) {
		return clientWorkspace(ctx, errOut)
	})
}

// clientWorkspace opens the workspace at the worker's cwd and stamps the caller.
// globalCfg has to be set first: loadMagus reads it, and a zero config would
// drop the workspace's sandbox mode. Output of the magus runs a script starts
// goes to errOut: out carries the one response line.
func clientWorkspace(ctx context.Context, errOut io.Writer) (context.Context, error) {
	root, err := magus.FindRoot(".")
	if err != nil {
		return ctx, err
	}
	cfg, err := config.LoadWithRoot("", root)
	if err != nil {
		return ctx, err
	}
	knownEnv := func(name string) string {
		if !config.KnownEnvVar(name) {
			return ""
		}
		return os.Getenv(name)
	}
	if err := configgen.ApplyEnv(&cfg, knownEnv); err != nil {
		return ctx, err
	}
	if err := config.Validate(cfg); err != nil {
		return ctx, err
	}
	globalCfg = cfg
	m, err := loadMagus(ctx, root)
	if err != nil {
		return ctx, err
	}
	ctx = types.WithWorkspace(ctx, m)
	sandboxed, err := m.ApplySandbox(ctx)
	if err != nil {
		return ctx, err
	}
	ctx = run.WithOutputWriters(sandboxed, errOut, errOut)
	ctx = trail.ContextWithBase(ctx, m.CacheDir())
	ctx = proc.WithLease(ctx, trail.LeaseFromEnv())
	return trail.ContextWithEntryPoint(ctx, types.EntryPointMCP), nil
}
