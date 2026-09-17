package knowledge

import (
	"cmp"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// callableKinds are the SCIP classifiers a duplication pair may be made of. A type, field
// or constant has no body to duplicate, and comparing them would rank the model's shape.
var callableKinds = map[string]bool{"Function": true, "Method": true}

// Duplicates ranks pairs of functions that orchestrate the same workspace symbols in the
// same proportions, which is what copied logic looks like from the call graph.
//
// It reads `calls` edges, so it is language-agnostic: any language whose index emits them
// is compared the same way, and nothing here parses source. The price is what those edges
// hold. They record calls to symbols THIS WORKSPACE DEFINES, not to a standard library or
// a dependency, so two functions whose shared logic is entirely stdlib calls have empty
// call sets and are invisible to this. What it finds is duplication one level up: two
// functions that assemble the same helpers into the same sequence.
//
// Each shared callee is weighted by how RARE it is across the functions compared, the idf
// of search ranking, because the signal is not "both call something" but "both call the
// same unusual things". A pair where one calls the other is skipped: a thin wrapper around
// a function reads as sharing all of its callees, and it is not a copy.
//
// A measurement, not a verdict. Two functions can share their shape on purpose, and the
// reader decides which pairs are worth folding. The thresholds in opts are the noise
// filters; see types.DuplicationOptions.
func (g *Graph) Duplicates(opts types.DuplicationOptions) []types.DuplicationGroup {
	g.ensureAdj()

	type candidate struct {
		site    types.DuplicationSite
		callees map[string]bool
		span    int
	}
	var fns []candidate
	for id, n := range g.nodes {
		if n.Kind != types.KindSymbol || !callableKinds[n.Attrs[attrSymbolKind]] {
			continue
		}
		// Tests are left out unless asked for. Sibling tests in one file call the same setup
		// helpers by design, so they pair at a perfect score and bury every finding a person
		// would act on: on this repository they were 3,944 of 4,182 pairs.
		if !opts.IncludeTests && isTestSource(n.Source) {
			continue
		}
		start, end := spanOf(n)
		if end < start || start == 0 {
			continue
		}
		callees := map[string]bool{}
		for _, e := range g.out[id] {
			if e.Relation == types.RelationCalls && e.Target != id {
				callees[e.Target] = true
			}
		}
		if len(callees) < opts.MinCallees {
			continue
		}
		fns = append(fns, candidate{
			site:    types.DuplicationSite{ID: id, Label: n.Label, Source: n.Source, EndLine: end},
			callees: callees,
			span:    end - start + 1,
		})
	}

	// Rarity: how many compared functions call each symbol. The candidate list is built
	// from the same inverted index, so only pairs sharing a callee are ever scored, which
	// keeps this near-linear in call edges rather than quadratic in functions.
	callers := map[string][]int{}
	for i, f := range fns {
		for c := range f.callees {
			callers[c] = append(callers[c], i)
		}
	}
	idf := func(symbol string) float64 {
		return math.Log(float64(len(fns)+1) / float64(len(callers[symbol])))
	}

	// Pairs that clear every threshold are joined into groups by union-find, keeping the
	// weakest score seen on any joining pair so a group never reads tighter than its
	// weakest link.
	parent := make([]int, len(fns))
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
	weakest := map[int]float64{}
	seen := map[[2]int]bool{}
	for _, idx := range callers {
		for x := 0; x < len(idx); x++ {
			for y := x + 1; y < len(idx); y++ {
				key := [2]int{min(idx[x], idx[y]), max(idx[x], idx[y])}
				if seen[key] {
					continue
				}
				seen[key] = true
				score, ok := pairScore(fns[key[0]].callees, fns[key[1]].callees, fns[key[0]].site.ID, fns[key[1]].site.ID,
					fns[key[0]].span, fns[key[1]].span, idf, opts)
				if !ok {
					continue
				}
				ra, rb := find(key[0]), find(key[1])
				low := score
				for _, r := range []int{ra, rb} {
					if w, has := weakest[r]; has && w < low {
						low = w
					}
				}
				delete(weakest, ra)
				delete(weakest, rb)
				parent[ra] = rb
				weakest[rb] = low
			}
		}
	}

	members := map[int][]int{}
	for i := range fns {
		if _, grouped := weakest[find(i)]; grouped {
			members[find(i)] = append(members[find(i)], i)
		}
	}
	deps := g.packageDeps()
	fileNS := g.fileNamespaces()
	out := make([]types.DuplicationGroup, 0, len(members))
	for root, idxs := range members {
		group := types.DuplicationGroup{Score: math.Round(weakest[root]*100) / 100, Siblings: true}
		shared := maps.Clone(fns[idxs[0]].callees)
		memberPkgs := map[string]bool{}
		for _, i := range idxs {
			memberPkgs[g.nodes[fns[i].site.ID].Attrs[attrNamespace]] = true
			maps.DeleteFunc(shared, func(c string, _ bool) bool { return !fns[i].callees[c] })
		}
		spans, longest := 0, 0
		for _, i := range idxs {
			site := g.describeMember(fns[i].site, fns[i].callees, shared, fileNS)
			group.Members = append(group.Members, site)
			group.Siblings = group.Siblings && site.Label == fns[idxs[0]].site.Label
			spans += fns[i].span
			longest = max(longest, fns[i].span)
		}
		// Members by ID and callees sorted, so the same group renders identically every run:
		// member order came from map iteration, and a list that reshuffles cannot be worked.
		slices.SortFunc(group.Members, func(a, b types.DuplicationSite) int { return cmp.Compare(a.ID, b.ID) })
		group.Shared = slices.Sorted(maps.Keys(shared))
		// A fold keeps one body and leaves each member its signature, the call and its close,
		// so that is what it saves: every body but the longest, less three lines a member.
		group.Removable = max(0, spans-longest-foldedMemberLines*len(idxs))
		group.Shape = foldShape(group)
		unplaced := memberPkgs[""]
		delete(memberPkgs, "")
		group.Packages = slices.Sorted(maps.Keys(memberPkgs))

		if unplaced {
			group.Placement = types.PlacementUnknown
		} else {
			calleePkgs := map[string]bool{}
			for c := range shared {
				if p := g.nodes[c].Attrs[attrNamespace]; p != "" {
					calleePkgs[p] = true
				}
			}
			group.Placement, group.Home = deps.placement(group.Packages, slices.Sorted(maps.Keys(calleePkgs)))
		}
		out = append(out, group)
	}

	// Folds worth making first, largest saving first, then score, then the first member's ID
	// so ties are stable between runs. Similarity alone ranked four identical three-line
	// wrappers above a sixty-line copy.
	left := func(g types.DuplicationGroup) int {
		if g.Shape == types.ShapeLeave {
			return 1
		}
		return 0
	}
	slices.SortFunc(out, func(p, q types.DuplicationGroup) int {
		if c := cmp.Compare(left(p), left(q)); c != 0 {
			return c
		}
		if c := cmp.Compare(q.Removable, p.Removable); c != 0 {
			return c
		}
		if c := cmp.Compare(q.Score, p.Score); c != 0 {
			return c
		}
		return cmp.Compare(p.Members[0].ID, q.Members[0].ID)
	})
	return out
}

// foldedMemberLines is what a member still occupies once its body moves into a helper:
// its signature, the call, and its closing line.
const foldedMemberLines = 3

// describeMember fills in what sets one member apart: the calls the group does not share,
// who calls it, whether code outside its package names it, and whether a test does.
func (g *Graph) describeMember(site types.DuplicationSite, callees, shared map[string]bool, fileNS map[string][]string) types.DuplicationSite {
	n := g.nodes[site.ID]
	site.Distinct = []string{}
	for c := range callees {
		if !shared[c] {
			site.Distinct = append(site.Distinct, c)
		}
	}
	slices.Sort(site.Distinct)

	callers := map[string]bool{}
	foreign := map[string]bool{}
	for _, e := range g.in[site.ID] {
		switch e.Relation {
		case types.RelationCalls:
			callers[e.Source] = true
		case types.RelationReferences:
			file, ok := strings.CutPrefix(e.Source, types.KindFile+":")
			if !ok || isTestSource(file) {
				continue
			}
			if !slices.Contains(fileNS[e.Source], n.Attrs[attrNamespace]) {
				foreign[file] = true
			}
		}
	}
	site.Callers = len(callers)
	site.ForeignFiles = len(foreign)
	site.Tested = n.Attrs[attrTestRefs] != ""
	return site
}

// foldShape reads the fold a group's evidence supports. Order matters: a fold that saves
// nothing is not worth making whatever its shape, and same-named members are siblings
// even when they also differ only in values.
func foldShape(group types.DuplicationGroup) types.DuplicationShape {
	if group.Removable == 0 {
		return types.ShapeLeave
	}
	if group.Siblings {
		return types.ShapeSiblingHelper
	}
	for _, m := range group.Members {
		if len(m.Distinct) > 0 {
			return types.ShapeSharedCore
		}
	}
	return types.ShapeParameterize
}

// pairScore is the rarity-weighted overlap of two functions' callees, and whether the pair
// clears every threshold. A pair where one calls the other is refused: a thin wrapper reads
// as sharing all of its callee's calls, and it is not a copy.
func pairScore(a, b map[string]bool, aID, bID string, aSpan, bSpan int, idf func(string) float64, opts types.DuplicationOptions) (float64, bool) {
	if a[bID] || b[aID] {
		return 0, false
	}
	if float64(min(aSpan, bSpan))/float64(max(aSpan, bSpan)) < opts.MinSpanRatio {
		return 0, false
	}
	var inter, union float64
	shared := 0
	for c := range a {
		w := idf(c)
		union += w
		if b[c] {
			inter += w
			shared++
		}
	}
	for c := range b {
		if !a[c] {
			union += idf(c)
		}
	}
	if union == 0 || shared < opts.MinShared {
		return 0, false
	}
	score := inter / union
	return score, score >= opts.MinScore
}

// spanOf is a symbol's first and last definition line, or zeros when the index emitted no
// enclosing range. Source is "<path>:<line>".
func spanOf(n types.KnowledgeNode) (start, end int) {
	_, line, ok := strings.Cut(n.Source, ":")
	if !ok {
		return 0, 0
	}
	start, err := strconv.Atoi(line)
	if err != nil {
		return 0, 0
	}
	end, err = strconv.Atoi(n.Attrs[attrDefEndLine])
	if err != nil {
		return 0, 0
	}
	return start, end
}
