package changeset

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egladman/magus/types"
)

// OrderInput is everything OrderHunks needs. The caller resolves symbols, uses and
// implementations from its index; ordering reads no files and asks no graph.
type OrderInput struct {
	Files []OrderFile
	// Sites are uses of changed symbols, at lines of the new file.
	Sites []OrderSite
	// Implements pairs changed implementers with the interfaces they satisfy.
	Implements []OrderLink
	// IsTest reports whether a path holds tests. Nil means no path does.
	IsTest func(path string) bool
}

// OrderFile is one changed file and the hunks of its patch.
type OrderFile struct {
	Path string
	// Generated marks declared generated output, which is read after the source.
	Generated bool
	// Moved marks a file whose working tree no longer matches the range head, so the
	// index's line numbers do not describe its hunks. Its hunks are unranked.
	Moved bool
	Hunks []types.DiffHunk
	// Symbols are the file's changed symbols; hunk Symbols refer to their IDs.
	Symbols []types.DiffSymbol
	// Reach is the number of files that reference the file's symbols, the ranking key after
	// group size.
	Reach int
}

// OrderSite is a use of a changed symbol at Path:Line in the new file.
type OrderSite struct {
	Symbol string
	Path   string
	Line   int
}

// OrderLink says the symbol Implementer implements the symbol Interface.
type OrderLink struct {
	Implementer string
	Interface   string
}

type orderEdgeKind int

// The kinds are in preference order: when two edges join the same pair of hunks, the
// earlier kind explains the placement.
const (
	orderEdgeUses orderEdgeKind = iota
	orderEdgeImplements
	orderEdgeContinues
)

type orderNode struct {
	ref   types.DiffHunkRef
	path  string
	file  int
	hunk  types.DiffHunk
	test  bool
	reach int
}

// orderEdge says hunk definer comes before hunk user.
type orderEdge struct {
	definer, user int
	symbol        string
	kind          orderEdgeKind
}

// orderPlacement is one strongly connected set of hunks and the edge that put it where it is.
type orderPlacement struct {
	nodes []int
	// how is "start", "after" (other is the definer it follows) or "before" (other is the
	// later user it precedes). The generated and unranked groups carry fixed instead.
	how    string
	other  int
	symbol string
	kind   orderEdgeKind
	cycle  []string
	fixed  *types.DiffWhy
}

type orderGroup struct {
	kind       types.DiffGroupKind
	placements []orderPlacement
	size       int
	reach      int
	first      int
}

type orderBuilder struct {
	in    OrderInput
	files []OrderFile
	nodes []orderNode
	bare  []string

	labels    map[string]string
	definedBy map[string][]int
	defs      []map[string]bool
	edges     []orderEdge
	rep       map[int]int
	stepOf    map[int]int
}

// OrderHunks arranges the hunks of in into groups of steps: a definition before its uses, an
// interface before its implementations, code before its tests. The result is a pure function
// of in, and the same input in any order gives the same output. Every hunk lands in exactly
// one group; Count on the result is computed by walking the groups it holds.
func OrderHunks(in OrderInput) types.DiffOrder {
	b := &orderBuilder{in: in, labels: map[string]string{}, definedBy: map[string][]int{}, rep: map[int]int{}, stepOf: map[int]int{}}
	b.normalize()

	var generated, unranked []orderPlacement
	var eligible []int
	for id, n := range b.nodes {
		f := b.files[n.file]
		switch {
		case f.Generated:
			generated = append(generated, fixedOrderPlacement(id, types.DiffWhy{
				Relation: types.DiffWhyGenerated,
				Text:     "generated output; its source carries the review",
			}))
		case f.Moved:
			unranked = append(unranked, fixedOrderPlacement(id, unrankedOrderWhy("the working tree no longer matches the range head for this file")))
		default:
			eligible = append(eligible, id)
		}
	}

	for _, id := range eligible {
		for _, s := range b.nodes[id].hunk.Symbols {
			if ids := b.definedBy[s]; len(ids) == 0 || ids[len(ids)-1] != id {
				b.definedBy[s] = append(b.definedBy[s], id)
			}
		}
	}
	b.buildEdges(eligible)

	linked := map[int]bool{}
	for _, e := range b.edges {
		linked[e.definer] = true
		linked[e.user] = true
	}
	var ranked []int
	for _, id := range eligible {
		if linked[id] {
			ranked = append(ranked, id)
			continue
		}
		unranked = append(unranked, fixedOrderPlacement(id, unrankedOrderWhy(b.unlinkedReason(id))))
	}

	var connected []orderGroup
	b.condense(ranked)
	for _, members := range b.components(ranked) {
		connected = append(connected, b.groupOf(members))
	}
	sort.SliceStable(connected, func(i, j int) bool {
		a, c := connected[i], connected[j]
		if a.size != c.size {
			return a.size > c.size
		}
		if a.reach != c.reach {
			return a.reach > c.reach
		}
		return a.first < c.first
	})

	groups := connected
	if len(generated) > 0 {
		groups = append(groups, orderGroup{kind: types.DiffGroupGenerated, placements: generated, size: len(generated)})
	}
	if len(unranked) > 0 {
		groups = append(groups, orderGroup{kind: types.DiffGroupUnranked, placements: unranked, size: len(unranked)})
	}
	return b.render(groups)
}

