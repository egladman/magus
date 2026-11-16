package guard

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/egladman/magus/internal/unverified"
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
// holding dir, so `git -C other push` reads the store of other.
func guardUnverified(deps Dependencies) unverified.Reader {
	var mu sync.Mutex
	answered := map[[2]string]types.UnverifiedReport{}
	return func(ctx context.Context, dir, revision string) (types.UnverifiedReport, error) {
		if deps.Inspect == nil {
			return types.UnverifiedReport{}, errors.New("this caller cannot open the workspace, so no run can be read")
		}
		where := hookLocationAt(deps, dir)
		if where.workspace == "" || where.cacheDir == "" {
			return types.UnverifiedReport{Revision: revision, Unknown: fmt.Sprintf("no workspace with an output store holds `%s`", dir)}, nil
		}
		key := [2]string{where.workspace, revision}
		mu.Lock()
		defer mu.Unlock()
		if got, ok := answered[key]; ok {
			return got, nil
		}
		budget := unverifiedBudget(ctx, unverifiedShare)
		wait, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		log := judgmentPhases(ctx)
		log.begin(phaseUnverified)
		start := time.Now()
		report := unverified.Open(wait, unverified.Checkout{
			Dir: dir, Workspace: where.workspace, CacheDir: where.cacheDir, VCS: deps.VCS,
			Projects: func(c context.Context) (types.WorkspaceRepository, error) { return deps.inspect(c, where.workspace) },
		}, revision)
		// The workspace open bounds itself to what remains of ctx, so it can run out before
		// this read's own share does.
		openLapse, openLapsed := log.lapsedPhase(phaseInspect)
		lapsed := report.Unknown != "" && (wait.Err() != nil || openLapsed)
		if lapsed {
			cut := lapseWording{"weighing the changed files against recorded runs", "finish"}.clause(budget)
			if openLapsed {
				cut = lapseWording{"opening the workspace to weigh the changed files", "finish"}.clause(time.Duration(openLapse.BudgetMs) * time.Millisecond)
			}
			report = types.UnverifiedReport{Revision: revision, Unknown: cut}
		}
		log.record(timedPhase(phaseUnverified, time.Since(start), budget, lapsed))
		answered[key] = report
		return report, nil
	}
}
