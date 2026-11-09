package magus

import (
	"context"
	"errors"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/readlog"
	"github.com/egladman/magus/types"
)

// recordedTargetPolicies answers, for a lazy workspace that has not been opened, what it
// declares as target policy, from what the knowledge store recorded at its last evaluated sync:
// valid while the domain fast stamp matches, which costs the tree walk and a few stats and
// never a magusfile parse. false means the caller must open the workspace to learn it; it
// never guesses, so a store that is missing, never recorded the targets, or was synced
// from sources that have since moved sends the caller to the workspace. It never opens lw
// and writes nothing. With no current sync it asks openedTargetPolicies.
func recordedTargetPolicies(ctx context.Context, lw *LazyWorkspace, cfg config.Config) (types.TargetPolicyIndex, bool) {
	store, fastDomain := recordedDomainStamp(ctx, lw, cfg)
	if ix, ok := store.TargetPolicies(fastDomain); ok {
		return ix, true
	}
	return openedTargetPolicies(ctx, lw, cfg)
}

// openedTargetPolicies answers from the record recordTargetPolicies left,
// while the domain fast stamp over its reads still matches the tree.
func openedTargetPolicies(ctx context.Context, lw *LazyWorkspace, cfg config.Config) (types.TargetPolicyIndex, bool) {
	cacheDir := resolveCacheDir(lw.root, cfg)
	rec, ok, err := knowledge.RecordedPolicies(cacheDir)
	if err != nil || !ok {
		return types.TargetPolicyIndex{}, false
	}
	if domainStamp(ctx, lw, cfg, cacheDir, rec.Reads) != rec.FastDomain {
		return types.TargetPolicyIndex{}, false
	}
	return rec.Policies, true
}

// recordTargetPolicies records what lw declares as target policy, opening it
// if it is not open yet. A later judgment then answers without the open.
func recordTargetPolicies(ctx context.Context, lw *LazyWorkspace, cfg config.Config) error {
	if cacheImmutable(cfg) {
		return errors.New("the cache is read-only, so no policy record is written")
	}
	// A nil log reads as no reads, which would stamp the record as if the
	// magusfile read nothing outside the tree.
	m := magusBehind(ctx, lw)
	if m == nil || m.loadReads == nil {
		return errors.New("the workspace carries no evaluation reads to record policy against")
	}
	reads := m.loadReads.Reads()
	cacheDir := resolveCacheDir(lw.root, cfg)
	stamp := domainStamp(ctx, lw, cfg, cacheDir, reads)
	if stamp == "" {
		return errors.New("the tree has no domain stamp to record policy against")
	}
	return knowledge.RecordTargetPolicies(ctx, cacheDir, knowledge.PolicyRecord{
		FastDomain: stamp, Reads: reads, Policies: targetPolicyIndexBehind(ctx, lw),
	})
}

// domainStamp is the domain fast stamp of lw's tree over reads.
func domainStamp(ctx context.Context, lw *LazyWorkspace, cfg config.Config, cacheDir string, reads readlog.Reads) string {
	want := []knowledge.ShardClass{knowledge.ClassDomain}
	return knowledgeFastStamps(ctx, cfg, lw.root, cacheDir, lw.tree(), want, reads, true)[knowledge.ClassDomain]
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
