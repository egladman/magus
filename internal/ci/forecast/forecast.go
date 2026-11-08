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
	items    []item
	nodeMs   []int64
	nodePeak []int64 // -1 when the history holds no peak
	setupMs  int64
	slots    int64
}

// item is units placed together, with their merged closure.
type item struct {
	units   []types.Unit
	own     []int // each unit's index in nodeMs
	closure []int
	alone   int64 // the item's cost on a shard of its own
}

func (f Model) weigh(units []types.Unit) weights {
	g := &weighing{
		f:     f,
		w:     weights{setupMs: int64(f.History.effectiveConstants().SetupP50Ms), slots: int64(f.slots())},
		index: map[types.Unit]int{},
	}
	units = slices.Clone(units)
	sortUnits(units)
	units = slices.Compact(units)
	closures, root := g.closures(units)
	g.group(units, closures, root)
	return g.w
}

// weighing is one weigh call's state: a node per unit and per need, in
// first-seen order.
type weighing struct {
	f       Model
	w       weights
	index   map[types.Unit]int
	byIndex []types.Unit
	// optimization: a stamp per node dedupes each closure in O(1) per
	// node; slices.Contains made weigh quadratic in closure size.
	stamp []int
}

func (g *weighing) node(u types.Unit) int {
	if i, ok := g.index[u]; ok {
		return i
	}
	g.index[u] = len(g.w.nodeMs)
	g.byIndex = append(g.byIndex, u)
	g.w.nodeMs = append(g.w.nodeMs, g.f.predict(u))
	peak, ok := g.f.History.PredictPeakRSS(u.Project, u.Target)
	if !ok {
		peak = -1
	}
	g.w.nodePeak = append(g.w.nodePeak, peak)
	return g.index[u]
}

// mark reports whether node n is new to the set id names, and adds it.
func (g *weighing) mark(n, id int) bool {
	for len(g.stamp) <= n {
		g.stamp = append(g.stamp, -1)
	}
	if g.stamp[n] == id {
		return false
	}
	g.stamp[n] = id
	return true
}

// closures returns each unit's closure, its own node first, and root, which
// joins units that share a node no shard may repeat.
func (g *weighing) closures(units []types.Unit) ([][]int, func(int) int) {
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
		own := g.node(u)
		g.mark(own, i)
		closures[i] = []int{own}
		if g.f.Needs != nil {
			for _, n := range g.f.Needs(u) {
				if j := g.node(n); g.mark(j, i) {
					closures[i] = append(closures[i], j)
				}
			}
		}
		for _, n := range closures[i] {
			if g.f.repeatable(g.byIndex[n]) {
				continue
			}
			if o, ok := owner[n]; ok {
				parent[find(i)] = find(o)
			} else {
				owner[n] = i
			}
		}
	}
	return closures, find
}

// group folds units sharing a root into one item, then orders the items
// heaviest first, by first unit on a tie.
func (g *weighing) group(units []types.Unit, closures [][]int, root func(int) int) {
	w := &g.w
	byRoot := map[int]int{}
	for i, u := range units {
		r := root(i)
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
			if g.mark(n, len(units)+at) {
				it.closure = append(it.closure, n)
			}
		}
	}
	for i := range w.items {
		w.items[i].alone = w.closureCost(w.items[i].closure)
	}
	slices.SortStableFunc(w.items, func(a, b item) int {
		if a.alone != b.alone {
			return cmp.Compare(b.alone, a.alone)
		}
		return cmp.Compare(a.units[0].String(), b.units[0].String())
	})
}

func (f Model) predict(u types.Unit) int64 {
	return f.History.PredictDuration(u.Project, u.Target, f.TagsByProject[u.Project]).Milliseconds()
}

// cost is a shard's duration: setup, then its targets run slots at a
// time, never shorter than the longest one.
func (w weights) cost(sum, longest int64) int64 {
	return w.setupMs + max((sum+w.slots-1)/w.slots, longest)
}

