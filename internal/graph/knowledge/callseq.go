package knowledge

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// Scores of callSeqCheck's finding: a copied pipeline is sharper when the change touches both
// copies, since the author has both in view.
const (
	callSeqScore        = 0.55
	callSeqPartnerScore = 0.65
)

// callSeqWrapperUsers is how many callables must call a single-call body before it counts as a
// wrapper and leaves every sequence.
const callSeqWrapperUsers = 3

// callSeqCheck reports a function the change adds, renames or re-signs that calls the same
// workspace functions in the same order as another function, over a run longer than the
// call-sequence value [types.ConformanceOptions.Effective] enforces for its population: the
// pipeline was copied, often with a step or two interleaved, and a rule added to one copy
// misses the other. Each callee counts once per sequence, so the run is also its count of
// distinct callees.
//
// A wrapper, a body of one call that three or more callables use, is dropped from every
// sequence: it is shared vocabulary, not a step. Two shapes measured as false positives are
// excluded: a pair whose shared callees are mostly methods of one type (a builder filling or
// reading values of that type, as script bindings do), and two Go command verbs, functions of
// a main package taking a []string argument list, which share the parse-and-open prologue by
// design. The partner is any measured function the change did not introduce, so a change
// cannot copy its own new pipeline into a norm. Order comes from the reference lines the index
// records, capped per file: a callee whose recorded lines all miss the caller's body sorts by
// its first recorded line, so a hot callee can sort out of place.
//
// It judges against the committed value, else the configured anchor, never a live
// percentile; a population with neither, and a workspace with no symbol index, are silent.
type callSeqCheck struct{}

func (callSeqCheck) name() string { return types.CheckCallSequence }

func (callSeqCheck) run(x *namingIndex) []finding {
	o := x.change.Conformance
	var q *callSeqIndex
	known := conformanceKnownItems(o.Ratchet.Known)[types.ConformanceCallSequence]
	subjects := map[string]bool{}
	for _, id := range x.change.Subjects {
		subjects[id] = true
	}
	var out []finding
	for _, s := range x.subjects {
		if !s.callable() {
			continue
		}
		if q == nil {
			q = newCallSeqIndex(x.g, o)
		}
		i, ok := q.idx[s.id]
		if !ok {
			continue
		}
		limit, src := o.Effective(types.ConformanceKey{Metric: types.ConformanceCallSequence, Population: q.pop[i]})
		if src == "" || known[q.items[i]] {
			continue
		}
		j, n := q.best(i, func(p int) bool { return x.change.Introduced[q.ids[p]] })
		if n <= limit {
			continue
		}
		partner := x.g.nodes[q.ids[j]]
		score := callSeqScore
		if subjects[q.ids[j]] {
			score = callSeqPartnerScore
		}
		run := sharedRun(q.seqs[i], q.seqs[j], q.wrappers)
		order := make([]string, len(run))
		for k, c := range run {
			order[k] = cmp.Or(x.g.nodes[c].Label, c)
		}
		out = append(out, finding{
			subject: s.id,
			score:   score,
			message: fmt.Sprintf("`%s` calls %d workspace functions in the order `%s` does, past the %s of %d for call-sequence %s",
				s.label, n, partner.Label, thresholdSource(src), limit, q.pop[i]),
			details: []string{
				"in order: " + strings.Join(order, ", "),
				"partner: " + q.items[j] + " at " + partner.Source,
				fmt.Sprintf("call-sequence %s: %d, the %s", q.pop[i], limit, thresholdSource(src)),
				"share the sequence in one function, or record a known call-sequence entry with its reason",
			},
		})
	}
	return out
}

// callSeqSamples measures, for each source and main callable that calls a workspace function
// past its wrappers, the longest run it shares with any other such callable, keyed by
// population and in item ID order. A callable calling none has no sequence and no sample.
func callSeqSamples(g *Graph, o types.ConformanceOptions) map[types.ConformanceKey][]types.ConformanceItem {
	q := newCallSeqIndex(g, o)
	out := map[types.ConformanceKey][]types.ConformanceItem{}
	for i, id := range q.ids {
		if len(q.steps[i]) == 0 {
			continue
		}
		_, n := q.best(i, nil)
		k := types.ConformanceKey{Metric: types.ConformanceCallSequence, Population: q.pop[i]}
		out[k] = append(out[k], types.ConformanceItem{ID: q.items[i], Source: g.nodes[id].Source, Measure: n})
	}
	for _, items := range out {
		slices.SortFunc(items, func(a, b types.ConformanceItem) int { return cmp.Compare(a.ID, b.ID) })
	}
	return out
}

