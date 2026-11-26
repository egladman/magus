package evidence

import (
	"context"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/workspace"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// runsAtCurrentKey answers whether a recorded run is current in tree: the cache key its
// check would mint there now is the key the run was stored under. Any lookup that fails
// answers no, so the clock judges the run as it would without this.
func runsAtCurrentKey(graphIn func(tree string) *Graph, env Env) job.CurrentRun {
	return func(ctx context.Context, tree string, run jobtypes.CapturedRun) bool {
		if run.Ref == "" || run.Failed {
			return false
		}
		dir, err := magus.ResolveCacheDir(tree, workspace.WithLoadedConfig(env.Config))
		if err != nil {
			return false
		}
		desc, err := cache.NewOutputStore(dir).DescriptorByRef(run.Ref)
		if err != nil || desc.Key == "" {
			return false
		}
		graph := graphIn(tree)
		if graph == nil || graph.workspace == nil {
			return false
		}
		m, err := graph.workspace(ctx)
		if err != nil {
			return false
		}
		target, charms, _ := strings.Cut(desc.Target, ":")
		var names []string
		if charms != "" {
			names = strings.Split(charms, ",")
		}
		key, _, err := m.ComputeTargetKey(ctx, desc.Project, target, names)
		return err == nil && key == desc.Key
	}
}

// Observer is the checkpoint observer plus the symbol delta of the job's diff and, for a
// job with a check gate on ci, the newest green ci gate on this checkout's branch and the
// tier of the change since it. One graph load serves the symbol postconditions and the delta, and it
// is the graph of the checkout the job's work lives in: a worker's unintegrated branch is
// invisible to the waiter's.
func Observer(root string, graphIn func(tree string) *Graph, env Env) job.Observer {
	return func(ctx context.Context, row jobtypes.Job) (job.Observed, error) {
		tree := job.WorkTree(root, row)
		graph := graphIn(tree)
		seen, err := job.CheckpointObserver(root, GraphSymbolReader(graph.LoadScoped(row.Paths.Write)))(ctx, row)
		if err != nil {
			return seen, err
		}
		seen.CurrentRun = runsAtCurrentKey(graphIn, env)
		seen.Regenerated = job.RegeneratedDelivered(tree, row, seen.Changed, graph.jobWorkspace(ctx, tree))
		if delta, ok := observeSymbolDelta(ctx, tree, row, seen, graph); ok {
			seen.AddedExports, seen.LeftUnreferenced, seen.SymbolDeltaKnown = delta.Added, delta.Left, true
		}
		if root == "" || !gatesOnCI(row) {
			return seen, nil
		}
		seen.GreenGate = latestGreenGate(ctx, root, env)
		return seen, nil
	}
}

// observeSymbolDelta reads the delta of the diff seen was graded on, in tree, the checkout
// that diff was read in, against graph, which is that tree's. A graph that will not load
// leaves the delta unknown, as does a stale index when graph must not build.
func observeSymbolDelta(ctx context.Context, tree string, row jobtypes.Job, seen job.Observed, graph *Graph) (job.SymbolDelta, bool) {
	if row.ReadOnly || tree == "" || !seen.ChangedKnown || seen.ChangedRevision == "" {
		return job.SymbolDelta{}, false
	}
	if len(seen.Changed) == 0 {
		return job.SymbolDelta{}, true
	}
	g := graph.LoadScoped(row.Paths.Write)(ctx)
	res, err := vcs.Resolve(ctx, tree, "", types.VCSOptions{})
	if err != nil || res.Source == types.VCSSourceDisabled {
		return job.SymbolDelta{}, false
	}
	return ReadSymbolDelta(ctx, tree, g, res.VCS, seen.ChangedRevision, seen.Changed)
}
