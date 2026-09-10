package cache

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

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
	// Needs are the node keys this target dispatches with ctx.needs, directly. They are
	// the only sequencing that exists INSIDE a step, so they are what decides whether a
	// same-step overlap is ordered or unschedulable; see FindSameStepConflicts.
	Needs []string
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
	SameStep []SameStepConflict
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
			// Same-step pairs are the body's own ctx.needs sequencing, which stays
			// untouched; only cross-step order is magus's to derive. A same-step pair
			// the body does NOT sequence is already in SameStep above, for the caller
			// to refuse over.
			if _, shared := sharedStep(nodes[w], nodes[r]); shared {
				continue
			}
			if !footprintsIntersect(nodes[w], nodes[r]) {
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

// footprintsIntersect reports whether w's writes can produce a path r's reads
// match. For a fallback reader, writes confined to the reader's pruned dirs are
// invisible to it and derive nothing.
func footprintsIntersect(w, r TargetNode) bool {
	_, _, ok := overlappingGlobs(w, r)
	return ok
}

// overlappingGlobs is footprintsIntersect with the witness kept: the first write glob and
// read glob that can meet. A refusal has to name them, and recomputing the pair in the
// message would be a second answer to the same question.
func overlappingGlobs(w, r TargetNode) (write, read string, ok bool) {
	for _, wg := range w.Writes {
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
		// Both consumed a literal segment; a shorter glob than the other's prefix
		// cannot match a longer path, unless it still has segments to offer.
		if i == len(as)-1 || i == len(bs)-1 {
			return i == len(as)-1 && i == len(bs)-1
		}
	}
	sa, sb := literalSuffix(as[len(as)-1]), literalSuffix(bs[len(bs)-1])
	if as[len(as)-1] == "**" || bs[len(bs)-1] == "**" {
		return true
	}
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
	// Write and Read are the overlapping pair of declared globs, workspace-rooted.
	Write, Read string
	// SameProject reports that reader and writer belong to one project, so the fix is a
	// ctx.needs in the file that composes both. A cross-project pair is real too, but its
	// sequencing may belong to another project's magusfile, so it advises rather than
	// refuses (see SameStepConflictError).
	SameProject bool
}

// OverlapWitness decides whether a write glob and a read glob meet on a path that
// actually exists. Glob-against-glob intersection is conservative on purpose, since
// over-ordering is cheap, but a refusal has to stand on a file: "reads **/MAGUS.md
// alongside a writer of cmd/magus/completions/*" intersects as globs and never as paths.
// nil means no witness is required, which keeps fixtures with invented globs testable.
type OverlapWitness func(write, read string) bool

// WorkspaceOverlapWitness answers from the workspace tree: the write glob is expanded
// once and each hit is matched against the read glob. Expansion is memoized per write
// glob because the same writer meets many readers in one plan.
func WorkspaceOverlapWitness(root string) OverlapWitness {
	tree := os.DirFS(root)
	hits := map[string][]string{}
	return func(write, read string) bool {
		paths, seen := hits[write]
		if !seen {
			// An unreadable or absent path reads as no witness: a refusal over a glob
			// that matched nothing on disk is the guess this exists to rule out.
			paths, _ = doublestar.Glob(tree, write, doublestar.WithFilesOnly(), doublestar.WithNoFollow())
			hits[write] = paths
		}
		for _, p := range paths {
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
func FindSameStepConflicts(nodes []TargetNode, witness OverlapWitness) []SameStepConflict {
	byKey := make(map[string]TargetNode, len(nodes))
	for _, n := range nodes {
		byKey[n.Key()] = n
	}
	var out []SameStepConflict
	for w := range nodes {
		for r := range nodes {
			if w == r || nodes[w].Key() == nodes[r].Key() {
				continue
			}
			if !nodes[w].DeclaredWrites || !nodes[r].DeclaredReads {
				continue
			}
			step, ok := sharedStep(nodes[w], nodes[r])
			if !ok {
				continue
			}
			write, read, ok := overlappingGlobs(nodes[w], nodes[r])
			if !ok {
				continue
			}
			if witness != nil && !witness(write, read) {
				continue
			}
			if needsPath(byKey, nodes[r].Key(), nodes[w].Key()) || needsPath(byKey, nodes[w].Key(), nodes[r].Key()) {
				continue
			}
			out = append(out, SameStepConflict{
				Step: step, Writer: nodes[w].Key(), Reader: nodes[r].Key(),
				Write: write, Read: read,
				SameProject: nodes[w].Project == nodes[r].Project,
			})
		}
	}
	slices.SortFunc(out, func(a, b SameStepConflict) int {
		return strings.Compare(a.Step+a.Writer+a.Reader, b.Step+b.Writer+b.Reader)
	})
	return out
}

// sharedStep returns the first step key (in a's order) that runs both nodes.
func sharedStep(a, b TargetNode) (string, bool) {
	for _, s := range a.Steps {
		if slices.Contains(b.Steps, s) {
			return s, true
		}
	}
	return "", false
}

// needsPath reports whether from reaches to by following ctx.needs edges, so the schedule
// already puts to first. Bounded by the node count, which is what keeps a chain the loader
// somehow admitted with a cycle in it from spinning here.
func needsPath(byKey map[string]TargetNode, from, to string) bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for _, next := range byKey[k].Needs {
			if next == to {
				return true
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// SameStepConflictError is the MGS4008 refusal for the SAME-project conflicts, or nil
// for none. It names the reader, the writer, the globs that overlap and both fixes,
// because a reader meeting this has to change a declaration and the message is where
// the choice is made.
//
// Same-project only: there the fix is one ctx.needs in the file that composes both, and
// nothing else can be meant. A cross-project pair reaches the same step through another
// project's chain, whose sequencing that project's author owns, and this tree's routing
// indexes read each other by design, so those pairs go to SameStepAdvice instead.
//
// Only the first conflict is spelled out. The rest are counted: they are usually the same
// authoring mistake seen from several members, and a wall of near-identical sentences
// buries the one worth reading.
func SameStepConflictError(conflicts []SameStepConflict) error {
	var own []SameStepConflict
	for _, c := range conflicts {
		if c.SameProject {
			own = append(own, c)
		}
	}
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
		displayKey(c.Step), displayKey(c.Reader), c.Read, displayKey(c.Writer), c.Write,
		displayKey(c.Step), targetOf(c.Writer), displayKey(c.Reader), displayKey(c.Reader), more)
}

// SameStepAdvice is the one-line notice for the CROSS-project conflicts, or "" for none:
// the same overlap, reported rather than refused, with the first pair named and the rest
// counted. `magus doctor` lists every one under the same code.
func SameStepAdvice(conflicts []SameStepConflict) string {
	var cross []SameStepConflict
	for _, c := range conflicts {
		if !c.SameProject {
			cross = append(cross, c)
		}
	}
	if len(cross) == 0 {
		return ""
	}
	c := cross[0]
	more := ""
	if n := len(cross) - 1; n > 0 {
		more = fmt.Sprintf("; %d more such pair(s)", n)
	}
	return fmt.Sprintf("[%s] %s reads %q inside %s's chain, which %s writes as %q, and nothing orders the two across projects%s; `magus doctor` lists them (see %s)",
		types.UnorderedSameStepWrite, displayKey(c.Reader), c.Read, displayKey(c.Step),
		displayKey(c.Writer), c.Write, more, types.CodeURL(types.UnorderedSameStepWrite))
}

// targetOf is a node key's target half, which is what a ctx.needs call names.
func targetOf(key string) string {
	_, target, ok := strings.Cut(key, nodeKeySep)
	if !ok {
		return key
	}
	return target
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
	byKey := map[string]*TargetNode{}
	var order []string

	var walk func(proj *types.Project, target string)
	walk = func(proj *types.Project, target string) {
		if proj == nil {
			return
		}
		key := DepKey(proj.Path, target)
		if _, ok := byKey[key]; ok {
			return
		}

		updates := make([]string, 0, len(proj.TargetUpdates[target]))
		for _, ref := range proj.TargetUpdates[target] {
			updates = append(updates, rootedGlob(proj, ref.Project, ref.Glob))
		}
		var reads []string
		declaredReads := len(proj.TargetInputs[target]) > 0
		if declaredReads {
			for _, ref := range proj.TargetInputs[target] {
				reads = append(reads, rootedGlob(proj, ref.Project, ref.Glob))
			}
			reads = append(reads, updates...)
		}
		writes := make([]string, 0, len(proj.TargetOutputs[target])+len(updates))
		for _, ref := range proj.TargetOutputs[target] {
			writes = append(writes, rootedGlob(proj, ref.Project, ref.Glob))
		}
		writes = append(writes, updates...)

		node := &TargetNode{
			Project: proj.Path, Target: target, Steps: []string{step},
			Reads: reads, Writes: writes,
			DeclaredReads:  declaredReads,
			DeclaredWrites: len(proj.TargetOutputs[target]) > 0 || len(updates) > 0,
		}
		byKey[key] = node
		order = append(order, key)
		chain := proj.TargetChains[target]
		keyOf := func(cs types.ChainStep) (string, bool) {
			owner := lookupOwner(proj, cs, lookup)
			if owner == nil {
				return "", false
			}
			return DepKey(owner.Path, cs.Target), true
		}
		for _, cs := range chain {
			memberKey, ok := keyOf(cs)
			if !ok {
				continue
			}
			node.Needs = append(node.Needs, memberKey)
			walk(lookupOwner(proj, cs, lookup), cs.Target)
		}
		for memberKey, earlier := range StageNeeds(chain, keyOf) {
			// Every member walked above exists; a step keyOf rejected has no entry.
			member := byKey[memberKey]
			for _, e := range earlier {
				if !slices.Contains(member.Needs, e) {
					member.Needs = append(member.Needs, e)
				}
			}
		}
	}
	walk(p, composer)

	nodes := make([]TargetNode, 0, len(order))
	for _, k := range order {
		nodes = append(nodes, *byKey[k])
	}
	return nodes
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

// StageNeeds lists, per chain member, the members of the earlier ctx.needs calls in the
// same body. One call fans its arguments out unordered and returns when all of them have
// run, so a later stage is ordered after every earlier one by the composer itself; a
// collector records that as needs edges on the member, which is the form needsPath
// already reads every other ordering in. keyOf names a step's node and says no for a
// step that resolves to nothing, which then neither orders nor is ordered.
//
// The edges land on the member's node, shared with every composer that reaches it. A
// target one composer runs in a later stage so carries that ordering into another
// composer that fans it out with the same writer in one call, and that pair goes
// unreported; both composers have to be scheduled in one invocation for it to matter.
func StageNeeds(chain []types.ChainStep, keyOf func(types.ChainStep) (string, bool)) map[string][]string {
	out := map[string][]string{}
	var earlier, current []string
	stage := 0
	for _, cs := range chain {
		key, ok := keyOf(cs)
		if !ok {
			continue
		}
		if cs.Stage != stage {
			earlier = append(earlier, current...)
			current = nil
			stage = cs.Stage
		}
		current = append(current, key)
		if len(earlier) > 0 {
			out[key] = slices.Clone(earlier)
		}
	}
	return out
}

// rootedGlob resolves a declared ref to a workspace-rooted glob. A ref with no project of
// its own belongs to the project that declared it.
func rootedGlob(p *types.Project, refProject, glob string) string {
	if refProject == "" {
		return types.RootGlob(p.Path, glob)
	}
	return types.RootGlob(refProject, glob)
}
