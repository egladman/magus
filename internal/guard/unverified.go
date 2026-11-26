package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/unverified"
	"github.com/egladman/magus/internal/workspace"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// phaseUnverified is the trail phase of one magus\workspace(dir).unverified read.
const phaseUnverified = "unverified"

// unverifiedShare is the most of the hook's budget one read spends, half as jobRowsBudget
// is, so the rules judged after it keep the rest.
const unverifiedShare = hookBudget / 2

// unverifiedBudget is share, or what remains of ctx's deadline less judgeReserve when that
// is less. A dry run has no share: it gets the whole of what remains.
func unverifiedBudget(ctx context.Context, share time.Duration) time.Duration {
	left, bounded := untilReserve(ctx)
	switch {
	case !bounded:
		return share
	case isDryRun(ctx):
		return left
	}
	return min(share, left)
}

// guardUnverified is the reader a workspace command rule's magus\workspace(dir).unverified
// calls. A judgment asks it once per checkout and revision: the approved and the working
// tree's rule are asked apart, and both would otherwise read. It locates the checkout
// holding dir, so `git -C other push` reads the store of other. A checkout no magus
// workspace holds is read as one that declares no target, and the report says so.
func guardUnverified(deps Dependencies) unverified.Reader {
	var mu sync.Mutex
	answered := map[[2]string]types.UnverifiedReport{}
	return func(ctx context.Context, dir, revision string) (types.UnverifiedReport, error) {
		if deps.Inspect == nil {
			return types.UnverifiedReport{}, errors.New("this caller cannot open the workspace, so no run can be read")
		}
		checkout, noWorkspace, ok := unverifiedCheckout(deps, dir)
		checkout.Recorded = jobRowRuns(ctx)
		if !ok {
			return types.UnverifiedReport{Revision: revision, Unknown: fmt.Sprintf("the output store of the workspace holding `%s` cannot be located", dir)}, nil
		}
		key := [2]string{checkout.Workspace, revision}
		mu.Lock()
		defer mu.Unlock()
		if got, ok := answered[key]; ok {
			return got, nil
		}
		report := readWithin(ctx, checkout, revision)
		report.NoWorkspace = noWorkspace && report.Unknown == ""
		answered[key] = report
		return report, nil
	}
}

// jobRowRuns are the runs the pinned job rows recorded. A worker ran them in a checkout that
// may be gone; unreadable rows add none.
func jobRowRuns(ctx context.Context) []cache.OutputDescriptor {
	snap, ok := job.SnapshotFromContext(ctx)
	if !ok || snap.Err != nil {
		return nil
	}
	var captured []jobtypes.CapturedRun
	for _, row := range snap.Rows {
		if row.Attempt != nil {
			captured = append(captured, *row.Attempt)
		}
		for _, g := range row.PostconditionAttempts {
			captured = append(captured, g.Attempt)
		}
	}
	return unverified.RecordedRuns(captured)
}

// unverifiedCheckout is the checkout a read of dir weighs: the workspace holding it and its
// store, or, when no magus workspace holds dir, dir with no projects and no store, which
// noWorkspace reports. False when a workspace holds dir but its store cannot be located.
func unverifiedCheckout(deps Dependencies, dir string) (checkout unverified.Checkout, noWorkspace, ok bool) {
	// FindRoot falls back to the version-control checkout when nothing declares a
	// workspace, which an open would read as one with projects of its own.
	if root, err := workspace.FindRoot(dir); err != nil || !declaresWorkspace(root) {
		return unverified.Checkout{
			Dir: dir, Workspace: dir, VCS: deps.VCS,
			Projects: func(context.Context) (unverified.Projects, error) { return unverified.NoProjects, nil },
		}, true, true
	}
	where := hookLocationAt(deps, dir)
	if where.workspace == "" || where.cacheDir == "" {
		return unverified.Checkout{}, false, false
	}
	return unverified.Checkout{
		Dir: dir, Workspace: where.workspace, CacheDir: where.cacheDir, VCS: deps.VCS,
		Projects: func(c context.Context) (unverified.Projects, error) { return deps.inspect(c, where.workspace) },
	}, false, true
}

// declaresWorkspace reports whether root holds a magus.yaml or a magusfile. It copies the
// markers internal/workspace's FindRoot reads, which keeps them unexported.
func declaresWorkspace(root string) bool {
	return slices.ContainsFunc([]string{"magus.yaml", "magusfile.buzz", "magusfiles"}, func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	})
}

// readWithin reads revision in checkout within unverifiedBudget, recorded as the judgment's
// unverified phase. A read the workspace open cut short says so, and names the open as an
// earlier rule's when that rule's open had already run out: the workspace is opened once per
// judgment, so this read could not open it again, and the lapse is not this read's own.
func readWithin(ctx context.Context, checkout unverified.Checkout, revision string) types.UnverifiedReport {
	log := judgmentPhases(ctx)
	earlier, lapsedBefore := log.lapsedPhase(phaseInspect)
	budget := unverifiedBudget(ctx, unverifiedShare)
	wait, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	log.begin(phaseUnverified)
	start := time.Now()
	report := unverified.Open(wait, checkout, revision)
	own := false
	if report.Unknown != "" {
		opened, lapsedOpen := log.lapsedPhase(phaseInspect)
		switch {
		case lapsedBefore:
			report = types.UnverifiedReport{Revision: revision, Unknown: lapseWording{
				"an earlier rule of this judgment opened the workspace, and that open", "finish",
			}.clause(time.Duration(earlier.BudgetMs)*time.Millisecond) + ", so no target's inputs could be read for this push"}
		case lapsedOpen:
			own = true
			report = types.UnverifiedReport{Revision: revision, Unknown: lapseWording{
				"opening the workspace to read which targets declare the changed files", "finish",
			}.clause(time.Duration(opened.BudgetMs) * time.Millisecond)}
		case wait.Err() != nil:
			own = true
			report = types.UnverifiedReport{Revision: revision, Unknown: lapseWording{
				"weighing the changed files against the recorded runs", "finish",
			}.clause(budget)}
		}
	}
	log.record(timedPhase(phaseUnverified, time.Since(start), budget, own))
	return report
}