// closureCost is closure's cost on a shard of its own.
func (w weights) closureCost(closure []int) int64 {
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
	nodes   []bool // indexed by node
	held    []int  // the nodes set in nodes
	sum     int64
	longest int64
	ms      int64
}

// with is b's cost once item i joins it.
func (w weights) with(b *bin, i int) int64 {
	sum, longest := b.sum, b.longest
	for _, n := range w.items[i].closure {
		if !b.nodes[n] {
			sum += w.nodeMs[n]
			longest = max(longest, w.nodeMs[n])
		}
	}
	return w.cost(sum, longest)
}

// packing is pack's state. inBins counts the bins holding each node and
// longest is the makespan, both kept as bins change.
type packing struct {
	w       weights
	bins    []bin
	inBins  []int
	longest int64
}

// pack places every item. It opens a shard unconditionally while fewer
// than minShards exist, and never past maxShards.
func (w weights) pack(minShards, maxShards int) []bin {
	p := packing{w: w, inBins: make([]int, len(w.nodeMs))}
	for i := range w.items {
		if len(p.bins) == 0 {
			p.open(i)
			continue
		}
		best, bestMs := 0, w.with(&p.bins[0], i)
		for s := 1; s < len(p.bins); s++ {
			if ms := w.with(&p.bins[s], i); ms < bestMs {
				best, bestMs = s, ms
			}
		}
		if len(p.bins) < maxShards && (len(p.bins) < minShards || p.opens(i, bestMs)) {
			p.open(i)
			continue
		}
		p.add(best, i)
	}
	return p.bins
}

func (p *packing) open(i int) {
	p.bins = append(p.bins, bin{nodes: make([]bool, len(p.w.nodeMs)), ms: p.w.setupMs})
	p.add(len(p.bins)-1, i)
}

// opens reports whether item i on a shard of its own shortens the
// longest shard by more than setup plus the prerequisites it repeats.
func (p *packing) opens(i int, joinedMs int64) bool {
	it := p.w.items[i]
	var repeated int64
	for _, n := range it.closure {
		if p.inBins[n] > 0 && !slices.Contains(it.own, n) {
			repeated += p.w.nodeMs[n]
		}
	}
	gain := max(p.longest, joinedMs) - max(p.longest, it.alone)
	return gain > p.w.setupMs+repeated
}

func (p *packing) add(s, i int) {
	b := &p.bins[s]
	for _, n := range p.w.items[i].closure {
		if !b.nodes[n] {
			b.nodes[n] = true
			b.held = append(b.held, n)
			p.inBins[n]++
			b.sum += p.w.nodeMs[n]
			b.longest = max(b.longest, p.w.nodeMs[n])
		}
	}
	b.ms = p.w.cost(b.sum, b.longest)
	b.items = append(b.items, i)
	p.longest = max(p.longest, b.ms)
}

