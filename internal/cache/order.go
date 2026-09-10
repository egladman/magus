package cache

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// TargetNode is one target's declared file footprint, the unit order derivation
// works on: a batch step's own target, or a chain member that step composes.
// Reads and Writes are workspace-rooted doublestar globs, resolved the same way
// the cache key resolves them (buildStep), so what orders and what hashes agree.
type TargetNode struct {
	Project string
	Target  string
	// Steps are the node keys (DepKey) of the batch steps whose execution runs this
	// target. Usually one; a target composed by several steps' chains carries all of
	// them, and derivation treats an edge as ordered only when every owner is.
	Steps []string
	// Reads is the target's source globs; Writes its output plus in-place-update
	// globs. Updates count as writes here even though describe.go derives no
	// DependsOn edge from them: within one batch, ordering a reader after an
	// in-place editor can only over-order, and the motivating chain
	// (changelog-generate -> content-generate) is declared exactly that way.
	Reads  []string
	Writes []string
	// DeclaredReads is false when Reads fell back to the project baseline because the
	// target names no ctx.readsFiles. Edges into such a reader are weak: the baseline
	// is a whole-project over-approximation, so a cycle through a weak edge is an
	// artifact to drop, not an authoring error to report.
	DeclaredReads  bool
	DeclaredWrites bool
	// IgnoreDirs are dir names the reader's source walk prunes (vendor, gen, ...).
	// A fallback reader provably never sees files under them, so writes landing
	// wholly inside one derive no edge.
	IgnoreDirs []string
	// Needs are the ctx.needs calls this target makes, in body order. They are the only
	// sequencing that exists INSIDE a step: a call fans its members out unordered and
	// returns when all of them have run, so the members of one call are siblings, every
	// call completes before this target's own work, and a later call starts after the
	// earlier ones have completed. That is what decides whether a same-step overlap is
	// ordered or unschedulable; see FindSameStepConflicts.
	Needs Calls
}

// Call is one ctx.needs call: the node keys it fans out, in argument order.
type Call []string

// Calls is a target's ctx.needs calls in body order, built the way the body reads:
// Needs(a, b) is one call, .Needs(c) the call after it. ChainCalls builds the real thing
// from a project's chain; this is for code that states the calls by hand.
type Calls []Call

// Needs starts the calls with the first ctx.needs call.
func Needs(keys ...string) Calls { return Calls{Call(keys)} }

// Needs appends one more ctx.needs call, ordered after every call before it.
func (c Calls) Needs(keys ...string) Calls { return append(c, Call(keys)) }

// members is every node key the calls dispatch, in body order.
func (c Calls) members() []string {
	var out []string
	for _, call := range c {
		out = append(out, call...)
	}
	return out
}

// before lists the members of the calls before the one naming member: the nodes that
// have completed before member starts, by this composer's own sequencing. Nil when
// member is in the first call or is not a member at all.
func (c Calls) before(member string) []string {
	var out []string
	for _, call := range c {
		if slices.Contains(call, member) {
			return out
		}
		out = append(out, call...)
	}
	return nil
}

// Key returns the node's scheduling identity, shared with the step barrier.
func (n TargetNode) Key() string { return DepKey(n.Project, n.Target) }

// DerivedEdge records that Writer's declared writes intersect Reader's declared
// reads, so Reader's result depends on running after Writer. Indices into
// DerivedOrder.Nodes.
type DerivedEdge struct {
	Writer, Reader int
	// weak marks an edge whose writer or reader side is a baseline fallback rather
	// than an explicit declaration; weak edges yield when they close a cycle.
	weak bool
	// Ordered reports the batch schedule honors this edge: the writer's step(s) all
	// run strictly before the reader's, via a coarse DependsOn edge or a derived
	// RunAfter edge. An unordered edge is a settling candidate for the caller.
	Ordered bool
}

// DroppedEdge is a derived edge removed to break a cycle. The dependency is
// real, so it stays a settling candidate; only the schedule cannot express it.
type DroppedEdge struct {
	DerivedEdge
	// Reason is "weak" for a baseline-fallback side that yielded, "cycle" for the
	// tie-break that breaks a cycle of explicit declarations.
	Reason string
}