// callSequences is each non-test, non-generated callable's workspace callees in source order,
// keyed by symbol ID; a callable calling none is absent. The callees are its `calls` edges;
// the order is each callee's first reference line inside the caller's definition span, read
// from the caller file's `references` edge. A callee whose capped line list misses the span
// sorts by its first recorded line, and one with no recorded line sorts last, by ID.
func callSequences(g *Graph) map[string][]string {
	g.ensureAdj()
	refLines := map[string]map[string][]int{}
	linesIn := func(file string) map[string][]int {
		if lines, ok := refLines[file]; ok {
			return lines
		}
		lines := map[string][]int{}
		for _, e := range g.out[fileID(file)] {
			if e.Relation != types.RelationReferences {
				continue
			}
			if _, ls, ok := parseRefProvenance(e.Provenance); ok {
				lines[e.Target] = slices.Sorted(slices.Values(ls))
			}
		}
		refLines[file] = lines
		return lines
	}
	out := map[string][]string{}
	for id, n := range g.nodes {
		if n.Kind != types.KindSymbol {
			continue
		}
		file, _, ok := splitPathLine(n.Source)
		if !ok || types.IsTestPath(file) || conformanceGenerated(g, types.ConformanceOptions{}, file) {
			continue
		}
		p, _ := parseDescriptors(id)
		if k := neutralKind(afterNamespace(p)); k != declFunction && k != declMethod {
			continue
		}
		var callees []string
		for _, e := range g.out[id] {
			if e.Relation == types.RelationCalls && e.Target != id {
				callees = append(callees, e.Target)
			}
		}
		if len(callees) == 0 {
			continue
		}
		start, end := definitionSpan(n)
		lines := linesIn(file)
		at := func(callee string) int {
			ls := lines[callee]
			if i := slices.IndexFunc(ls, func(l int) bool { return l >= start && l <= end }); i >= 0 {
				return ls[i]
			}
			if len(ls) > 0 {
				return ls[0]
			}
			return math.MaxInt
		}
		slices.SortFunc(callees, func(a, b string) int { return cmp.Or(cmp.Compare(at(a), at(b)), cmp.Compare(a, b)) })
		out[id] = slices.Compact(callees)
	}
	return out
}

