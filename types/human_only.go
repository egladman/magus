package types

// HumanOnlyTarget is one target a workspace declares human_only: the reason it gives,
// whether the target is cached, and the targets that compose it.
type HumanOnlyTarget struct {
	TargetRef
	Reason    string `json:"reason"`
	SkipCache bool   `json:"skip_cache,omitempty"`
	// ReachedBy is every (project, target) whose ctx.needs chain reaches this target at
	// depth > 0 without passing through another human_only target, the root included: a
	// run a person approves covers the chain under it, so a human_only target is never
	// reported as composed through one.
	ReachedBy []TargetRef `json:"reached_by,omitempty"`
}

// HumanOnlyIndex is what a workspace declares human_only, resolved from its evaluated
// projects so a reader can tell which targets a run reaches without evaluating them.
// Projects is every project path in path order, listed whether or not any target is
// human_only, so an index of a workspace that declares none still names its projects and
// an index that names none was resolved from no workspace. Targets is sorted by
// TargetRef.Compare.
type HumanOnlyIndex struct {
	Projects []string          `json:"projects,omitempty"`
	Targets  []HumanOnlyTarget `json:"targets,omitempty"`
}
