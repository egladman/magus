package types

// Shard is one runner's worth of work in a CI shard plan.
type Shard struct {
	ID           string   // zero-padded shard index (e.g. "00", "01")
	ProjectPaths []string // workspace-relative project paths assigned to this shard
}

// ShardPlan is a provider-neutral CI shard plan produced by Magus.Plan.
type ShardPlan struct {
	Shards      []Shard
	Source      string // VCS source label (e.g. "git diff vs origin/main")
	MaxParallel int    // recommended concurrency cap; 0 means unlimited
}