// DerivedOrder is the result of DeriveTargetOrder: the target-granular
// writer-before-reader edges a batch implies, and the step-level ordering that
// enforces the enforceable subset.
type DerivedOrder struct {
	Nodes []TargetNode
	Edges []DerivedEdge
	// Dropped holds the edges cycle resolution removed from Edges. Kept out of the
	// schedule and out of TopoNodes, but the caller folds them back in as
	// permanently unordered edges so their readers can still settle.
	Dropped []DroppedEdge
	// RunAfter maps a step's node key to the step node keys it must wait for,
	// beyond its coarse DependsOn. RunAll's barrier waits these exactly.
	RunAfter map[string][]string
	// SameStep holds the overlaps inside one step that nothing sequences. They are not
	// edges: no schedule can express them, and settling cannot repair them either, since
	// both targets run in one window. The caller refuses the run over them.
	SameStep SameStepConflicts
}

// DeriveTargetOrder derives cross-step, target-granular ordering from declared
// footprints. steps are the batch RunAll will schedule; nodes are the targets it
// runs (each step's own target plus chain members), with resolved globs.
//
// An edge is derived when one node's writes can touch a path another node's reads
// can match, the two never run inside the same step, and they are not the same
// target (a target reading what it writes is not an edge). Glob intersection is
// conservative: uncertainty derives the edge, which can only over-order.
//
// No cycle is fatal: a cycle is legitimate authoring (every sibling routing index
// declares it writes its own MAGUS.md and reads the others'), and the answer is
// the same one the entangled shape already gets. Cycle resolution drops edges
// into Dropped until the rest is acyclic; the drops are still settled after the
// batch. Edges whose direction the step schedule can honor become RunAfter
// entries; the rest are marked unordered for the caller to settle. Coarse
// DependsOn edges always win over derived ones: project-level ordering (and the
// affected set) is never widened or narrowed here.
func DeriveTargetOrder(steps []Step, nodes []TargetNode, witness OverlapWitness) *DerivedOrder {
	d := &DerivedOrder{Nodes: nodes, RunAfter: map[string][]string{}, SameStep: FindSameStepConflicts(nodes, witness)}

	for w := range nodes {
		for r := range nodes {
			if w == r || nodes[w].Key() == nodes[r].Key() {
				continue
			}
			// A pair that runs in exactly the same steps is the body's own ctx.needs
			// sequencing, which stays untouched; only cross-step order is magus's to
			// derive, and an unsequenced pair is already in SameStep above, for the
			// caller to refuse over. A pair that only PARTLY shares its steps still
			// meets across the steps it does not share, so the edge derives and the
			// projection decides, per owner, whether the schedule can honor it.
			if nodes[w].identicalSteps(nodes[r]) {
				continue
			}
			if _, _, ok := nodes[w].overlap(nodes[r]); !ok {
				continue
			}
			d.Edges = append(d.Edges, DerivedEdge{
				Writer: w, Reader: r,
				weak: !nodes[w].DeclaredWrites || !nodes[r].DeclaredReads,
			})
		}
	}

	d.resolveFineCycles()
	d.projectOntoSteps(steps)
	return d
}

// TopoNodes returns every node index in dependency order: each edge's writer
// before its reader. Dropped edges are excluded, which is what makes the walk
// well defined. Deterministic.
func (d *DerivedOrder) TopoNodes() []int {
	indeg := make([]int, len(d.Nodes))
	for _, e := range d.Edges {
		indeg[e.Reader]++
	}
	var ready, out []int
	for n := range d.Nodes {
		if indeg[n] == 0 {
			ready = append(ready, n)
		}
	}
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		out = append(out, n)
		for _, e := range d.Edges {
			if e.Writer != n {
				continue
			}
			if indeg[e.Reader]--; indeg[e.Reader] == 0 {
				ready = append(ready, e.Reader)
			}
		}
	}
	return out
}

// overlap reports whether n's writes can produce a path r's reads match, and names the
// first write glob and read glob that can meet: a refusal has to say which, and
// recomputing the pair in the message would be a second answer to the same question.
// For a fallback reader, writes confined to the reader's pruned dirs are invisible to
// it and derive nothing.
func (n TargetNode) overlap(r TargetNode) (write, read string, ok bool) {
	for _, wg := range n.Writes {
		if !r.DeclaredReads && underIgnoredDir(wg, r.IgnoreDirs) {
			continue
		}
		for _, rg := range r.Reads {
			if globsOverlap(wg, rg) {
				return wg, rg, true
			}
		}
	}
	return "", "", false
}

