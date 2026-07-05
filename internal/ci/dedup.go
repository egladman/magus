package ci

import "sort"

// MissBuild is one cache-miss event, tagged with the shard report File it came
// from so the same key seen on two shards counts as two builds (and the same key
// twice within one shard counts once).
type MissBuild struct {
	Project    string
	Target     string
	Hash       string
	DurationMs int64
	File       string
}

// DedupEntry is one (project, target, hash) that was built redundantly across
// shards, with how many extra builds it caused and the wasted time.
type DedupEntry struct {
	Project     string
	Target      string
	Hash        string
	ExtraBuilds int
	ExtraMs     int64
}

// DedupResult is the cross-shard redundant-build analysis.
type DedupResult struct {
	TotalMisses     int
	UniqueKeys      int
	RedundantBuilds int
	RedundantMs     int64
	// Approx is set when some events lack a Hash (older reports), so grouping is
	// by (project, target) only and the count is an approximation.
	Approx bool
	// Top lists redundant keys sorted by wasted time descending.
	Top []DedupEntry
}

// Dedup measures cross-shard redundant builds: when the same (project, target,
// hash) is a cache miss on more than one shard, those extra builds are waste a
// shared remote cache would eliminate. Within a single shard's File a key counts
// once; the longest of the duplicated builds is treated as the necessary one, so
// the wasted time is total minus max.
func Dedup(misses []MissBuild) DedupResult {
	// If ANY event lacks a hash (older reports omit it), the hash can't be trusted to
	// tell a same-input rebuild from a different-input one, so the whole analysis drops
	// it and groups by (project, target) - the approximation Approx advertises. Detected
	// up front to keep grouping uniform: mixing hash and no-hash keys for one target
	// would split it and undercount.
	approx := false
	for _, m := range misses {
		if m.Hash == "" {
			approx = true
			break
		}
	}

	type key struct{ project, target, hash string }
	byKey := make(map[key][]MissBuild)
	for _, m := range misses {
		k := key{m.Project, m.Target, m.Hash}
		if approx {
			k.hash = ""
		}
		byKey[k] = append(byKey[k], m)
	}

	res := DedupResult{TotalMisses: len(misses), UniqueKeys: len(byKey), Approx: approx}
	for k, ms := range byKey {
		// One build per distinct File (a target appears once per shard run).
		seen := make(map[string]int64, len(ms))
		for _, m := range ms {
			if _, ok := seen[m.File]; !ok {
				seen[m.File] = m.DurationMs
			}
		}
		if len(seen) <= 1 {
			continue
		}
		extra := len(seen) - 1
		var maxMs, totalMs int64
		for _, d := range seen {
			totalMs += d
			if d > maxMs {
				maxMs = d
			}
		}
		extraMs := totalMs - maxMs
		res.RedundantBuilds += extra
		res.RedundantMs += extraMs
		res.Top = append(res.Top, DedupEntry{k.project, k.target, k.hash, extra, extraMs})
	}
	// Sort by wasted time descending, then by identity so equal-waste entries get a
	// stable order (byKey iteration is randomized).
	sort.Slice(res.Top, func(i, j int) bool {
		a, b := res.Top[i], res.Top[j]
		if a.ExtraMs != b.ExtraMs {
			return a.ExtraMs > b.ExtraMs
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Hash < b.Hash
	})
	return res
}
