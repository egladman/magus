package forecast

import (
	"cmp"
	"slices"

	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/types"
)

// Forecaster computes adaptive shard plans from historical runtime data.
// History is read-only at forecast time; Forecasters are safe to copy.
type Forecaster struct {
	History       History
	TagsByProject map[string][]string
	// Needs is the transitive needs closure of a unit, the unit itself
	// included. nil treats every unit as needing nothing else.
	Needs func(types.Unit) []types.Unit
	// MemoryBudgetBytes caps a shard's predicted peak; zero checks none. The
	// caller derives it from the configured runner class, never the planner.
	MemoryBudgetBytes int64
}

// BudgetBytes is the share of a runner's memory the units on one shard may be
// predicted to need. Not all of it: the CI agent and the toolchains share the
// machine, and the measured peaks are the units alone.
func BudgetBytes(runnerMemoryMB int) int64 {
	return int64(float64(runnerMemoryMB) * (1 << 20) * mem.UsableFraction)
}

// ColdShards is the most shards a plan opens when no unit has a recorded
// duration. Unmeasured weights are guesses, and fanning out on guesses buys
// runners that may finish nothing sooner.
const ColdShards = 2

// Cold reports whether the history measured none of units or their
// prerequisites, so every weight in a pack would be the fallback.
func (f Forecaster) Cold(units []types.Unit) bool {
	return len(units) > 0 && len(f.Unmeasured(units)) == len(f.nodes(units))
}

// nodes is units and their prerequisites, each once.
func (f Forecaster) nodes(units []types.Unit) []types.Unit {
	seen := map[string]bool{}
	var out []types.Unit
	note := func(u types.Unit) {
		if k := u.String(); !seen[k] {
			seen[k] = true
			out = append(out, u)
		}
	}
	for _, u := range units {
		note(u)
		if f.Needs != nil {
			for _, n := range f.Needs(u) {
				note(n)
			}
		}
	}
	return out
}

func (f Forecaster) memoryBudget() int64 { return f.MemoryBudgetBytes }

// Plan partitions units into at most maxShards shards. A shard costs
// SetupP50Ms plus each distinct target in its units' needs closures.
// Units go longest-first, ties by project:target, to the shard they
// leave cheapest. A shard opens only when it shortens the longest one
// by more than setup plus the prerequisites it repeats. Equal history
// and units give an equal plan.
func (f Forecaster) Plan(units []types.Unit, maxShards int) [][]types.Unit {
	if len(units) == 0 {
		return nil
	}
	return f.fitMemory(f.weigh(units), clampShards(maxShards, len(units)))
}

func clampShards(maxShards, units int) int {
	if maxShards <= 0 || maxShards > units {
		return units
	}
	return maxShards
}

// weights is a unit set prepared for packing: each closure as indexes
// into nodeMs, and units sorted longest-first.
type weights struct {
	units   []types.Unit
	closure [][]int
	own     []int // own[i] is units[i]'s index in nodeMs
	nodeMs  []int64
	setupMs int64
}

func (f Forecaster) weigh(units []types.Unit) weights {
	w := weights{setupMs: int64(f.History.effectiveConstants().SetupP50Ms)}
	index := map[string]int{}
	node := func(u types.Unit) int {
		k := u.String()
		if i, ok := index[k]; ok {
			return i
		}
		index[k] = len(w.nodeMs)
		w.nodeMs = append(w.nodeMs, f.predict(u))
		return index[k]
	}
	type weighed struct {
		unit    types.Unit
		own     int
		closure []int
		ms      int64
	}
	all := make([]weighed, 0, len(units))
	for _, u := range units {
		x := weighed{unit: u, own: node(u), closure: []int{node(u)}}
		if f.Needs != nil {
			for _, n := range f.Needs(u) {
				if i := node(n); !slices.Contains(x.closure, i) {
					x.closure = append(x.closure, i)
				}
			}
		}
		for _, i := range x.closure {
			x.ms += w.nodeMs[i]
		}
		all = append(all, x)
	}
	slices.SortStableFunc(all, func(a, b weighed) int {
		if a.ms != b.ms {
			return cmp.Compare(b.ms, a.ms)
		}
		return cmp.Compare(a.unit.String(), b.unit.String())
	})
	for _, x := range all {
		w.units = append(w.units, x.unit)
		w.own = append(w.own, x.own)
		w.closure = append(w.closure, x.closure)
	}
	return w
}

func (f Forecaster) predict(u types.Unit) int64 {
	return f.History.PredictDuration(u.Project, u.Target, f.TagsByProject[u.Project]).Milliseconds()
}

// bin is one shard while packing.
type bin struct {
	units []int
	nodes map[int]bool
	ms    int64
}

// with is b's cost once unit i joins it.
func (w weights) with(b bin, i int) int64 {
	ms := b.ms
	for _, n := range w.closure[i] {
		if !b.nodes[n] {
			ms += w.nodeMs[n]
		}
	}
	return ms
}