// sharedStep returns the first step key (in n's order) that runs both nodes.
func (n TargetNode) sharedStep(other TargetNode) (string, bool) {
	for _, s := range n.Steps {
		if slices.Contains(other.Steps, s) {
			return s, true
		}
	}
	return "", false
}

// identicalSteps reports that the two nodes run in exactly the same steps, so no step
// runs one without the other.
func (n TargetNode) identicalSteps(other TargetNode) bool {
	if len(n.Steps) != len(other.Steps) {
		return false
	}
	for _, s := range n.Steps {
		if !slices.Contains(other.Steps, s) {
			return false
		}
	}
	return true
}

// underIgnoredDir reports whether every path glob can match lies inside one of
// the named directories. Only the glob's leading literal segments are decidable;
// a meta segment before any match means "cannot prove", so the answer is false
// and the edge stays (the conservative direction).
func underIgnoredDir(glob string, ignore []string) bool {
	if len(ignore) == 0 {
		return false
	}
	for seg := range strings.SplitSeq(glob, "/") {
		if isMetaSegment(seg) {
			return false
		}
		if slices.Contains(ignore, seg) {
			return true
		}
	}
	return false
}

func isMetaSegment(seg string) bool { return strings.ContainsAny(seg, "*?[{") }

// globsOverlap conservatively reports whether two doublestar globs can match a
// common path. False only when provable: a literal path one side rejects,
// diverging literal prefixes, or incompatible literal filename suffixes.
// Everything else answers true; over-ordering is safe, a missed edge is not.
func globsOverlap(a, b string) bool {
	aMeta, bMeta := isMetaSegment(a), isMetaSegment(b)
	switch {
	case !aMeta && !bMeta:
		return a == b
	case !aMeta:
		ok, err := doublestar.Match(b, a)
		return ok || err != nil
	case !bMeta:
		ok, err := doublestar.Match(a, b)
		return ok || err != nil
	}
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if isMetaSegment(as[i]) || isMetaSegment(bs[i]) {
			break
		}
		if as[i] != bs[i] {
			return false
		}
		// Both consumed a literal segment. The shorter side, exhausted, may name a
		// directory the longer one descends into, which is an overlap this cannot
		// rule out; conservative, since over-ordering is cheap and a refusal stands on
		// a witnessed file besides.
		if i == len(as)-1 || i == len(bs)-1 {
			return true
		}
	}
	if as[len(as)-1] == "**" || bs[len(bs)-1] == "**" {
		return true
	}
	sa, sb := literalSuffix(as[len(as)-1]), literalSuffix(bs[len(bs)-1])
	return strings.HasSuffix(sa, sb) || strings.HasSuffix(sb, sa)
}

// literalSuffix returns the literal tail of one glob segment: everything after
// the last metacharacter ("" when the segment ends in one).
func literalSuffix(seg string) string {
	if i := strings.LastIndexAny(seg, "*?[]{}"); i >= 0 {
		return seg[i+1:]
	}
	return seg
}

// resolveFineCycles moves edges out of Edges until no cycle remains. A weak
// (baseline-fallback) edge yields first, since it may not describe a real
// dependency at all; a cycle of explicit declarations yields to the tie-break.
func (d *DerivedOrder) resolveFineCycles() {
	for {
		cycle := d.findCycle()
		if cycle == nil {
			return
		}
		reason := "weak"
		drop := make([]int, 0, len(cycle))
		for _, ei := range cycle {
			if d.Edges[ei].weak {
				drop = append(drop, ei)
			}
		}
		if len(drop) == 0 {
			reason = "cycle"
			drop = append(drop, d.cycleBackEdge(cycle))
		}
		// Descending index order: deleting shifts everything after the hole.
		slices.Sort(drop)
		for _, ei := range slices.Backward(drop) {
			d.Dropped = append(d.Dropped, DroppedEdge{DerivedEdge: d.Edges[ei], Reason: reason})
			d.Edges = slices.Delete(d.Edges, ei, ei+1)
		}
	}
}