// normalize copies the input into a canonical order, so shuffling files or hunks cannot
// change the result: nodes are numbered by path, then hunk index.
func (b *orderBuilder) normalize() {
	b.files = append([]OrderFile(nil), b.in.Files...)
	sort.SliceStable(b.files, func(i, j int) bool { return b.files[i].Path < b.files[j].Path })
	for fi := range b.files {
		f := &b.files[fi]
		f.Hunks = append([]types.DiffHunk(nil), f.Hunks...)
		sort.SliceStable(f.Hunks, func(i, j int) bool { return f.Hunks[i].Index < f.Hunks[j].Index })
		if len(f.Hunks) == 0 && !containsString(b.bare, f.Path) {
			b.bare = append(b.bare, f.Path)
		}
		for _, s := range f.Symbols {
			if _, ok := b.labels[s.ID]; !ok {
				b.labels[s.ID] = orderSymbolName(s)
			}
		}
		isTest := b.in.IsTest != nil && b.in.IsTest(f.Path)
		for _, h := range f.Hunks {
			b.nodes = append(b.nodes, orderNode{
				ref:   types.DiffHunkRef{Path: f.Path, Index: h.Index, Digest: h.Digest},
				path:  f.Path,
				file:  fi,
				hunk:  h,
				test:  isTest,
				reach: f.Reach,
			})
		}
	}
}

func (b *orderBuilder) buildEdges(eligible []int) {
	b.defs = make([]map[string]bool, len(b.nodes))
	for s, ids := range b.definedBy {
		for _, id := range ids {
			if b.defs[id] == nil {
				b.defs[id] = map[string]bool{}
			}
			b.defs[id][s] = true
		}
	}
	covering := map[string][]int{}
	for _, id := range eligible {
		if b.nodes[id].hunk.NewCount > 0 {
			covering[b.nodes[id].path] = append(covering[b.nodes[id].path], id)
		}
	}

	seen := map[orderEdge]bool{}
	add := func(e orderEdge) {
		if e.definer == e.user || seen[e] {
			return
		}
		seen[e] = true
		b.edges = append(b.edges, e)
	}

	for _, s := range b.in.Sites {
		definers := b.definedBy[s.Symbol]
		if len(definers) == 0 {
			continue
		}
		for _, user := range covering[s.Path] {
			h := b.nodes[user].hunk
			if s.Line < h.NewStart || s.Line > h.NewStart+h.NewCount-1 {
				continue
			}
			// A hunk that itself defines the symbol is the definition, not a use of it.
			if b.defs[user][s.Symbol] {
				continue
			}
			for _, definer := range definers {
				add(orderEdge{definer: definer, user: user, symbol: s.Symbol, kind: orderEdgeUses})
			}
		}
	}
	for _, l := range b.in.Implements {
		for _, iface := range b.definedBy[l.Interface] {
			for _, impl := range b.definedBy[l.Implementer] {
				add(orderEdge{definer: iface, user: impl, symbol: l.Interface, kind: orderEdgeImplements})
			}
		}
	}
	for s, ids := range b.definedBy {
		for k := 1; k < len(ids); k++ {
			add(orderEdge{definer: ids[k-1], user: ids[k], symbol: s, kind: orderEdgeContinues})
		}
	}

	sort.Slice(b.edges, func(i, j int) bool {
		x, y := b.edges[i], b.edges[j]
		switch {
		case x.definer != y.definer:
			return x.definer < y.definer
		case x.user != y.user:
			return x.user < y.user
		case x.kind != y.kind:
			return x.kind < y.kind
		}
		return x.symbol < y.symbol
	})
}

