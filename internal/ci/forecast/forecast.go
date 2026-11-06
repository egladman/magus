package forecast

import (
	"cmp"
	"slices"

	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/types"
)

// Model computes adaptive shard plans from historical runtime data.
// History is read-only at forecast time; Models are safe to copy.
type Model struct {
	History       History
	TagsByProject map[string][]string
	// Needs is the transitive needs closure of a unit, the unit itself
	// included. nil treats every unit as needing nothing else.
	Needs func(types.Unit) []types.Unit
	// Repeatable reports whether a prerequisite may run on several shards.
	// nil repeats none.
	Repeatable func(types.Unit) bool
	// Slots is how many targets a shard runs at once; below 1 counts as 1.
	Slots int
	// MemoryBudgetBytes caps a shard's predicted peak; zero checks none. The
	// caller derives it from the configured machine, never the planner.
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
func (f Model) Cold(units []types.Unit) bool {
	return len(units) > 0 && len(f.Unmeasured(units)) == len(f.nodes(units))
}

// nodes is units and their prerequisites, each once.
func (f Model) nodes(units []types.Unit) []types.Unit {
	seen := map[types.Unit]bool{}
	var out []types.Unit
	note := func(u types.Unit) {
		if !seen[u] {
			seen[u] = true
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

func (f Model) slots() int {
	return max(f.Slots, 1)
}

func (f Model) repeatable(u types.Unit) bool {
	return f.Repeatable != nil && f.Repeatable(u)
}

// Plan partitions units into at most maxShards shards. Units that share a
// prerequisite Repeatable refuses go to one shard as a group. A shard costs
// SetupP50Ms plus its closure's targets run Slots at a time. Groups go
// longest-first, ties by project:target, to the shard they leave cheapest.
// A shard opens only when it shortens the longest one by more than setup
// plus the prerequisites it repeats. Equal history and units give an equal
// plan.
func (f Model) Plan(units []types.Unit, maxShards int) [][]types.Unit {
	if len(units) == 0 {
		return nil
	}
	w := f.weigh(units)
	return f.fitMemory(w, clampShards(maxShards, len(w.items)))
}

func clampShards(maxShards, items int) int {
	if maxShards <= 0 || maxShards > items {
		return items
	}
	return maxShards
}

// weights is a unit set prepared for packing. Each item is units that
// must share a shard, its closure as indexes into nodeMs. Items sort
// longest-first.
type weights struct {
	items   []item
	nodeMs  []int64
	setupMs int64
	slots   int64
}

// item is units placed together, with their merged closure.
type item struct {
	units   []types.Unit
	own     []int // each unit's index in nodeMs
	closure []int
}

func (f Model) weigh(units []types.Unit) weights {
	w := weights{setupMs: int64(f.History.effectiveConstants().SetupP50Ms), slots: int64(f.slots())}
	index := map[types.Unit]int{}
	var byIndex []types.Unit
	node := func(u types.Unit) int {
		if i, ok := index[u]; ok {
			return i
		}
		index[u] = len(w.nodeMs)
		byIndex = append(byIndex, u)
		w.nodeMs = append(w.nodeMs, f.predict(u))
		return index[u]
	}
	units = slices.Clone(units)
	sortUnits(units)
	units = slices.Compact(units)

	parent := make([]int, len(units))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	owner := map[int]int{}
	closures := make([][]int, len(units))
	for i, u := range units {
		closures[i] = []int{node(u)}
		if f.Needs != nil {
			for _, n := range f.Needs(u) {
				if j := node(n); !slices.Contains(closures[i], j) {
					closures[i] = append(closures[i], j)
				}
			}
		}
		for _, n := range closures[i] {
			if f.repeatable(byIndex[n]) {
				continue
			}
			if o, ok := owner[n]; ok {
				parent[find(i)] = find(o)
			} else {
				owner[n] = i
			}
		}
	}

	byRoot := map[int]int{}
	for i, u := range units {
		r := find(i)
		at, ok := byRoot[r]
		if !ok {
			at = len(w.items)
			byRoot[r] = at
			w.items = append(w.items, item{})
		}
		it := &w.items[at]
		it.units = append(it.units, u)
		it.own = append(it.own, closures[i][0])
		for _, n := range closures[i] {
			if !slices.Contains(it.closure, n) {
				it.closure = append(it.closure, n)
			}
		}
	}
	slices.SortStableFunc(w.items, func(a, b item) int {
		if x, y := w.costOf(a.closure), w.costOf(b.closure); x != y {
			return cmp.Compare(y, x)
		}
		return cmp.Compare(a.units[0].String(), b.units[0].String())
	})
	return w
}

func (f Model) predict(u types.Unit) int64 {
	return f.History.PredictDuration(u.Project, u.Target, f.TagsByProject[u.Project]).Milliseconds()
}

// cost is a shard's duration: setup, then its targets run slots at a
// time, never shorter than the longest one.
func (w weights) cost(sum, longest int64) int64 {
	return w.setupMs + max((sum+w.slots-1)/w.slots, longest)
}

func (w weights) costOf(closure []int) int64 {
	var sum, longest int64
	for _, n := range closure {
		sum += w.nodeMs[n]
		longest = max(longest, w.nodeMs[n])
	}
	return w.cost(sum, longest)
}

// bin is one shard while packing.
type bin struct {
	items   []int
	nodes   map[int]bool
	sum     int64
	longest int64
	ms      int64
}

// with is b's cost once item i joins it.
func (w weights) with(b bin, i int) int64 {
	sum, longest := b.sum, b.longest
	for _, n := range w.items[i].closure {
		if !b.nodes[n] {
			sum += w.nodeMs[n]
			longest = max(longest, w.nodeMs[n])
		}
	}
	return w.cost(sum, longest)
}

// pack places every item. It opens a shard unconditionally while fewer
// than minShards exist, and never past maxShards.
func (w weights) pack(minShards, maxShards int) []bin {
	var bins []bin
	open := func(i int) {
		bins = append(bins, w.add(bin{nodes: map[int]bool{}, ms: w.setupMs}, i))
	}
	for i := range w.items {
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

// opens reports whether item i on a shard of its own shortens the
// longest shard by more than setup plus the prerequisites it repeats.
func (w weights) opens(bins []bin, i int, joinedMs int64) bool {
	longest := makespan(bins)
	alone := w.costOf(w.items[i].closure)
	var repeated int64
	for _, n := range w.items[i].closure {
		if slices.Contains(w.items[i].own, n) {
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
	for _, n := range w.items[i].closure {
		if !b.nodes[n] {
			b.nodes[n] = true
			b.sum += w.nodeMs[n]
			b.longest = max(b.longest, w.nodeMs[n])
		}
	}
	b.ms = w.cost(b.sum, b.longest)
	b.items = append(b.items, i)
	return b
}

func (w weights) assignments(bins []bin) [][]types.Unit {
	out := make([][]types.Unit, len(bins))
	for s, b := range bins {
		for _, i := range b.items {
			out[s] = append(out[s], w.items[i].units...)
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

// fitMemory adds shards until every shard's peak fits the budget. A
// runner out of memory vanishes as "cancelled". It only splits, so a
// wrong peak costs runners, never a regression.
func (f Model) fitMemory(w weights, limit int) [][]types.Unit {
	bins := w.pack(1, limit)
	for f.MemoryBudgetBytes > 0 && len(bins) < limit && len(f.OverBudget(w.assignments(bins))) > 0 {
		bins = w.pack(len(bins)+1, limit)
	}
	return w.assignments(bins)
}

// peak is a shard's predicted peak memory: the Slots largest recorded
// peaks among its units and their prerequisites, as that many run at
// once. A target with no recorded peak adds nothing; UnmeasuredPeaks
// names each.
func (f Model) peak(shard []types.Unit) int64 {
	var peaks []int64
	for _, u := range f.nodes(shard) {
		if b, ok := f.History.PredictPeakRSS(u.Project, u.Target); ok {
			peaks = append(peaks, b)
		}
	}
	slices.SortFunc(peaks, func(a, b int64) int { return cmp.Compare(b, a) })
	var total int64
	for _, b := range peaks[:min(len(peaks), f.slots())] {
		total += b
	}
	return total
}

// SufficientShards is the smallest shard count that reaches the makespan
// Plan predicts within maxShards, or 0 when nothing is predictable.
// Advice, not a cap: fitMemory may still want more shards.
func (f Model) SufficientShards(units []types.Unit, maxShards int) int {
	if len(units) == 0 {
		return 0
	}
	w := f.weigh(units)
	best := w.pack(1, clampShards(maxShards, len(w.items)))
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
func (f Model) Unmeasured(units []types.Unit) []types.Unit {
	var out []types.Unit
	for _, u := range f.nodes(units) {
		if !f.History.Measured(u.Project, u.Target, f.TagsByProject[u.Project]) {
			out = append(out, u)
		}
	}
	sortUnits(out)
	return out
}

// UnmeasuredPeaks names the units and prerequisites the history holds no
// peak memory for, sorted by project:target. The memory check counts
// each as zero.
func (f Model) UnmeasuredPeaks(units []types.Unit) []types.Unit {
	var out []types.Unit
	for _, u := range f.nodes(units) {
		if _, ok := f.History.PredictPeakRSS(u.Project, u.Target); !ok {
			out = append(out, u)
		}
	}
	sortUnits(out)
	return out
}

// Repeats names each prerequisite that Repeatable refuses to repeat yet
// sits on more than one of shards, sorted by project:target. Every shard
// holding one computes it again.
func (f Model) Repeats(shards [][]types.Unit) []types.Unit {
	count := map[types.Unit]int{}
	var out []types.Unit
	for _, shard := range shards {
		for _, n := range f.nodes(shard) {
			if count[n]++; count[n] == 2 && !f.repeatable(n) {
				out = append(out, n)
			}
		}
	}
	sortUnits(out)
	return out
}

func sortUnits(us []types.Unit) {
	slices.SortFunc(us, func(a, b types.Unit) int { return cmp.Compare(a.String(), b.String()) })
}

// OverBudget names the shards whose predicted peak memory exceeds one runner's budget,
// by index. Empty when every shard fits, or when the budget is unknown.
//
// Asked AFTER packing rather than inside it, because fitMemory can only answer while it
// still has runners to add: at the shard cap its loop returns the plan it has, and
// whether that plan actually fits is the one thing nobody was told. A runner that
// exceeds its memory does not run slower, it disappears, and the job reports "cancelled"
// with no diagnostics, so this is the only warning a reader can get before it happens.
func (f Model) OverBudget(shards [][]types.Unit) []int {
	budget := f.MemoryBudgetBytes
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