// cycleBackEdge picks the edge that breaks a cycle of explicit declarations: the
// one whose writer key sorts after its reader key, greatest (writer, reader)
// pair when several qualify. Walking a cycle the keys must both rise and fall,
// so one always qualifies and the loop always shrinks. The choice reads keys
// rather than node indices, which shift with the batch's composition.
func (d *DerivedOrder) cycleBackEdge(cycle []int) int {
	best, bestPair := cycle[0], ""
	for _, ei := range cycle {
		w := d.Nodes[d.Edges[ei].Writer].Key()
		r := d.Nodes[d.Edges[ei].Reader].Key()
		if w < r {
			continue
		}
		if pair := w + "\x00" + r; pair > bestPair {
			best, bestPair = ei, pair
		}
	}
	return best
}

// findCycle returns the edge indices of one cycle, in walk order, or nil.
// Deterministic: adjacency follows edge insertion order.
func (d *DerivedOrder) findCycle() []int {
	adj := map[int][]int{}
	for ei, e := range d.Edges {
		adj[e.Writer] = append(adj[e.Writer], ei)
	}
	const (
		white = iota
		grey
		black
	)
	color := map[int]int{}
	var stack []int
	var visit func(n int) []int
	visit = func(n int) []int {
		color[n] = grey
		for _, ei := range adj[n] {
			m := d.Edges[ei].Reader
			switch color[m] {
			case grey:
				for i, p := range stack {
					if d.Edges[p].Writer == m {
						return append(append([]int(nil), stack[i:]...), ei)
					}
				}
				return append(append([]int(nil), stack...), ei)
			case white:
				stack = append(stack, ei)
				if cyc := visit(m); cyc != nil {
					return cyc
				}
				stack = stack[:len(stack)-1]
			}
		}
		color[n] = black
		return nil
	}
	for n := range d.Nodes {
		if color[n] != white {
			continue
		}
		if cyc := visit(n); cyc != nil {
			return cyc
		}
	}
	return nil
}

// projectOntoSteps turns fine edges into step-level RunAfter ordering where the
// combined step graph stays acyclic, and marks the rest unordered. Coarse
// DependsOn edges are never dropped: where a derived direction conflicts with
// them (the entangled shape hand-wiring used to be the only answer for), the
// fine edge is left to post-batch settling instead of deadlocking the barrier.
func (d *DerivedOrder) projectOntoSteps(steps []Step) {
	inScope := make(map[string]bool, len(steps))
	for _, s := range steps {
		inScope[stepKey(s)] = true
	}
	// coarse holds today's barrier edges (upstream -> dependent, same target).
	coarse := map[string][]string{}
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			k := DepKey(dep, s.Target)
			if inScope[k] && k != stepKey(s) {
				coarse[k] = append(coarse[k], stepKey(s))
			}
		}
	}

	type stepEdge struct{ from, to string }
	induced := map[stepEdge][]int{}
	for ei, e := range d.Edges {
		for _, sw := range d.Nodes[e.Writer].Steps {
			for _, sr := range d.Nodes[e.Reader].Steps {
				if sw != sr && inScope[sw] && inScope[sr] {
					induced[stepEdge{sw, sr}] = append(induced[stepEdge{sw, sr}], ei)
				}
			}
		}
	}

	// Admit induced edges one at a time, keeping the combined graph acyclic.
	// Deterministic order so the same batch always schedules the same way.
	kept := map[stepEdge]bool{}
	reachable := func(from, to string) bool {
		seen := map[string]bool{from: true}
		queue := []string{from}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			if n == to {
				return true
			}
			next := slices.Clone(coarse[n])
			for e := range kept {
				if e.from == n {
					next = append(next, e.to)
				}
			}
			for _, m := range next {
				if !seen[m] {
					seen[m] = true
					queue = append(queue, m)
				}
			}
		}
		return false
	}
	edges := make([]stepEdge, 0, len(induced))
	for e := range induced {
		edges = append(edges, e)
	}
	slices.SortFunc(edges, func(a, b stepEdge) int {
		if a.from != b.from {
			return strings.Compare(a.from, b.from)
		}
		return strings.Compare(a.to, b.to)
	})
	for _, e := range edges {
		if !reachable(e.to, e.from) {
			kept[e] = true
		}
	}

	for ei := range d.Edges {
		e := &d.Edges[ei]
		e.Ordered = true
		for _, sw := range d.Nodes[e.Writer].Steps {
			for _, sr := range d.Nodes[e.Reader].Steps {
				if sw == sr || !inScope[sw] || !inScope[sr] {
					continue
				}
				if !kept[stepEdge{sw, sr}] && !reachable(sw, sr) {
					e.Ordered = false
				}
			}
		}
	}
	for e := range kept {
		if !slices.Contains(d.RunAfter[e.to], e.from) {
			d.RunAfter[e.to] = append(d.RunAfter[e.to], e.from)
		}
	}
	for k := range d.RunAfter {
		slices.Sort(d.RunAfter[k])
	}
}

