package types

// ChainMemoryMB is the largest memory declaration in what a target will run: its own and
// every target it composes with ctx.needs, transitively, plus the target that declared it.
// A peak, not a maximum: one ctx.needs call's members run together and add, separate calls
// run in turn (ChainStep.CallIndex), so the fold is the largest sum over one call.
// lookup resolves a cross-project step; a nil answer contributes nothing rather than a
// guess. It lives here because admission and doctor must agree on one figure.
func ChainMemoryMB(p *Project, target string, lookup func(path string) *Project) (mb int, declaredBy string) {
	c := SizedChainMemory(p, target, lookup, nil, nil)
	return c.MB, c.DeclaredBy
}

// MembersClaimTheirOwn reports whether target's same-project ctx.needs members each take
// their own machine claim when target holds its own: the run admits a skip_cache
// composer's members as cache steps of their own, and the composer hands its claim back
// while it waits on them. Admission and dispatch both read it, so a member is never
// counted twice nor left uncovered.
func MembersClaimTheirOwn(p *Project, target string) bool {
	return p.TargetPolicies[target].SkipCache
}

// MemorySizer turns one target's own memory declaration, 0 when it declares none, into
// the figure it claims and the curve that figure peaks over. It must not return more
// than a positive declaredMB. A zero profile (Runs 0) is a claim held flat at mb.
type MemorySizer func(proj *Project, target string, declaredMB int) (mb int, sizing MemorySizing, profile MemoryProfile)

// ChainMemory is a folded memory claim: the figure, the target whose declaration it
// came from, and how that declaration was sized. Profile is the curve MB peaks over:
// one run's, or one call's members composed; it is zero when the claim folds calls
// that run in turn, or fell back to a peak.
type ChainMemory struct {
	MB         int
	DeclaredBy string
	Sizing     MemorySizing
	Profile    MemoryProfile
}

// SizedChainMemory is ChainMemoryMB with each member passed through size before it folds;
// a nil size folds the declarations as written. One call's members fold by curve when each
// has a profile (composeCall), else by summed peaks, with Sizing.Fallback naming the member
// that had none. ownClaims reports a composer whose same-project members are admitted as
// steps of their own (MembersClaimTheirOwn): when target is one, those members leave its
// fold rather than count twice. Deeper composers and cross-project members still fold.
func SizedChainMemory(p *Project, target string, lookup func(path string) *Project, size MemorySizer, ownClaims func(p *Project, target string) bool) ChainMemory {
	seen := map[string]bool{}

	var walk func(proj *Project, name string) ChainMemory
	walk = func(proj *Project, name string) ChainMemory {
		if proj == nil {
			return ChainMemory{}
		}
		key := proj.Path + "\x00" + name
		if seen[key] {
			return ChainMemory{} // a cycle is rejected elsewhere; here it must simply terminate
		}
		seen[key] = true

		separate := proj == p && name == target && ownClaims != nil && ownClaims(proj, name)
		peak := ownPeak(proj, name, size)
		contributors := 0
		if peak.MB > 0 {
			contributors = 1
		}
		// One fold per call; a call's members run together, and the calls run in turn.
		for _, call := range chainCalls(proj.TargetChains[name]) {
			var members []ChainMemory
			var names []string
			for _, step := range call {
				next, ok := stepProject(proj, step.Project, lookup)
				if !ok || (separate && step.Project == "") {
					continue
				}
				if member := walk(next, step.Target); member.MB > 0 {
					members, names = append(members, member), append(names, step.Target)
				}
			}
			if len(members) > 0 {
				contributors++
				if folded := foldCall(members, names); folded.MB > peak.MB {
					peak = folded
				}
			}
		}
		// Calls in turn, or a body and its call, compose no one curve: the larger peak is held flat.
		if contributors > 1 {
			peak.Profile = MemoryProfile{}
		}
		return peak
	}
	return walk(p, target)
}

// ownPeak is name's own declaration passed through size, or zero when it claims nothing.
func ownPeak(proj *Project, name string, size MemorySizer) ChainMemory {
	declared := proj.TargetPolicies[name].MemoryMB
	peak := ChainMemory{MB: declared, DeclaredBy: name}
	if size != nil {
		peak.MB, peak.Sizing, peak.Profile = size(proj, name, declared)
	}
	if peak.MB <= 0 {
		return ChainMemory{}
	}
	return peak
}

// chainCalls groups steps into one slice per ctx.needs call, in order.
func chainCalls(steps []ChainStep) [][]ChainStep {
	var calls [][]ChainStep
	for i, step := range steps {
		if i == 0 || step.CallIndex != steps[i-1].CallIndex {
			calls = append(calls, nil)
		}
		calls[len(calls)-1] = append(calls[len(calls)-1], step)
	}
	return calls
}

// stepProject resolves a step's project: proj for a same-project step, lookup's answer
// for another, and false when there is no lookup to ask.
func stepProject(proj *Project, path string, lookup func(path string) *Project) (*Project, bool) {
	if path == "" {
		return proj, true
	}
	if lookup == nil {
		return nil, false
	}
	return lookup(path), true
}