// fits reports whether every bin's peak, its slots largest node peaks,
// fits budget.
func (w weights) fits(bins []bin, budget int64) bool {
	var peaks []int64
	for _, b := range bins {
		peaks = peaks[:0]
		for _, n := range b.held {
			if w.nodePeak[n] >= 0 {
				peaks = append(peaks, w.nodePeak[n])
			}
		}
		slices.SortFunc(peaks, func(a, b int64) int { return cmp.Compare(b, a) })
		var total int64
		for _, pk := range peaks[:min(len(peaks), int(w.slots))] {
			total += pk
		}
		if total > budget {
			return false
		}
	}
	return true
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
//
// optimization: the smallest fitting minimum is binary searched, as more
// shards never raise a shard's peak. Stepping one shard at a time
// repacked up to 256 times.
func (f Model) fitMemory(w weights, limit int) [][]types.Unit {
	budget := f.MemoryBudgetBytes
	bins := w.pack(1, limit)
	if budget <= 0 || len(bins) >= limit || w.fits(bins, budget) {
		return w.assignments(bins)
	}
	lo, hi := len(bins)+1, limit
	found, foundAt := []bin(nil), 0
	for lo < hi {
		mid := lo + (hi-lo)/2
		if b := w.pack(mid, limit); w.fits(b, budget) {
			hi, found, foundAt = mid, b, mid
		} else {
			lo = mid + 1
		}
	}
	if foundAt != lo {
		found = w.pack(lo, limit)
	}
	return w.assignments(found)
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
	// optimization: binary search, as a higher cap never lengthens the
	// makespan. The linear scan packed once per count.
	target := makespan(best)
	lo, hi := 1, len(best)
	for lo < hi {
		if mid := lo + (hi-lo)/2; makespan(w.pack(1, mid)) <= target {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
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

// Stages is a plan in two stages: every shard of Zero runs before any of One.
type Stages struct {
	// Zero computes each prerequisite that two or more shards of One would
	// otherwise both compute, and each one a unit of another project needs.
	// Empty when there is neither.
	Zero [][]types.Unit
	// One holds the units Zero does not compute. Its shards replay Zero's
	// units from the cache Zero leaves.
	One [][]types.Unit
}

// PlanStages is Plan with shared computing work moved to a stage before it.
// It packs with the shared prerequisites counted as repeatable, so their
// dependents may split. Each one the split leaves on two shards goes to
// Zero, and so does each one Crossing names, wherever the split put it. Zero
// packs under the same maxShards and memory budget. With none, One is
// Plan(units, maxShards). Needs must answer for prerequisites too.
func (f Model) PlanStages(units []types.Unit, maxShards int) Stages {
	crossing := f.Crossing(units)
	if shared := union(f.shared(units), crossing); len(shared) > 0 {
		split := f.Replaying(shared).Plan(units, maxShards)
		if zero := union(f.Repeats(split), crossing); len(zero) > 0 {
			return Stages{Zero: f.Plan(zero, maxShards), One: without(split, zero)}
		}
	}
	return Stages{One: f.Plan(units, maxShards)}
}

// Crossing names each prerequisite Repeatable refuses that a unit of another
// project needs, sorted by project:target. A shard holding that unit computes
// each one too, so the other project's work gates its dependent's runner.
func (f Model) Crossing(units []types.Unit) []types.Unit {
	if f.Needs == nil {
		return nil
	}
	seen := map[types.Unit]bool{}
	var out []types.Unit
	for _, u := range units {
		for _, n := range f.Needs(u) {
			if n.Project == u.Project || seen[n] || f.repeatable(n) {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	sortUnits(out)
	return out
}

// union is a and b, each unit once, sorted by project:target.
func union(a, b []types.Unit) []types.Unit {
	out := slices.Clone(a)
	for _, u := range b {
		if !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	sortUnits(out)
	return out
}

// Replaying is f with units counted as Repeatable, as a stage that runs
// after they computed replays them.
func (f Model) Replaying(units []types.Unit) Model {
	done := make(map[types.Unit]bool, len(units))
	for _, u := range units {
		done[u] = true
	}
	g := f
	g.Repeatable = func(u types.Unit) bool { return done[u] || f.repeatable(u) }
	return g
}

// shared names each prerequisite Repeatable refuses that two or more of
// units' closures hold, sorted by project:target.
func (f Model) shared(units []types.Unit) []types.Unit {
	if f.Needs == nil {
		return nil
	}
	held := map[types.Unit]int{}
	var out []types.Unit
	for _, u := range units {
		seen := map[types.Unit]bool{}
		for _, n := range f.Needs(u) {
			if seen[n] || f.repeatable(n) {
				continue
			}
			seen[n] = true
			if held[n]++; held[n] == 2 {
				out = append(out, n)
			}
		}
	}
	sortUnits(out)
	return out
}

// without is shards less the units in drop, less any shard left empty.
func without(shards [][]types.Unit, drop []types.Unit) [][]types.Unit {
	var out [][]types.Unit
	for _, shard := range shards {
		kept := slices.DeleteFunc(slices.Clone(shard), func(u types.Unit) bool { return slices.Contains(drop, u) })
		if len(kept) > 0 {
			out = append(out, kept)
		}
	}
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