// SameStepConflict is one target reading, inside a single step, what another target of
// that same step writes, with nothing sequencing the two.
//
// This is the one overlap magus must refuse rather than schedule around. Across steps the
// engine derives writer-before-reader order itself (DeriveTargetOrder); within one step
// the sequencing belongs to the composing body, and ctx.needs is the only thing that can
// express it.
type SameStepConflict struct {
	// Step is the node key of the step whose chain runs both targets.
	Step string
	// Writer and Reader are node keys (DepKey).
	Writer, Reader string
	// WriteGlob and ReadGlob are the overlapping pair of declared globs, workspace-rooted.
	WriteGlob, ReadGlob string
}

// SameProject reports that reader and writer belong to one project, so the fix is a
// ctx.needs in the file that composes both. A cross-project pair is real too, but its
// sequencing may belong to another project's magusfile, so it advises rather than
// refuses (see SameStepConflicts.Refusal).
func (c SameStepConflict) SameProject() bool { return projectOf(c.Writer) == projectOf(c.Reader) }

// SameStepConflicts is one plan's unordered pairs, sorted; the refusal and the advice are
// two readings of the same list.
type SameStepConflicts []SameStepConflict

// OverlapWitness decides whether a write glob and a read glob meet on a path that
// actually exists. Glob-against-glob intersection is conservative on purpose, since
// over-ordering is cheap, but a refusal has to stand on a file: "reads **/MAGUS.md
// alongside a writer of cmd/magus/completions/*" intersects as globs and never as paths.
// nil means no witness is required, which keeps fixtures with invented globs testable.
//
// ignore is the reader's own pruned directory names beyond the ones every walk prunes
// (a spell's build dirs). A pattern read never reaches a file under one of them when
// the key is hashed (expandSources prunes the walk by name), so a file there witnesses
// nothing for a pattern; an exact read names its file deliberately and is hashed by
// stat, so it does. What orders and what hashes agree.
type OverlapWitness func(write, read string, ignore []string) bool

// WorkspaceOverlapWitness answers from the workspace tree. The tree is walked once,
// pruned by the same directory names the hasher prunes (isIgnoreDir: VCS and magus
// metadata, gen, vendor, node_modules, ...), since a file under those is one no pattern
// glob ever hashes; each write glob is then matched over that list once, memoized,
// because one writer meets many readers in a plan. An exact read is answered by a stat
// instead, which is how the hasher reaches it too. Safe for concurrent callers.
//
// An unreadable or absent root reads as no witness anywhere: a refusal over a glob that
// matched nothing on disk is the guess this exists to rule out.
func WorkspaceOverlapWitness(root string) OverlapWitness {
	var (
		mu    sync.Mutex
		files []string
		once  sync.Once
		hits  = map[string][]string{}
	)
	tree := os.DirFS(root)
	walk := func() {
		_ = fs.WalkDir(tree, ".", func(p string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				// An entry the walk cannot read is no witness, and must not end the walk
				// for the entries it can.
				return nil //nolint:nilerr // see above
			case d.IsDir():
				if p != "." && isIgnoreDir(d.Name(), nil) {
					return fs.SkipDir
				}
			case d.Type().IsRegular():
				files = append(files, p)
			}
			return nil
		})
	}
	return func(write, read string, ignore []string) bool {
		if !strings.ContainsAny(read, "*?[{") {
			if ok, err := doublestar.Match(write, read); !ok || err != nil {
				return false
			}
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(read)))
			return err == nil && info.Mode().IsRegular()
		}
		once.Do(walk)
		mu.Lock()
		paths, seen := hits[write]
		if !seen {
			for _, f := range files {
				if ok, err := doublestar.Match(write, f); ok && err == nil {
					paths = append(paths, f)
				}
			}
			hits[write] = paths
		}
		mu.Unlock()
		for _, p := range paths {
			if underIgnoredDir(p, ignore) {
				continue
			}
			if ok, err := doublestar.Match(read, p); ok && err == nil {
				return true
			}
		}
		return false
	}
}

