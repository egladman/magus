package magus

import (
	"context"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// recordedTargetPolicies answers, for a lazy workspace that has not been opened, what it
// declares as target policy, from what the knowledge store recorded at its last evaluated sync:
// valid while the domain fast stamp matches, which costs the tree walk and a few stats and
// never a magusfile parse. false means the caller must open the workspace to learn it; it
// never guesses, so a store that is missing, never recorded the targets, or was synced
// from sources that have since moved sends the caller to the workspace. It never opens lw
// and writes nothing.
func recordedTargetPolicies(ctx context.Context, lw *LazyWorkspace, cfg config.Config) (types.TargetPolicyIndex, bool) {
	store, fastDomain := recordedDomainStamp(ctx, lw, cfg)
	return store.TargetPolicies(fastDomain)
}

// targetPolicyIndexBehind is types.IndexTargetPolicies for the evaluation behind ws, and an index
// that lists no project, which the store records as nothing, when ws carries none.
func targetPolicyIndexBehind(ctx context.Context, ws types.Inspector) types.TargetPolicyIndex {
	m := magusBehind(ctx, ws)
	if m == nil {
		return types.TargetPolicyIndex{}
	}
	return types.IndexTargetPolicies(m)
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
