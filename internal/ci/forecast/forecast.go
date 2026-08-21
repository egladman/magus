// Package forecast picks an adaptive CI shard count using a USL model (N* = sqrt(W/α)) and
// packs projects via LPT bin-packing. History is a pure data structure; Load/Save handle persistence.
package forecast

import (
	"cmp"
	"context"
	"math"
	"slices"

	"github.com/egladman/magus/internal/hostmem"
	"github.com/egladman/magus/types"
)

// Forecaster computes adaptive shard plans from historical runtime data.
// History is read-only at forecast time; Forecasters are safe to copy.
type Forecaster struct {
	History       History
	Target        string
	TagsByProject map[string][]string
	// MemoryBudgetBytes caps a shard's predicted peak. Leave it zero: the
	// forecaster then derives a budget from the machine it is planning for, which
	// is what every caller should want and nobody can pick better by hand.
	//
	// It stays settable only because a test has to pin behavior without depending
	// on the memory of whatever runs the suite. There is deliberately no config
	// key and no flag behind it: the number a user would have to invent is a
	// fraction of a runner's RAM they cannot see, and a knob defaulting to "off"
	// would protect nobody while implying it did.
	MemoryBudgetBytes int64
}

// usableMemoryFraction is the share of the machine's memory the projects on one
// shard may be predicted to need.
//
// Not 1.0, because a runner is never only running the build: the CI agent, the
// Go build cache, node, and the toolchains all sit in the same memory, and the
// measured peaks this is compared against are the projects alone. Three quarters
// leaves that headroom without buying runners for work that would have fit.
const usableMemoryFraction = 0.75

// memoryBudget returns the byte budget for one shard: the explicit setting when
// a caller pinned one, otherwise three quarters of the host's memory, otherwise
// zero for "unknown, plan as before".
func (f Forecaster) memoryBudget() int64 {
	if f.MemoryBudgetBytes > 0 {
		return f.MemoryBudgetBytes
	}
	if total := hostmem.TotalBytes(context.Background()); total > 0 {
		return int64(float64(total) * usableMemoryFraction)
	}
	return 0
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
	return f.fitMemory(projects, durations, n, limit)
}

// fitMemory raises the shard count until no shard is predicted to exceed the
// memory budget, then packs.
//
// It exists because optimalShardCount reasons entirely in milliseconds: it
// weighs predicted work against runner setup cost and consolidates whenever
// setup would dominate. That is right for time and blind to the thing that
// actually ends a CI job - two heavy projects landing on one runner, which the
// duration model scores as a cheap win because their combined runtime is
// unremarkable. A runner that runs out of memory does not run slower, it
// disappears, and the job reports "cancelled" with no diagnostics at all.
//
// It can only ever SPLIT. Never dropping below n means this can add runners but
// never remove the ones the time model asked for, so the worst case of a wrong
// memory prediction is a job that costs more, not one that regresses.
//
// A project with no recorded peak contributes nothing to a shard's predicted
// total. That is deliberate: unknown is not zero, and pretending otherwise
// would either force every unmeasured project onto its own runner (if read as
// infinite) or quietly protect nothing (if read as free). Contributing nothing
// keeps today's behavior for what has never been measured, and tightens as
// history accumulates.
func (f Forecaster) fitMemory(projects []*types.Project, durations []int64, n, limit int) [][]*types.Project {
	budget := f.memoryBudget()
	if budget <= 0 {
		return lpt(projects, durations, n)
	}
	peaks := make([]int64, len(projects))
	for i, p := range projects {
		if b, ok := f.History.PredictPeakRSS(p.Path, f.Target); ok {
			peaks[i] = b
		}
	}
	for {
		assignments := lpt(projects, durations, n)
		if n >= limit || f.fits(assignments, projects, peaks, budget) {
			return assignments
		}
		n++
	}
}

// fits reports whether every shard's predicted peak is within the budget.
//
// A shard's figure is the SUM of its projects, and the contrast with
// types.PeakRSS - which takes a maximum - is not an inconsistency. That one
// aggregates the processes WITHIN one target, which run in sequence, so the
// high-water mark is whichever process peaked. This one aggregates the projects
// within a shard, which magus schedules CONCURRENTLY up to its concurrency
// budget, so their peaks can coincide and the runner has to hold all of them.
//
// Summing is also what makes splitting work at all: the maximum of a shard's
// projects does not fall when the shard is halved, so a max-based test could
// never be satisfied by adding runners, and the loop above would spin to the
// limit without ever improving anything.
func (f Forecaster) fits(assignments [][]*types.Project, projects []*types.Project, peaks []int64, budget int64) bool {
	index := make(map[string]int, len(projects))
	for i, p := range projects {
		index[p.Path] = i
	}
	for _, shard := range assignments {
		var total int64
		for _, p := range shard {
			total += peaks[index[p.Path]]
		}
		if total > budget {
			return false
		}
	}
	return true
}

// optimalShardCount returns the makespan-minimizing shard count N* = sqrt(W/α), clamped to [1, maxN].
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
		if durations[a] != durations[b] {
			return cmp.Compare(durations[b], durations[a])
		}
		return cmp.Compare(projects[a].Path, projects[b].Path)
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