// FindSameStepConflicts reports the same-step pairs no schedule can order: both sides
// declared explicitly, the writer's writes reaching the reader's reads on a path the
// witness confirms, and no ctx.needs path between the two in either direction.
//
// Baseline fallbacks are excluded on either side. A fallback footprint is a whole-project
// over-approximation, so an overlap through one is a guess, and refusing a run over a
// guess costs more than the stale read it would prevent.
//
// A needs path in EITHER direction clears the pair. Reader-after-writer is the fix this
// reports. Writer-after-reader is a sequencing the author wrote down: the reader reads
// what was there beforehand, which is a staleness question rather than a plan that cannot
// be scheduled.
//
// Deterministic: results are sorted by step, then writer, then reader.
func FindSameStepConflicts(nodes []TargetNode, witness OverlapWitness) SameStepConflicts {
	order := newNodeOrder(nodes)
	var out SameStepConflicts
	for w := range nodes {
		for r := range nodes {
			if w == r || nodes[w].Key() == nodes[r].Key() {
				continue
			}
			if !nodes[w].DeclaredWrites || !nodes[r].DeclaredReads {
				continue
			}
			step, ok := nodes[w].sharedStep(nodes[r])
			if !ok {
				continue
			}
			write, read, ok := nodes[w].overlap(nodes[r])
			if !ok {
				continue
			}
			if witness != nil && !witness(write, read, nodes[r].IgnoreDirs) {
				continue
			}
			if order.runsAfter(nodes[r].Key(), nodes[w].Key()) || order.runsAfter(nodes[w].Key(), nodes[r].Key()) {
				continue
			}
			out = append(out, SameStepConflict{
				Step: step, Writer: nodes[w].Key(), Reader: nodes[r].Key(),
				WriteGlob: write, ReadGlob: read,
			})
		}
	}
	// Joined on a byte no key carries, the way cycleBackEdge keys a pair, so fields
	// cannot transpose across the boundary.
	slices.SortFunc(out, func(a, b SameStepConflict) int {
		return strings.Compare(a.Step+"\x00"+a.Writer+"\x00"+a.Reader, b.Step+"\x00"+b.Writer+"\x00"+b.Reader)
	})
	return out
}

// nodeOrder answers, over one collection of nodes, whether one node's own work runs
// after another has completed. Every answer is a lookup in two sets computed once per
// node and memoized, so a plan's N^2 pairs cost N set constructions rather than a walk
// each.
type nodeOrder struct {
	byKey     map[string]TargetNode
	composers map[string][]string
	// done holds, per key, everything complete once the key has completed: the key, its
	// members transitively, and whatever preceded each of them.
	done map[string]map[string]bool
	// preceded holds, per key, everything complete before the key STARTS.
	preceded map[string]map[string]bool
}

func newNodeOrder(nodes []TargetNode) *nodeOrder {
	o := &nodeOrder{
		byKey:     make(map[string]TargetNode, len(nodes)),
		composers: map[string][]string{},
		done:      map[string]map[string]bool{},
		preceded:  map[string]map[string]bool{},
	}
	for _, n := range nodes {
		o.byKey[n.Key()] = n
		for _, member := range n.Needs.members() {
			o.composers[member] = append(o.composers[member], n.Key())
		}
	}
	return o
}

// runsAfter reports whether later's own work runs after earlier has completed, so the
// body's sequencing already puts earlier first: earlier precedes later, or completes
// with one of later's own members.
func (o *nodeOrder) runsAfter(later, earlier string) bool {
	if o.precededBy(later)[earlier] {
		return true
	}
	for _, m := range o.byKey[later].Needs.members() {
		if o.doneBy(m)[earlier] {
			return true
		}
	}
	return false
}

// doneBy is everything complete once key has completed. A key under construction (a
// chain the loader somehow admitted with a cycle in it) reads as its partial set, which
// orders less rather than more.
func (o *nodeOrder) doneBy(key string) map[string]bool {
	if s, ok := o.done[key]; ok {
		return s
	}
	s := map[string]bool{key: true}
	o.done[key] = s
	for _, m := range o.byKey[key].Needs.members() {
		maps.Copy(s, o.doneBy(m))
	}
	maps.Copy(s, o.precededBy(key))
	return s
}

