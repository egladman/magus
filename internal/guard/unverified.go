package guard

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/egladman/magus/internal/unverified"
	"github.com/egladman/magus/internal/workspace"
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
// holding dir, so `git -C other push` reads the store of other. A checkout no workspace
// holds is read as one that declares no target, so every file it changed is named as one
// no target reads.
func guardUnverified(deps Dependencies) unverified.Reader {
	var mu sync.Mutex
	answered := map[[2]string]types.UnverifiedReport{}
	return func(ctx context.Context, dir, revision string) (types.UnverifiedReport, error) {
		if deps.Inspect == nil {
			return types.UnverifiedReport{}, errors.New("this caller cannot open the workspace, so no run can be read")
		}
		checkout, ok := unverifiedCheckout(deps, dir)
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
		answered[key] = report
		return report, nil
	}
}

// unverifiedCheckout is the checkout a read of dir weighs: the workspace holding it and its
// store, or, when no workspace holds dir, dir with no projects and no store. False when a
// workspace holds dir but its store cannot be located.
func unverifiedCheckout(deps Dependencies, dir string) (unverified.Checkout, bool) {
	if _, err := workspace.FindRoot(dir); err != nil {
		return unverified.Checkout{
			Dir: dir, Workspace: dir, VCS: deps.VCS,
			Projects: func(context.Context) (unverified.Projects, error) { return unverified.NoProjects, nil },
		}, true
	}
	where := hookLocationAt(deps, dir)
	if where.workspace == "" || where.cacheDir == "" {
		return unverified.Checkout{}, false
	}
	return unverified.Checkout{
		Dir: dir, Workspace: where.workspace, CacheDir: where.cacheDir, VCS: deps.VCS,
		Projects: func(c context.Context) (unverified.Projects, error) { return deps.inspect(c, where.workspace) },
	}, true
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
