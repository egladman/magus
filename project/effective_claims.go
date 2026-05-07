package project

import "github.com/egladman/magus/types"

// EffectiveClaims returns the claim set for p.ResolvedSpells[idx] after
// weight-ordered precedence (higher ClaimWeight wins; equal weight is last-wins)
// and binding AddedClaims/RemovedClaims are applied.
func EffectiveClaims(p *types.Project, idx int) []string {
	spells := p.ResolvedSpells
	bindings := p.Bindings

	declared := spells[idx].Claims()
	if len(declared) == 0 && (idx >= len(bindings) || len(bindings[idx].AddedClaims) == 0) {
		return nil
	}

	effective := make([]string, 0, len(declared))
	effective = append(effective, declared...)
	if idx < len(bindings) {
		effective = append(effective, bindings[idx].AddedClaims...)
	}

	selfWeight := 0
	if idx < len(bindings) {
		selfWeight = bindings[idx].ClaimWeight
	}

	for j := 0; j < len(spells); j++ {
		if j == idx {
			continue
		}
		if len(spells[j].Claims()) == 0 {
			continue
		}
		otherWeight := 0
		if j < len(bindings) {
			otherWeight = bindings[j].ClaimWeight
		}
		outranks := otherWeight > selfWeight || (otherWeight == selfWeight && j > idx)
		if !outranks {
			continue
		}
		effective = removeOverlapping(effective, spells[j].Claims())
	}

	if idx < len(bindings) {
		effective = removeOverlapping(effective, bindings[idx].RemovedClaims)
	}

	return effective
}

func removeOverlapping(a, remove []string) []string {
	if len(remove) == 0 {
		return a
	}
	removeSet := make(map[string]struct{}, len(remove))
	for _, s := range remove {
		removeSet[s] = struct{}{}
	}
	out := a[:0:0]
	for _, s := range a {
		if _, found := removeSet[s]; !found {
			out = append(out, s)
		}
	}
	return out
}