// precededBy is everything complete before key starts. A node is dispatched by
// whichever composer reaches it first and runs once, so only what EVERY composer puts
// before it is certain: the intersection, over its composers, of what that composer's
// earlier calls completed plus what preceded the composer itself. A composer's other
// members never count; those are key's siblings, fanned out unordered beside it.
func (o *nodeOrder) precededBy(key string) map[string]bool {
	if s, ok := o.preceded[key]; ok {
		return s
	}
	o.preceded[key] = map[string]bool{}
	var s map[string]bool
	for _, c := range o.composers[key] {
		via := map[string]bool{}
		for _, m := range o.byKey[c].Needs.before(key) {
			maps.Copy(via, o.doneBy(m))
		}
		maps.Copy(via, o.precededBy(c))
		if s == nil {
			s = via
			continue
		}
		for k := range s {
			if !via[k] {
				delete(s, k)
			}
		}
	}
	if s == nil {
		s = map[string]bool{}
	}
	o.preceded[key] = s
	return s
}

// Refusal is the MGS4008 refusal for the SAME-project conflicts, or nil for none. It
// names the reader, the writer, the globs that overlap and both fixes, because a reader
// meeting this has to change a declaration and the message is where the choice is made.
//
// Same-project only: there the fix is one ctx.needs in the file that composes both, and
// nothing else can be meant. A cross-project pair reaches the same step through another
// project's chain, whose sequencing that project's author owns, and this tree's routing
// indexes read each other by design, so those pairs go to Advice instead.
//
// Only the first conflict is spelled out. The rest are counted: they are usually the same
// authoring mistake seen from several members, and a wall of near-identical sentences
// buries the one worth reading.
func (cs SameStepConflicts) Refusal() error {
	own := cs.within(true)
	if len(own) == 0 {
		return nil
	}
	c := own[0]
	more := ""
	if n := len(own) - 1; n > 0 {
		more = fmt.Sprintf(" %d further pair(s) in this run overlap the same way.", n)
	}
	return types.DiagnosticErrorf(types.UnorderedSameStepWrite,
		"refusing to run %s: %s reads %q, which %s writes as %q, and nothing orders the two."+
			" Both run inside %s's ctx.needs chain, with no needs path between them, so the reader"+
			" can start before the writer finishes and can wedge the run waiting for a slot the"+
			" writer needs. Declare ctx.needs(%s) in %s so it runs after the writer, or narrow %s's"+
			" ctx.readsFiles so it no longer matches what the writer produces.%s",
		DisplayNodeKey(c.Step), DisplayNodeKey(c.Reader), c.ReadGlob, DisplayNodeKey(c.Writer), c.WriteGlob,
		DisplayNodeKey(c.Step), targetOf(c.Writer), DisplayNodeKey(c.Reader), DisplayNodeKey(c.Reader), more)
}

// Advice is the one-line notice for the CROSS-project conflicts, or "" for none: the
// same overlap, reported rather than refused, with the first pair named and the rest
// counted. `magus doctor` lists every one under the same code.
func (cs SameStepConflicts) Advice() string {
	cross := cs.within(false)
	if len(cross) == 0 {
		return ""
	}
	c := cross[0]
	more := ""
	if n := len(cross) - 1; n > 0 {
		more = fmt.Sprintf("; %d more such pair(s)", n)
	}
	return fmt.Sprintf("[%s] %s reads %q inside %s's chain, which %s writes as %q, and nothing orders the two across projects%s; `magus doctor` lists them (see %s)",
		types.UnorderedSameStepWrite, DisplayNodeKey(c.Reader), c.ReadGlob, DisplayNodeKey(c.Step),
		DisplayNodeKey(c.Writer), c.WriteGlob, more, types.CodeURL(types.UnorderedSameStepWrite))
}

// within selects the pairs whose SameProject answer is same.
func (cs SameStepConflicts) within(same bool) SameStepConflicts {
	var out SameStepConflicts
	for _, c := range cs {
		if c.SameProject() == same {
			out = append(out, c)
		}
	}
	return out
}

// targetOf is a node key's target half, which is what a ctx.needs call names.
func targetOf(key string) string {
	_, target, ok := strings.Cut(key, nodeKeySep)
	if !ok {
		return key
	}
	return target
}