func (b *orderBuilder) unlinkedReason(id int) string {
	n := b.nodes[id]
	switch {
	case len(b.files[n.file].Symbols) == 0 && len(n.hunk.Symbols) == 0:
		return "no symbol index covers this file"
	case len(n.hunk.Symbols) == 0:
		return "defines no changed symbol and uses none"
	}
	return "unlinked: no other changed hunk uses, implements or continues what it changes"
}

// condense assigns every ranked hunk the smallest hunk number of its strongly connected
// component, so hunks that use each other collapse into one step.
func (b *orderBuilder) condense(ranked []int) {
	next := map[int][]int{}
	for _, e := range b.edges {
		next[e.definer] = append(next[e.definer], e.user)
	}
	index := map[int]int{}
	low := map[int]int{}
	onStack := map[int]bool{}
	var stack []int
	counter := 0

	var visit func(v int)
	visit = func(v int) {
		index[v], low[v] = counter, counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range next[v] {
			if _, ok := index[w]; !ok {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] != index[v] {
			return
		}
		var members []int
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			members = append(members, w)
			if w == v {
				break
			}
		}
		smallest := members[0]
		for _, m := range members {
			smallest = min(smallest, m)
		}
		for _, m := range members {
			b.rep[m] = smallest
		}
	}
	for _, v := range ranked {
		if _, ok := index[v]; !ok {
			visit(v)
		}
	}
}

// components groups ranked hunks that are joined by any edge, each group ascending and the
// groups in order of their first hunk.
func (b *orderBuilder) components(ranked []int) [][]int {
	adj := map[int][]int{}
	for _, e := range b.edges {
		adj[e.definer] = append(adj[e.definer], e.user)
		adj[e.user] = append(adj[e.user], e.definer)
	}
	seen := map[int]bool{}
	var out [][]int
	for _, seed := range ranked {
		if seen[seed] {
			continue
		}
		seen[seed] = true
		members := []int{seed}
		for at := 0; at < len(members); at++ {
			for _, m := range adj[members[at]] {
				if !seen[m] {
					seen[m] = true
					members = append(members, m)
				}
			}
		}
		sort.Ints(members)
		out = append(out, members)
	}
	return out
}

// groupOf orders one connected set of hunks. A step waits for every step it uses; among
// steps that are ready, code goes before tests and hunk order breaks ties.
func (b *orderBuilder) groupOf(members []int) orderGroup {
	inGroup := map[int]bool{}
	sets := map[int][]int{}
	reach := 0
	for _, m := range members {
		inGroup[m] = true
		sets[b.rep[m]] = append(sets[b.rep[m]], m)
		reach = max(reach, b.nodes[m].reach)
	}
	testOnly := map[int]bool{}
	indeg := map[int]int{}
	succ := map[int][]int{}
	for rep, g := range sets {
		indeg[rep] = 0
		all := true
		for _, m := range g {
			all = all && b.nodes[m].test
		}
		testOnly[rep] = all
	}
	pairs := map[[2]int]bool{}
	var local []orderEdge
	for _, e := range b.edges {
		if !inGroup[e.definer] {
			continue
		}
		local = append(local, e)
		a, c := b.rep[e.definer], b.rep[e.user]
		if a == c || pairs[[2]int{a, c}] {
			continue
		}
		pairs[[2]int{a, c}] = true
		succ[a] = append(succ[a], c)
		indeg[c]++
	}

	var ready, sequence []int
	for rep, n := range indeg {
		if n == 0 {
			ready = append(ready, rep)
		}
	}
	position := map[int]int{}
	for len(ready) > 0 {
		best := 0
		for i, r := range ready {
			if placedBefore(r, ready[best], testOnly) {
				best = i
			}
		}
		rep := ready[best]
		ready = append(ready[:best], ready[best+1:]...)
		position[rep] = len(sequence)
		sequence = append(sequence, rep)
		for _, s := range succ[rep] {
			if indeg[s]--; indeg[s] == 0 {
				ready = append(ready, s)
			}
		}
	}

	incoming := map[int][]orderEdge{}
	outgoing := map[int][]orderEdge{}
	cycles := map[int][]string{}
	for _, e := range local {
		a, c := b.rep[e.definer], b.rep[e.user]
		if a == c {
			label := b.labelOf(e.symbol)
			if !containsString(cycles[a], label) {
				cycles[a] = append(cycles[a], label)
			}
			continue
		}
		incoming[c] = append(incoming[c], e)
		outgoing[a] = append(outgoing[a], e)
	}

	placements := make([]orderPlacement, 0, len(sequence))
	for k, rep := range sequence {
		p := orderPlacement{nodes: sets[rep], how: "start", other: -1, cycle: cycles[rep]}
		sort.Strings(p.cycle)
		best := -1
		for _, e := range incoming[rep] {
			if at := position[b.rep[e.definer]]; at > best {
				best = at
				p.how, p.other, p.symbol, p.kind = "after", e.definer, e.symbol, e.kind
			}
		}
		if p.how == "start" && k > 0 {
			soonest := len(sequence)
			for _, e := range outgoing[rep] {
				if at := position[b.rep[e.user]]; at < soonest {
					soonest = at
					p.how, p.other, p.symbol, p.kind = "before", e.user, e.symbol, e.kind
				}
			}
		}
		placements = append(placements, p)
	}
	return orderGroup{kind: types.DiffGroupConnected, placements: placements, size: len(members), reach: reach, first: members[0]}
}