// longestSharedRun is the length of the longest common subsequence of a and b once every
// wrapper is dropped from both.
func longestSharedRun(a, b []string, wrappers map[string]bool) int {
	a, b = withoutWrappers(a, wrappers), withoutWrappers(b, wrappers)
	prev, cur := make([]int, len(b)+1), make([]int, len(b)+1)
	for i := range a {
		for j := range b {
			if a[i] == b[j] {
				cur[j+1] = prev[j] + 1
			} else {
				cur[j+1] = max(prev[j+1], cur[j])
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// sharedRun is one longest common subsequence of a and b once every wrapper is dropped, in
// a's order: the run a finding names.
func sharedRun(a, b []string, wrappers map[string]bool) []string {
	a, b = withoutWrappers(a, wrappers), withoutWrappers(b, wrappers)
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var run []string
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			run = append(run, a[i])
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			i++
		default:
			j++
		}
	}
	return run
}

func withoutWrappers(seq []string, wrappers map[string]bool) []string {
	return slices.DeleteFunc(slices.Clone(seq), func(c string) bool { return wrappers[c] })
}

// callSeqWrappers is every callee with a single-call body, a definition of at most three lines
// calling at most one workspace function, that callSeqWrapperUsers or more callables call.
func callSeqWrappers(g *Graph, seqs map[string][]string) map[string]bool {
	users := map[string]int{}
	for _, callees := range seqs {
		for _, c := range callees {
			users[c]++
		}
	}
	wrappers := map[string]bool{}
	for c, n := range users {
		start, end := definitionSpan(g.nodes[c])
		if n >= callSeqWrapperUsers && start > 0 && end >= start && end-start < 3 && len(seqs[c]) <= 1 {
			wrappers[c] = true
		}
	}
	return wrappers
}

// callSeqIndex holds the measured callables' sequences, numbered in ID order so a search
// counts shared callees in a slice rather than a map.
type callSeqIndex struct {
	g        *Graph
	ci       *conformanceIndex
	wrappers map[string]bool
	ids      []string
	idx      map[string]int
	items    []string
	pop      []types.ConformancePopulation
	seqs     [][]string
	// steps are the sequences less their wrappers, as sets.
	steps   []map[string]bool
	callers map[string][]int
	counts  []int
}

// newCallSeqIndex places every sequenced callable in its population, leaving out tests and
// what o generates or excludes.
func newCallSeqIndex(g *Graph, o types.ConformanceOptions) *callSeqIndex {
	all := callSequences(g)
	q := &callSeqIndex{g: g, ci: newConformanceIndex(g, o), wrappers: callSeqWrappers(g, all), idx: map[string]int{}, callers: map[string][]int{}}
	for _, id := range slices.Sorted(maps.Keys(all)) {
		file, _, _ := splitPathLine(g.nodes[id].Source)
		pop, ok := q.ci.population(file)
		if !ok || pop == types.ConformancePopTest {
			continue
		}
		i := len(q.ids)
		steps := map[string]bool{}
		for _, c := range all[id] {
			if !q.wrappers[c] && !steps[c] {
				steps[c] = true
				q.callers[c] = append(q.callers[c], i)
			}
		}
		q.idx[id] = i
		q.ids = append(q.ids, id)
		q.items = append(q.items, symbolItemID(file, q.ci.local[id]))
		q.pop = append(q.pop, pop)
		q.seqs = append(q.seqs, all[id])
		q.steps = append(q.steps, steps)
	}
	q.counts = make([]int, len(q.ids))
	return q
}

// best is the callable sharing the longest run with callable i, and that run's length: -1 and
// 0 when none shares a step. skip leaves a partner out; on a tie the lower ID wins.
func (q *callSeqIndex) best(i int, skip func(int) bool) (partner, n int) {
	var touched []int
	for c := range q.steps[i] {
		for _, j := range q.callers[c] {
			if j == i {
				continue
			}
			if q.counts[j] == 0 {
				touched = append(touched, j)
			}
			q.counts[j]++
		}
	}
	slices.Sort(touched)
	partner = -1
	for _, j := range touched {
		shared := q.counts[j]
		q.counts[j] = 0
		if shared <= n || skip != nil && skip(j) || q.excluded(i, j) {
			continue
		}
		if run := longestSharedRun(q.seqs[i], q.seqs[j], q.wrappers); run > n {
			partner, n = j, run
		}
	}
	return partner, n
}

// excluded reports whether callables i and j share their callees by a shape that is not a
// copied pipeline: most of the shared callees are methods of one type, or both are Go command
// verbs taking a []string argument list.
func (q *callSeqIndex) excluded(i, j int) bool {
	if q.pop[i] == types.ConformancePopMain && q.pop[j] == types.ConformancePopMain && q.takesArgList(i) && q.takesArgList(j) {
		return true
	}
	shared, byType := 0, map[string]int{}
	for c := range q.steps[i] {
		if !q.steps[j][c] {
			continue
		}
		shared++
		if neutralKind(q.ci.local[c]) == declMethod {
			byType[c[:strings.LastIndexByte(c, '#')+1]]++
		}
	}
	for _, n := range byType {
		if 2*n > shared {
			return true
		}
	}
	return false
}

// takesArgList reports whether callable i is a Go function with a []string parameter.
func (q *callSeqIndex) takesArgList(i int) bool {
	id := q.ids[i]
	n := q.g.nodes[id]
	if n.Attrs[attrLanguage] != "go" {
		return false
	}
	local := q.ci.local[id]
	shape := goShapes{}.read(symbolFacts{
		Kind: n.Attrs[attrSymbolKind], Signature: n.Attrs[AttrSignature], Path: local, Neutral: neutralKind(local),
	})
	return slices.ContainsFunc(shape.Params, func(p namingParam) bool { return p.Type == "[]string" })
}