// projectOf is a node key's project half.
func projectOf(key string) string {
	project, _, _ := strings.Cut(key, nodeKeySep)
	return project
}

// DeclaredNodes builds the nodes of one composer's ctx.needs closure: the composer itself
// and every target it composes, transitively, with the step key of the composer.
//
// It reads DECLARED footprints only, where a batch's own node collection also carries a
// project baseline for a target that declares none. That is not a second opinion about
// what a target touches: FindSameStepConflicts ignores a baseline on either side, so the
// two collections agree on every pair either can report, and a caller with no batch in
// hand (doctor) needs no engine to ask the question.
//
// lookup resolves a cross-project chain step and may return nil, in which case that step
// and everything under it contribute nothing rather than a guess.
func DeclaredNodes(p *types.Project, composer string, lookup func(path string) *types.Project) []TargetNode {
	if p == nil {
		return nil
	}
	step := DepKey(p.Path, composer)
	var nodes []TargetNode
	_ = types.WalkChain(p, composer, lookup, func(v types.ChainVisit) error {
		nodes = append(nodes, DeclaredNode(v.Project, v.Target, step, lookup))
		return nil
	})
	return nodes
}

// DeclaredNode is one target's node as its declarations describe it, run inside step:
// workspace-rooted reads, writes and in-place updates, and its ctx.needs calls resolved
// through lookup.
func DeclaredNode(proj *types.Project, target, step string, lookup func(path string) *types.Project) TargetNode {
	updates := make([]string, 0, len(proj.TargetUpdates[target]))
	for _, ref := range proj.TargetUpdates[target] {
		updates = append(updates, types.RootGlob(ref.Project, ref.Glob))
	}
	var reads []string
	declaredReads := len(proj.TargetInputs[target]) > 0
	if declaredReads {
		for _, ref := range proj.TargetInputs[target] {
			reads = append(reads, types.RootGlob(ref.Project, ref.Glob))
		}
		reads = append(reads, updates...)
	}
	writes := make([]string, 0, len(proj.TargetOutputs[target])+len(updates))
	for _, ref := range proj.TargetOutputs[target] {
		writes = append(writes, types.RootGlob(ref.Project, ref.Glob))
	}
	writes = append(writes, updates...)
	return TargetNode{
		Project: proj.Path, Target: target, Steps: []string{step},
		Reads: reads, Writes: writes,
		DeclaredReads:  declaredReads,
		DeclaredWrites: len(proj.TargetOutputs[target]) > 0 || len(updates) > 0,
		IgnoreDirs:     prunedDirs(proj),
		Needs:          ChainCalls(proj, target, lookup),
	}
}

// prunedDirs is the directory-name set the project's source walk prunes: the core
// names every walk skips plus what each resolved spell declares, the same union
// buildStep hands the hasher. A project loaded without resolved spells (a doctor
// fixture) prunes the core set alone.
func prunedDirs(proj *types.Project) []string {
	out := slices.Clone(project.IgnoreDirs)
	for _, sp := range proj.ResolvedSpells {
		for _, d := range sp.IgnoreDirs() {
			if !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	return out
}

// lookupOwner resolves the project a chain step runs in: the composer's own for a local
// step, lookup's answer for a cross-project one, nil when there is none.
func lookupOwner(proj *types.Project, cs types.ChainStep, lookup func(path string) *types.Project) *types.Project {
	if cs.Project == "" || cs.Project == proj.Path {
		return proj
	}
	if lookup == nil {
		return nil
	}
	return lookup(cs.Project)
}

// ChainCalls groups target's chain by the ctx.needs call that named each step, in body
// order, as the node keys the steps resolve to. A step lookup cannot resolve neither
// orders nor is ordered, and a call left with no member is dropped.
func ChainCalls(proj *types.Project, target string, lookup func(path string) *types.Project) Calls {
	var out Calls
	var call Call
	index := 0
	flush := func() {
		if len(call) > 0 {
			out = append(out, call)
		}
		call = nil
	}
	for _, cs := range proj.TargetChains[target] {
		owner := lookupOwner(proj, cs, lookup)
		if owner == nil {
			continue
		}
		if cs.CallIndex != index {
			flush()
			index = cs.CallIndex
		}
		call = append(call, DepKey(owner.Path, cs.Target))
	}
	flush()
	return out
}