// placedBefore reports whether step a goes before step b: code before tests, then hunk
// order.
func placedBefore(a, b int, testOnly map[int]bool) bool {
	if testOnly[a] != testOnly[b] {
		return !testOnly[a]
	}
	return a < b
}

// render numbers the steps, merges consecutive placements from one file into one screen
// and words each hunk's relationship.
func (b *orderBuilder) render(groups []orderGroup) types.DiffOrder {
	screens := make([][][]int, len(groups))
	number := 0
	for gi, g := range groups {
		previous := ""
		for pi, p := range g.placements {
			path := b.singleFile(p)
			if pi == 0 || path == "" || path != previous {
				number++
				screens[gi] = append(screens[gi], nil)
			}
			previous = path
			last := len(screens[gi]) - 1
			screens[gi][last] = append(screens[gi][last], pi)
			for _, n := range p.nodes {
				b.stepOf[n] = number
			}
		}
	}

	order := types.DiffOrder{Groups: make([]types.DiffGroup, 0, len(groups))}
	step := 0
	for gi, g := range groups {
		out := types.DiffGroup{Kind: g.kind, Hunks: g.size, Reach: g.reach}
		if g.kind == types.DiffGroupConnected {
			out.Label = b.groupLabel(g)
		}
		for _, screen := range screens[gi] {
			step++
			ds := types.DiffStep{Number: step}
			for _, pi := range screen {
				p := g.placements[pi]
				for k, n := range p.nodes {
					ds.Hunks = append(ds.Hunks, types.DiffStepHunk{
						Hunk:  b.nodes[n].ref,
						Label: b.hunkLabel(n),
						Why:   b.why(p, k, step),
					})
				}
			}
			out.Steps = append(out.Steps, ds)
		}
		order.Groups = append(order.Groups, out)
	}
	order.Count = b.count(order.Groups)
	return order
}

func (b *orderBuilder) singleFile(p orderPlacement) string {
	path := b.nodes[p.nodes[0]].path
	for _, n := range p.nodes {
		if b.nodes[n].path != path {
			return ""
		}
	}
	return path
}

func (b *orderBuilder) groupLabel(g orderGroup) string {
	for _, p := range g.placements {
		for _, n := range p.nodes {
			if syms := b.nodes[n].hunk.Symbols; len(syms) > 0 {
				return b.labelOf(syms[0])
			}
		}
	}
	return ""
}

func (b *orderBuilder) hunkLabel(n int) string {
	if syms := b.nodes[n].hunk.Symbols; len(syms) > 0 {
		return b.labelOf(syms[0])
	}
	return b.nodes[n].hunk.Declaration
}