// pack places every unit. It opens a shard unconditionally while fewer
// than minShards exist, and never past maxShards.
func (w weights) pack(minShards, maxShards int) []bin {
	var bins []bin
	open := func(i int) {
		bins = append(bins, w.add(bin{nodes: map[int]bool{}, ms: w.setupMs}, i))
	}
	for i := range w.units {
		if len(bins) == 0 {
			open(i)
			continue
		}
		best, bestMs := 0, w.with(bins[0], i)
		for s := 1; s < len(bins); s++ {
			if ms := w.with(bins[s], i); ms < bestMs {
				best, bestMs = s, ms
			}
		}
		if len(bins) < maxShards && (len(bins) < minShards || w.opens(bins, i, bestMs)) {
			open(i)
			continue
		}
		bins[best] = w.add(bins[best], i)
	}
	return bins
}

// opens reports whether unit i on a shard of its own shortens the
// longest shard by more than setup plus the prerequisites it repeats.
func (w weights) opens(bins []bin, i int, joinedMs int64) bool {
	longest := makespan(bins)
	alone := w.setupMs
	var repeated int64
	for _, n := range w.closure[i] {
		alone += w.nodeMs[n]
		if n == w.own[i] {
			continue
		}
		for _, b := range bins {
			if b.nodes[n] {
				repeated += w.nodeMs[n]
				break
			}
		}
	}
	gain := max(longest, joinedMs) - max(longest, alone)
	return gain > w.setupMs+repeated
}

func (w weights) add(b bin, i int) bin {
	b.ms = w.with(b, i)
	for _, n := range w.closure[i] {
		b.nodes[n] = true
	}
	b.units = append(b.units, i)
	return b
}

func (w weights) assignments(bins []bin) [][]types.Unit {
	out := make([][]types.Unit, len(bins))
	for s, b := range bins {
		for _, i := range b.units {
			out[s] = append(out[s], w.units[i])
		}
	}
	return out
}

func makespan(bins []bin) int64 {
	var longest int64
	for _, b := range bins {
		longest = max(longest, b.ms)
	}
	return longest
}

// fitMemory adds shards until every shard's peaks fit the budget. A
// runner out of memory vanishes as "cancelled". It only splits, so a
// wrong peak costs runners, never a regression.
func (f Forecaster) fitMemory(w weights, limit int) [][]types.Unit {
	budget := f.memoryBudget()
	bins := w.pack(1, limit)
	for budget > 0 && len(bins) < limit && !f.fits(w.assignments(bins), budget) {
		bins = w.pack(len(bins)+1, limit)
	}
	return w.assignments(bins)
}

// fits reports whether every shard's summed unit peaks fit the budget.
// A sum, since a shard's units run concurrently. A unit with no recorded
// peak adds nothing: unknown is neither free nor vast.
func (f Forecaster) fits(shards [][]types.Unit, budget int64) bool {
	for _, shard := range shards {
		if f.peak(shard) > budget {
			return false
		}
	}
	return true
}

func (f Forecaster) peak(shard []types.Unit) int64 {
	var total int64
	for _, u := range shard {
		if b, ok := f.History.PredictPeakRSS(u.Project, u.Target); ok {
			total += b
		}
	}
	return total
}

// SufficientShards is the smallest shard count that reaches the makespan
// Plan predicts within maxShards, or 0 when nothing is predictable.
// Advice, not a cap: fitMemory may still want more shards.
func (f Forecaster) SufficientShards(units []types.Unit, maxShards int) int {
	if len(units) == 0 {
		return 0
	}
	w := f.weigh(units)
	best := w.pack(1, clampShards(maxShards, len(units)))
	if makespan(best) <= w.setupMs {
		return 0
	}
	for n := 1; n < len(best); n++ {
		if makespan(w.pack(1, n)) <= makespan(best) {
			return n
		}
	}
	return len(best)
}

// Unmeasured names the units and prerequisites the history holds no
// duration for, sorted by project:target.
func (f Forecaster) Unmeasured(units []types.Unit) []types.Unit {
	var out []types.Unit
	for _, u := range f.nodes(units) {
		if !f.History.Measured(u.Project, u.Target, f.TagsByProject[u.Project]) {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b types.Unit) int { return cmp.Compare(a.String(), b.String()) })
	return out
}

// OverBudget names the shards whose predicted peak memory exceeds one runner's budget,
// by index. Empty when every shard fits, or when the budget is unknown.
//
// Asked AFTER packing rather than inside it, because fitMemory can only answer while it
// still has runners to add: at the shard cap its loop returns the plan it has, and
// whether that plan actually fits is the one thing nobody was told. A runner that
// exceeds its memory does not run slower, it disappears, and the job reports "cancelled"
// with no diagnostics, so this is the only warning a reader can get before it happens.
func (f Forecaster) OverBudget(shards [][]types.Unit) []int {
	budget := f.memoryBudget()
	if budget <= 0 {
		return nil
	}
	var out []int
	for i, shard := range shards {
		if f.peak(shard) > budget {
			out = append(out, i)
		}
	}
	return out
}
