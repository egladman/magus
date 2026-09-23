package queue

// Entry is a change admitted to the queue, with what the graph says it reaches.
type Entry struct {
	Change  Change
	Order   int // queue position
	Paths   []string
	Closure Closure
}

// Group is a run of entries whose closures overlap, in queue order. Entries in one
// group stack; separate groups are independent.
type Group struct {
	Entries []Entry
}

// Partition splits entries into groups with pairwise disjoint closures, preserving
// queue order inside each group and ordering groups by their first entry.
//
// Cost is linear in the summed closure sizes: each project is indexed once to the first
// entry that reached it, and later entries union with that owner. No pair of entries is
// ever compared directly, which is what keeps planning cheap with hundreds of open
// changes.
//
// Soundness: one unproven closure puts every entry in one group, because independence
// the graph cannot prove is never assumed.
func Partition(entries []Entry) []Group {
	if len(entries) == 0 {
		return nil
	}
	for _, e := range entries {
		if !e.Closure.Proven {
			return []Group{{Entries: entries}}
		}
	}

	parent := make([]int, len(entries))
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
		// The lower index stays the root, so a group's root is its first entry.
		if rb < ra {
			ra, rb = rb, ra
		}
		parent[rb] = ra
	}

	owner := make(map[string]int)
	for i, e := range entries {
		for _, p := range e.Closure.Projects {
			if o, ok := owner[p]; ok {
				union(o, i)
				continue
			}
			owner[p] = i
		}
	}

	index := make(map[int]int)
	var groups []Group
	for i, e := range entries {
		r := find(i)
		gi, ok := index[r]
		if !ok {
			gi = len(groups)
			index[r] = gi
			groups = append(groups, Group{})
		}
		groups[gi].Entries = append(groups[gi].Entries, e)
	}
	return groups
}
