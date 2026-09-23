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
	// Sufficient is the smallest shard count that finishes as fast as this plan, so a
	// reader can see what the extra runners bought. 0 when nothing was predictable.
	Sufficient int
	// OverBudget names the shards predicted to exceed one runner's memory, by ID. A
	// runner that runs out of memory disappears and reports "cancelled" with no
	// diagnostics, so this is the only warning there is.
	OverBudget []string
	// Affected is every project the change set reaches, sorted: the closure the shards
	// were cut from, before any sharding or filtering.
	Affected []string
	// UnboundedBy, when set, says why Affected is not a proof: the change set edits a
	// file that can move the graph's edges or every project's build (a declaration, a
	// dependency manifest, a toolchain pin), touches a file no project claims, or the
	// VCS could not diff and the plan fell back to every project. A consumer that relies
	// on two closures being disjoint (a merge queue) must treat it as overlapping
	// everything.
	UnboundedBy string
}
