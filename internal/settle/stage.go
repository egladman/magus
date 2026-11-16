package settle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/job"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// stagePaths shells out to git for the index write itself.
//
// One pathspec that matches nothing aborts the WHOLE `git add` before staging anything,
// so a single stale declaration silently loses every other path in the call.
// filterStageable splits them first: on disk is staged; gone from disk but still tracked
// is ALSO staged, since that is how a deletion or rename is recorded. Only a path that is
// neither is dropped, and reported rather than discarded.
//
// Paths are passed after `--` so one beginning with a dash is unambiguously a path.
//
// It returns what it staged and dropped rather than printing either, so `-o json` gets
// the same answer the terminal does.
func stagePaths(ctx context.Context, root string, driver types.VCSDriver, paths []string) (staged, dropped []string, err error) {
	stageable, dropped, err := filterStageable(ctx, root, driver, paths)
	if err != nil {
		return nil, nil, err
	}
	if len(stageable) == 0 {
		return []string{}, dropped, nil
	}
	// MarkResolved batches the pathspecs, which matters here: `vcs resolve` over a whole
	// dirty tree is the largest path list these commands hand to the VCS.
	if err := driver.MarkResolved(ctx, root, stageable); err != nil {
		return nil, nil, fmt.Errorf("stage: %w", err)
	}
	return stageable, dropped, nil
}

// heldForOtherLeases is the subset of paths a whole-tree listing leaves out, each
// with the live lease it belongs to: the ones another lease's write paths cover in this
// checkout. Two holders sharing a checkout share its dirty tree, and staging the other's
// uncommitted sources would land them under this caller's commit.
func heldForOtherLeases(ctx context.Context, place func(root string) (job.Location, error), root string, paths []string) (map[string]string, error) {
	loc, err := place(root)
	if err != nil {
		return nil, fmt.Errorf("place the job store to read which leases share this checkout: %w", err)
	}
	rows, err := job.NewStore(loc).List()
	if err != nil {
		return nil, fmt.Errorf("read the job store for the paths other leases in this checkout write: %w", err)
	}
	lease, _ := job.RunLease(ctx, loc)
	return otherLeasesPaths(rows, root, lease, paths), nil
}

// otherLeasesPaths maps each of paths that a live row in the checkout at root, other than
// lease's own, declares among its write paths to that row's id. Two rows covering one path
// both name it, unless lease is one of them.
func otherLeasesPaths(rows []jobtypes.Job, root, lease string, paths []string) map[string]string {
	here := slices.DeleteFunc(slices.Clone(rows), func(r jobtypes.Job) bool {
		return r.CheckoutRoot == "" || !sameDir(r.CheckoutRoot, root)
	})
	held := map[string]string{}
	for _, p := range paths {
		owner, ok := job.AttributeWrite(here, p)
		switch {
		case ok && owner.Job != lease:
			held[p] = owner.Job
		case !ok && len(owner.Ambiguous) > 0 && !slices.Contains(owner.Ambiguous, lease):
			held[p] = strings.Join(owner.Ambiguous, " and ")
		}
	}
	return held
}

// filterStageable separates paths git add can actually act on from ones that
// would abort the whole `git add` call. See stagePaths for why a missing-but-
// tracked path (a deletion or the old half of a rename) must still be staged.
func filterStageable(ctx context.Context, root string, driver types.VCSDriver, paths []string) (stageable, dropped []string, err error) {
	var maybeGone []string
	for _, p := range paths {
		if _, statErr := os.Stat(filepath.Join(root, p)); statErr == nil {
			stageable = append(stageable, p)
			continue
		}
		maybeGone = append(maybeGone, p)
	}
	if len(maybeGone) == 0 {
		return stageable, nil, nil
	}
	// The abort-the-whole-invocation behavior this guards against is git's. Other
	// backends get the paths passed through rather than probed.
	if driver.Name() != "git" {
		return append(stageable, maybeGone...), nil, nil
	}

	// The tracked listing does not care whether a file is still on disk, so a
	// tracked-but-deleted path is kept and its deletion gets recorded.
	known, err := driver.TrackedFiles(ctx, root, maybeGone)
	if err != nil {
		return nil, nil, fmt.Errorf("stage: %w", err)
	}
	tracked := make(map[string]bool, len(known))
	for _, p := range known {
		tracked[p] = true
	}
	for _, p := range maybeGone {
		if tracked[p] {
			stageable = append(stageable, p)
		} else {
			dropped = append(dropped, p)
		}
	}
	return stageable, dropped, nil
}

// sameDir reports whether a and b name one directory, through symlinks. It is
// cmd/magus's copy: that one is unexported and serves the pool-root check, a different
// caller.
func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
