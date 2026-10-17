package types

import (
	"maps"
	"slices"
)

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

// IndexHumanOnly is what ws declares human_only, in the form the knowledge store records:
// every project path, and each human_only target with every target whose ctx.needs chain
// reaches it the way the guard walks one, stopping at the first human_only target on a
// branch. The guard reads the same index from an opened workspace that it reads from the
// record, so a verdict cannot depend on which one answered.
func IndexHumanOnly(ws WorkspaceReader) HumanOnlyIndex {
	var ix HumanOnlyIndex
	projects := ws.All()
	at := map[TargetRef]int{}
	for _, p := range projects {
		ix.Projects = append(ix.Projects, p.Path)
		for _, name := range slices.Sorted(maps.Keys(p.TargetPolicies)) {
			if pol := p.TargetPolicies[name]; pol.HumanOnly {
				ref := TargetRef{Project: p.Path, Target: name}
				at[ref] = len(ix.Targets)
				ix.Targets = append(ix.Targets, HumanOnlyTarget{TargetRef: ref, Reason: pol.HumanOnlyReason, SkipCache: pol.SkipCache})
			}
		}
	}
	if len(ix.Targets) == 0 {
		return ix
	}
	// Only a target with a chain composes anything, so only those are walked.
	for _, p := range projects {
		for _, name := range slices.Sorted(maps.Keys(p.TargetChains)) {
			composer := TargetRef{Project: p.Path, Target: name}
			_ = WalkChain(p, name, ws.Get, func(v ChainVisit) error {
				if !v.Project.TargetPolicies[v.Target].HumanOnly {
					return nil
				}
				if i, ok := at[TargetRef{Project: v.Project.Path, Target: v.Target}]; ok && v.Depth > 0 {
					ix.Targets[i].ReachedBy = append(ix.Targets[i].ReachedBy, composer)
				}
				return ErrSkipChain
			})
		}
	}
	return ix
}
