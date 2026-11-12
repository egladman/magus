package types

import (
	"maps"
	"slices"
)

// TargetPolicy is one target's declared policy the guard reads without evaluating the
// workspace: whether it is human_only and why, whether it is cached, the binary it relinks,
// why an integrator runs it, and, for a human_only target, the targets that compose it.
type TargetPolicy struct {
	TargetRef
	HumanOnly bool `json:"human_only,omitempty"`
	// Reason is the human_only reason, "" for a target that is not human_only.
	Reason     string `json:"reason,omitempty"`
	SkipCache  bool   `json:"skip_cache,omitempty"`
	Relinks    string `json:"relinks,omitempty"`
	// Product is where a worker builds the Relinks binary, which no hook runs.
	// ProductTarget is the sibling target that writes it.
	Product       string `json:"product,omitempty"`
	ProductTarget string `json:"product_target,omitempty"`
	Integrates    string `json:"integrates,omitempty"`
	// ReachedBy is every target whose ctx.needs chain reaches this human_only one at depth
	// > 0 through no other human_only target: an approved run covers the chain under it.
	ReachedBy []TargetRef `json:"reached_by,omitempty"`
}

// TargetPolicyIndex is every target policy a workspace declares, resolved from its
// evaluated projects so a reader can tell what a run reaches and what it replaces without
// evaluating them. Projects is every project path in path order, listed whether or not any
// target declares a policy, so an index of a workspace that declares none still names its
// projects and an index that names none was resolved from no workspace. Targets holds each
// target that declares human_only, relinks or integrates, sorted by TargetRef.Compare.
type TargetPolicyIndex struct {
	Projects []string       `json:"projects,omitempty"`
	Targets  []TargetPolicy `json:"targets,omitempty"`
}

// Lookup is ref's declared policy, and false when ref declares none the index holds.
func (ix TargetPolicyIndex) Lookup(ref TargetRef) (TargetPolicy, bool) {
	for _, t := range ix.Targets {
		if t.TargetRef == ref {
			return t, true
		}
	}
	return TargetPolicy{}, false
}

// ProductBuild is the relinking policy whose product ref writes.
// It is false when no target in ref's project names ref as its product target.
func (ix TargetPolicyIndex) ProductBuild(ref TargetRef) (TargetPolicy, bool) {
	for _, t := range ix.Targets {
		if t.Project == ref.Project && t.ProductTarget != "" && t.ProductTarget == ref.Target {
			return t, true
		}
	}
	return TargetPolicy{}, false
}

// Products is every declared product path, workspace-relative, in index order.
func (ix TargetPolicyIndex) Products() []string {
	var out []string
	for _, t := range ix.Targets {
		if t.Product != "" {
			out = append(out, t.Product)
		}
	}
	return out
}

// HumanOnly is the human_only targets of ix, in index order.
func (ix TargetPolicyIndex) HumanOnly() []TargetPolicy {
	var out []TargetPolicy
	for _, t := range ix.Targets {
		if t.HumanOnly {
			out = append(out, t)
		}
	}
	return out
}

// IndexTargetPolicies is what ws declares, in the form the knowledge store records: every
// project path, and each target with a policy the guard reads, a human_only one with every
// target whose ctx.needs chain reaches it the way the guard walks one, stopping at the
// first human_only target on a branch. The guard reads the same index from an opened
// workspace that it reads from the record, so a verdict cannot depend on which answered.
func IndexTargetPolicies(ws WorkspaceReader) TargetPolicyIndex {
	var ix TargetPolicyIndex
	projects := ws.All()
	at := map[TargetRef]int{}
	humanOnly := false
	for _, p := range projects {
		ix.Projects = append(ix.Projects, p.Path)
		for _, name := range slices.Sorted(maps.Keys(p.TargetPolicies)) {
			pol := p.TargetPolicies[name]
			if !pol.HumanOnly && pol.Relinks == "" && pol.Integrates == "" {
				continue
			}
			ref := TargetRef{Project: p.Path, Target: name}
			at[ref] = len(ix.Targets)
			humanOnly = humanOnly || pol.HumanOnly
			ix.Targets = append(ix.Targets, TargetPolicy{
				TargetRef: ref, HumanOnly: pol.HumanOnly, Reason: pol.HumanOnlyReason, SkipCache: pol.SkipCache,
				Relinks: pol.Relinks, Product: pol.Product, ProductTarget: pol.ProductTarget, Integrates: pol.Integrates,
			})
		}
	}
	if !humanOnly {
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
