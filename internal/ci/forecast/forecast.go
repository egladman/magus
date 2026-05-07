// Package forecast picks an adaptive CI shard count using a USL model (N* = sqrt(W/α)) and
// packs projects via LPT bin-packing. History is a pure data structure; Load/Save handle persistence.
package forecast

import (
	"cmp"
	"math"
	"slices"

	"github.com/egladman/magus/types"
)

// Forecaster computes adaptive shard plans from historical runtime data.
// History is read-only at forecast time; Forecasters are safe to copy.
type Forecaster struct {
	History       History
	Target        string
	TagsByProject map[string][]string
}

// Plan partitions projects into at most maxShards shards using USL-optimal N* = sqrt(W/α) and LPT packing.
func (f Forecaster) Plan(projects []*types.Project, maxShards int) [][]*types.Project {
	if len(projects) == 0 {
		return nil
	}

	durations := make([]int64, len(projects))
	var totalMs int64
	for i, p := range projects {
		ms := f.History.PredictDuration(p.Path, f.Target, f.TagsByProject[p.Path]).Milliseconds()
		durations[i] = ms
		totalMs += ms
	}

	limit := maxShards
	if limit <= 0 || limit > len(projects) {
		limit = len(projects)
	}

	c := f.History.effectiveConstants()
	n := optimalShardCount(Millis(totalMs), c.SetupP50Ms, c.AlphaMs, limit)
	return lpt(projects, durations, n)
}

// optimalShardCount returns the makespan-minimising shard count N* = sqrt(W/α), clamped to [1, maxN].
func optimalShardCount(workMs, setupMs, alphaMs Millis, maxN int) int {
	if maxN < 1 {
		return 1
	}
	if workMs <= 0 {
		return 1
	}
	if setupMs > 0 && workMs < 2*setupMs {
		return 1
	}
	if alphaMs <= 0 {
		return maxN
	}

	n := int(math.Round(math.Sqrt(float64(workMs) / float64(alphaMs))))
	if n < 1 {
		return 1
	}
	if n > maxN {
		return maxN
	}
	return n
}

// lpt packs projects into nShards bins (Longest Processing Time first).
func lpt(projects []*types.Project, durations []int64, nShards int) [][]*types.Project {
	if nShards < 1 {
		nShards = 1
	}
	if len(projects) == 0 {
		return nil
	}
	if nShards > len(projects) {
		nShards = len(projects)
	}

	// Sort indices by descending duration (ties broken by path). Sorting indices avoids mutating callers' slice.
	idx := make([]int, len(projects))
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		ia, ib := idx[a], idx[b]
		if durations[ia] != durations[ib] {
			return cmp.Compare(durations[ib], durations[ia])
		}
		return cmp.Compare(projects[ia].Path, projects[ib].Path)
	})

	shards := make([][]*types.Project, nShards)
	totals := make([]int64, nShards)

	for _, i := range idx {
		minShard := 0 // smallest-total bin; ties go to lowest index for stability
		for s := 1; s < nShards; s++ {
			if totals[s] < totals[minShard] {
				minShard = s
			}
		}
		shards[minShard] = append(shards[minShard], projects[i])
		totals[minShard] += durations[i]
	}

	return shards
}
