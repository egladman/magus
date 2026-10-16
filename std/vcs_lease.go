//go:build !wasm

package std

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/proc/environ"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
)

// VCSLeaseGate decides whether row, the lease a Buzz process acts under, may run the
// version-control backend with args in dir, starting with env ("KEY=value" entries, the
// last of a name winning): nil to run it, or the refusal to raise.
type VCSLeaseGate func(ctx context.Context, row types.Job, backend string, args []string, dir string, env []string) error

// vcsLeaseGate is lease-vcs as internal/guard decides it, nil until the guard registers it.
// It is registered rather than called because the guard imports this package.
var (
	vcsLeaseGateMu sync.RWMutex
	vcsLeaseGate   VCSLeaseGate
)

// RegisterVCSLeaseGate installs the gate vcs.cmd asks before it runs a version-control
// command while a lease is acting. internal/guard calls it from init, so every binary that
// links the guard has it; a later call replaces the gate. Safe to call while vcs.cmd runs:
// that call sees the old gate or the new one. It returns the gate it replaced, for a test
// that swaps in its own to put back.
func RegisterVCSLeaseGate(gate VCSLeaseGate) (previous VCSLeaseGate) {
	vcsLeaseGateMu.Lock()
	defer vcsLeaseGateMu.Unlock()
	previous, vcsLeaseGate = vcsLeaseGate, gate
	return previous
}

func currentVCSLeaseGate() VCSLeaseGate {
	vcsLeaseGateMu.RLock()
	defer vcsLeaseGateMu.RUnlock()
	return vcsLeaseGate
}

// vcsLeaseRefusal is why vcs.cmd may not run backend with args in dir and env under the
// acting lease, nil when it may. With no workspace, no acting lease, or no live row for it
// there is no boundary to hold, which is the guard's answer for a shell line too; a store
// that cannot be read holds no row either. A lease acting with no gate registered is
// refused: running unguarded is the one answer that cannot be taken back.
func vcsLeaseRefusal(ctx context.Context, backend string, args []string, dir string, env []string) error {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil
	}
	cd, ok := ws.(interface{ CacheDir() string })
	if !ok {
		return nil
	}
	// Ctx-only, not job.RunLease: the daemon's own environment names nobody's lease, and a
	// CLI process has frozen its BAGGAGE onto ctx already.
	// No user state dir leaves the checkout with no record and the store unreadable, which
	// liveRow below meets as no live row, as it does any unreadable store.
	stateBase, _ := xdg.StateDir()
	loc := job.Location{CacheDir: cd.CacheDir(), StateBase: stateBase, Root: ws.Root()}
	lease, _ := job.ActingLease(loc, types.JobLeaseFromContext(ctx))
	if lease == "" {
		return nil
	}
	gate := currentVCSLeaseGate()
	if gate == nil {
		return fmt.Errorf("vcs\\cmd: lease %s is acting and this binary registers no lease-vcs gate, so `%s %s` is refused rather than run unguarded",
			lease, backend, strings.Join(args, " "))
	}
	row, ok := liveRow(loc, lease)
	if !ok {
		return nil
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("vcs\\cmd: the working directory cannot be read, so lease-vcs cannot place the command: %w", err)
		}
		dir = wd
	}
	return gate(ctx, row, backend, args, dir, env)
}

// childEnv is the environment a command run through runResult starts with, as far as a
// variable the child reads is concerned: the sandbox's base environment or this
// process's, the run's env\set changes, then os\with_env overrides. spawn.Exec adds only
// magus's own variables past these.
func childEnv(ctx context.Context) []string {
	base := os.Environ()
	if p := sandbox.PolicyFromContext(ctx); p != nil {
		base = p.BaseEnv
	}
	overrides, _ := ctx.Value(withEnvKey{}).([]string)
	return append(slices.Clip(environ.From(ctx).Apply(base)), overrides...)
}

// liveRow is lease's live row in the store at loc, false when the store holds none or
// cannot be read.
func liveRow(loc job.Location, lease string) (types.Job, bool) {
	rows, err := job.NewStore(loc).List()
	if err != nil {
		return types.Job{}, false
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == lease && r.State.Live() })
	if i < 0 {
		return types.Job{}, false
	}
	return rows[i], true
}
