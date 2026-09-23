package mergequeue

// Partition splits changes into groups with pairwise disjoint affected sets, preserving
// queue order inside each group and ordering groups by their first change.
//
// Cost is linear in the summed affected-set sizes: each unit is indexed once to the
// first change that reached it, and later changes union with that owner. No pair of
// changes is ever compared directly, which keeps planning cheap with hundreds of open
// changes.
//
// Soundness: one unproven change puts every change in one group, because independence
// nobody can prove is never assumed.
func Partition(changes []Change) [][]Change {
	if len(changes) == 0 {
		return nil
	}
	for _, c := range changes {
		if !c.Proven() {
			return [][]Change{changes}
		}
	}

	parent := make([]int, len(changes))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		// The lower index stays the root, so a group's root is its first change.
		if rb < ra {
			ra, rb = rb, ra
		}
		parent[rb] = ra
	}

	owner := make(map[string]int)
	for i, c := range changes {
		for _, u := range c.Affected {
			if o, ok := owner[u]; ok {
				union(o, i)
				continue
			}
			owner[u] = i
		}
	}

	index := make(map[int]int)
	var groups [][]Change
	for i, c := range changes {
		r := find(i)
		gi, ok := index[r]
		if !ok {
			gi = len(groups)
			index[r] = gi
			groups = append(groups, nil)
		}
		groups[gi] = append(groups[gi], c)
	}
	return groups
}
