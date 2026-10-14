package magus

import (
	"context"
	"maps"
	"slices"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// RecordedHumanOnly answers, for a lazy workspace that has not been opened, what it
// declares human_only, from what the knowledge store recorded at its last evaluated sync:
// valid while the domain fast stamp matches, which costs the tree walk and a few stats and
// never a magusfile parse. false means the caller must open the workspace to learn it; it
// never guesses, so a store that is missing, never recorded the targets, or was synced
// from sources that have since moved sends the caller to the workspace. It never opens lw
// and writes nothing.
func RecordedHumanOnly(ctx context.Context, lw *LazyWorkspace, cfg config.Config) (types.HumanOnlyIndex, bool) {
	store, fastDomain := recordedDomainStamp(ctx, lw, cfg)
	return store.HumanOnly(fastDomain)
}

// humanOnlyIndex is what ws declares human_only, in the form the knowledge store records:
// every project path, and each human_only target with every target whose ctx.needs chain
// reaches it the way the guard walks one, stopping at the first human_only target on a
// branch.
func humanOnlyIndex(ws types.WorkspaceReader) types.HumanOnlyIndex {
	var ix types.HumanOnlyIndex
	projects := ws.All()
	at := map[types.TargetRef]int{}
	for _, p := range projects {
		ix.Projects = append(ix.Projects, p.Path)
		for _, name := range slices.Sorted(maps.Keys(p.TargetPolicies)) {
			if pol := p.TargetPolicies[name]; pol.HumanOnly {
				ref := types.TargetRef{Project: p.Path, Target: name}
				at[ref] = len(ix.Targets)
				ix.Targets = append(ix.Targets, types.HumanOnlyTarget{TargetRef: ref, Reason: pol.HumanOnlyReason, SkipCache: pol.SkipCache})
			}
		}
	}
	if len(ix.Targets) == 0 {
		return ix
	}
	// Only a target with a chain composes anything, so only those are walked.
	for _, p := range projects {
		for _, name := range slices.Sorted(maps.Keys(p.TargetChains)) {
			composer := types.TargetRef{Project: p.Path, Target: name}
			_ = types.WalkChain(p, name, ws.Get, func(v types.ChainVisit) error {
				if !v.Project.TargetPolicies[v.Target].HumanOnly {
					return nil
				}
				if i, ok := at[types.TargetRef{Project: v.Project.Path, Target: v.Target}]; ok && v.Depth > 0 {
					ix.Targets[i].ReachedBy = append(ix.Targets[i].ReachedBy, composer)
				}
				return types.ErrSkipChain
			})
		}
	}
	return ix
}

// humanOnlyIndexBehind is humanOnlyIndex for the evaluation behind ws, and an index that
// lists no project, which the store records as nothing, when ws carries none.
func humanOnlyIndexBehind(ctx context.Context, ws types.Inspector) types.HumanOnlyIndex {
	m := magusBehind(ctx, ws)
	if m == nil {
		return types.HumanOnlyIndex{}
	}
	return humanOnlyIndex(m)
}

// recordedDomainStamp is the knowledge store of lw's workspace, opened read-only, and the
// domain fast stamp lw's tree computes against what that store says the last evaluation
// read: the pair every record the store keeps beside its domain stamp is checked with.
func recordedDomainStamp(ctx context.Context, lw *LazyWorkspace, cfg config.Config) (*knowledge.Store, string) {
	cacheDir := resolveCacheDir(lw.root, cfg)
	store := knowledge.NewStore(cacheDir, true, 0, nil, nil)
	reads, known := store.EvaluationReads()
	fast := knowledgeFastStamps(ctx, cfg, lw.root, cacheDir, lw.tree(), []knowledge.ShardClass{knowledge.ClassDomain}, reads, known)
	return store, fast[knowledge.ClassDomain]
}
