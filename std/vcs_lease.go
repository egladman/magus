//go:build !wasm

package std

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// VCSLeaseGate decides whether row, the lease a Buzz process acts under, may run the
// version-control backend with args in dir: nil to run it, or the refusal to raise.
type VCSLeaseGate func(ctx context.Context, row types.Job, backend string, args []string, dir string) error

// vcsLeaseGate is lease-vcs as internal/guard decides it, nil until the guard registers it.
// It is registered rather than called because the guard imports this package.
var vcsLeaseGate VCSLeaseGate

// RegisterVCSLeaseGate installs the gate vcs.cmd asks before it runs a version-control
// command while a lease is acting. internal/guard calls it from init, so every binary that
// links the guard has it; a later call replaces the gate. Not safe to call while vcs.cmd
// may run.
func RegisterVCSLeaseGate(gate VCSLeaseGate) { vcsLeaseGate = gate }

// vcsLeaseRefusal is why vcs.cmd may not run backend with args in dir under the acting
// lease, nil when it may. With no workspace, no acting lease, or no live row for it there
// is no boundary to hold, which is the guard's answer for a shell line too; a store that
// cannot be read holds no row either. A lease acting with no gate registered is refused:
// running unguarded is the one answer that cannot be taken back.
func vcsLeaseRefusal(ctx context.Context, backend string, args []string, dir string) error {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil
	}
	cd, ok := ws.(interface{ CacheDir() string })
	if !ok {
		return nil
	}
	lease, _ := job.ActingLease(cd.CacheDir(), proc.LeaseFromContext(ctx))
	if lease == "" {
		return nil
	}
	if vcsLeaseGate == nil {
		return fmt.Errorf("vcs\\cmd: lease %s is acting and this binary registers no lease-vcs gate, so `%s %s` is refused rather than run unguarded",
			lease, backend, strings.Join(args, " "))
	}
	row, ok := liveRow(job.Location{CacheDir: cd.CacheDir(), Root: ws.Root()}, lease)
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
	return vcsLeaseGate(ctx, row, backend, args, dir)
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