// why words the relationship that placed the k-th hunk of p, which sits on step here.
// Within a cycle only the first hunk carries the placement; the rest share its step.
func (b *orderBuilder) why(p orderPlacement, k, here int) types.DiffWhy {
	if p.fixed != nil {
		return *p.fixed
	}
	cycle := ""
	if len(p.nodes) > 1 {
		cycle = strings.Join(p.cycle, ", ")
		if k > 0 {
			return types.DiffWhy{
				Relation: types.DiffWhySameStep,
				Step:     here,
				Cycle:    p.cycle,
				Text:     "shares its step with the hunks it uses and is used by, through " + cycle,
			}
		}
	}
	w := types.DiffWhy{Relation: types.DiffWhyStarts, Text: "starts the group"}
	symbol := b.labelOf(p.symbol)
	switch p.how {
	case "after":
		step := b.stepOf[p.other]
		w.Step, w.Symbol = step, symbol
		switch {
		case p.kind == orderEdgeImplements:
			w.Relation = types.DiffWhyImplements
			w.Text = fmt.Sprintf("implements %s, declared %s", symbol, stepAt(step, here, "above", "in step"))
		case p.kind == orderEdgeContinues:
			w.Relation = types.DiffWhyContinues
			w.Text = fmt.Sprintf("continues %s from %s", symbol, stepAt(step, here, "above", "step"))
		case b.testOnly(p) && !b.nodes[p.other].test:
			w.Relation = types.DiffWhyTests
			w.Text = fmt.Sprintf("tests %s, defined %s", symbol, stepAt(step, here, "above", "in step"))
		default:
			w.Relation = types.DiffWhyUses
			w.Text = fmt.Sprintf("uses %s, defined %s", symbol, stepAt(step, here, "above", "in step"))
		}
	case "before":
		step := b.stepOf[p.other]
		w.Step, w.Symbol = step, symbol
		switch p.kind {
		case orderEdgeImplements:
			w.Relation = types.DiffWhyImplementedBy
			w.Text = fmt.Sprintf("declares %s, implemented %s", symbol, stepAt(step, here, "below", "in step"))
		case orderEdgeContinues:
			w.Text = fmt.Sprintf("begins %s, which continues %s", symbol, stepAt(step, here, "below", "in step"))
		default:
			w.Relation = types.DiffWhyUsedBy
			w.Text = fmt.Sprintf("defines %s, used %s", symbol, stepAt(step, here, "below", "in step"))
		}
	}
	if len(p.nodes) > 1 {
		w.Cycle = p.cycle
		w.Text += fmt.Sprintf("; %d hunks share this step because they use each other through %s", len(p.nodes), cycle)
	}
	return w
}

// stepAt names a step: the word given when it is the reader's own screen, else the step
// number after the lead-in.
func stepAt(step, here int, same, lead string) string {
	if step == here {
		return same
	}
	return fmt.Sprintf("%s %d", lead, step)
}

func (b *orderBuilder) testOnly(p orderPlacement) bool {
	for _, n := range p.nodes {
		if !b.nodes[n].test {
			return false
		}
	}
	return true
}

// count walks the groups and checks them against the input, so a hunk lost or placed twice
// shows up instead of being assumed away.
func (b *orderBuilder) count(groups []types.DiffGroup) types.DiffOrderCount {
	type key struct {
		path  string
		index int
	}
	seen := map[key]int{}
	placed := 0
	for _, g := range groups {
		for _, s := range g.Steps {
			for _, h := range s.Hunks {
				seen[key{h.Hunk.Path, h.Hunk.Index}]++
				placed++
			}
		}
	}
	count := types.DiffOrderCount{Hunks: len(b.nodes), Placed: placed, Bare: b.bare}
	reported := map[key]bool{}
	for _, n := range b.nodes {
		k := key{n.ref.Path, n.ref.Index}
		if reported[k] {
			continue
		}
		reported[k] = true
		switch times := seen[k]; {
		case times == 0:
			count.Missing = append(count.Missing, n.ref)
		case times > 1:
			count.Repeated = append(count.Repeated, n.ref)
		}
	}
	count.Complete = placed == count.Hunks && len(count.Repeated) == 0 && len(count.Missing) == 0
	return count
}

func (b *orderBuilder) labelOf(id string) string {
	if l, ok := b.labels[id]; ok {
		return l
	}
	return qualifySymbolID(id, id)
}

func orderSymbolName(s types.DiffSymbol) string {
	switch {
	case s.Qualified != "":
		return s.Qualified
	case s.Label != "":
		return s.Label
	}
	return qualifySymbolID(s.ID, s.ID)
}

// qualifySymbolID names a symbol by its SCIP descriptors, so two methods called alike on
// different receivers read apart: `goShapes.typeClass`.
func qualifySymbolID(id, fallback string) string {
	name := id[strings.LastIndex(id, "/")+1:]
	name = strings.TrimRight(strings.NewReplacer("()", "", "#", ".").Replace(name), ".")
	if name == "" {
		return fallback
	}
	return name
}

func fixedOrderPlacement(id int, why types.DiffWhy) orderPlacement {
	return orderPlacement{nodes: []int{id}, fixed: &why}
}

func unrankedOrderWhy(reason string) types.DiffWhy {
	return types.DiffWhy{Relation: types.DiffWhyUnranked, Text: reason}
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
