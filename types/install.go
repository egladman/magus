package types

import (
	"context"

	"github.com/egladman/magus/spells"
)

// InstallRunner runs one spell's install (target names the specific op: pnpm-install,
// go-mod-download, ...) for the project at dir under that install's own cache entry,
// calling run only when the entry does not replay. The run scheduler installs one per
// invocation, so an install keys the same way however it was reached: scheduled
// directly, composed through a magusfile's spell handle, or dispatched across projects.
type InstallRunner func(ctx context.Context, dir, spell, target string, choice spells.InstallChoice, run func(context.Context) error) error

type installRunnerKey struct{}

// WithInstallRunner stores r in ctx.
func WithInstallRunner(ctx context.Context, r InstallRunner) context.Context {
	return context.WithValue(ctx, installRunnerKey{}, r)
}

// InstallRunnerFromContext returns the runner WithInstallRunner stored, or nil outside
// a run, where an install simply runs.
func InstallRunnerFromContext(ctx context.Context) InstallRunner {
	r, _ := ctx.Value(installRunnerKey{}).(InstallRunner)
	return r
}
