package mcpclient

import (
	"context"
	"io"
	"os"

	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// Held is the workspace a client worker opened: what its scripts act on, the sandbox
// they run in and the cache directory their trail lands in.
type Held struct {
	Repository   types.WorkspaceRepository
	ApplySandbox func(ctx context.Context) (context.Context, error)
	CacheDir     string
}

// Opener opens root with cfg, which the opener also publishes process-wide: opening reads
// it, and a zero config would drop the workspace's sandbox mode.
type Opener func(ctx context.Context, cfg config.Config, root string) (Held, error)

// OpenWorkspace opens the workspace at the worker's cwd and stamps the caller. Output of
// the magus runs a script starts goes to errOut: out carries the one response line.
func OpenWorkspace(ctx context.Context, errOut io.Writer, open Opener) (context.Context, error) {
	root, err := workspace.FindRoot(".")
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
	m, err := open(ctx, cfg, root)
	if err != nil {
		return ctx, err
	}
	ctx = types.WithWorkspace(ctx, m.Repository)
	sandboxed, err := m.ApplySandbox(ctx)
	if err != nil {
		return ctx, err
	}
	ctx = spawn.WithOutputWriters(sandboxed, errOut, errOut)
	ctx = recorder.ContextWithBase(ctx, m.CacheDir)
	ctx, err = job.WithLease(ctx, recorder.LeaseFromEnv())
	if err != nil {
		return ctx, err
	}
	return recorder.ContextWithEntryPoint(ctx, job.EntryPointMCP), nil
}
