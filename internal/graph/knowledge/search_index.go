package knowledge

import (
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/types"
)

// optimization: Resolve scans a per-graph search index instead of g.nodes.
//
// Before, every node of every Resolve paid for work that is identical on every call:
// lowercasing id + label + doc into one haystack, lowercasing id and label again inside
// interactive.LeafScore once per term, lowercasing id and doc a third time on the
// substring fallback, and a longest-prefix scan over every project path to find the
// node's owner. The index does that once per graph, lazily, and a query keeps only the
// comparisons that depend on the query.
//
//	measured: 2000 projects x 8 targets, means of n=6 on 4 cores (benchstat is not
//	          installed; means taken by hand).
//	          BenchmarkResolve           25.5 ms -> 2.6 ms (-90%), 3.20 MB -> 0.49 MB/op,
//	                                     46,023 -> ~62 allocs/op.
//	          BenchmarkQueryNeighborhood 34.0 ms -> 3.8 ms (-89%), 4.7 MB -> 0.76 MB/op,
//	                                     49.5k -> ~560 allocs/op.
//	          Both include the one-time build, amortized over b.N.
//	trade-off: one entry per node (a copy of the node struct plus the lowercased text;
//	          strings that are already lowercase, ids mostly, are shared, not copied).
//	          The first query on a graph pays the build, about one old Resolve call, so a
//	          one-shot CLI query is not slower; a long-lived graph amortizes it.
//
// Scoring is unchanged: scoreNode keeps the old decision logic, and the differential
// test in search_index_test.go holds Resolve to a straightforward reference copy of the
// pre-index implementation.

// searchHolder is the lazily built index plus the mutex that coalesces concurrent
// first-builds. It is a separate object so graphs that share content (share, adopt) share
// the holder and therefore the one build; a graph that writes drops its pointer and never
// touches the holder the cache still serves.
type searchHolder struct {
	mu sync.Mutex
	ix *searchIndex
}

// searchHolder returns g's holder, creating it on first use. Guarded by idxMu because
// two concurrent first-queries would otherwise both create one and keep different ones.
func (g *Graph) searchHolder() *searchHolder {
	g.idxMu.Lock()
	defer g.idxMu.Unlock()
	if g.search == nil {
		g.search = &searchHolder{}
	}
	return g.search
}

// searchIdx returns g's search index, building it on first use. Built under the holder's
// mutex for the reason ensureAdj uses adjMu: the server publishes one *Graph to concurrent
// requests, and a bare nil check would let two first-queries build and publish together.
func (g *Graph) searchIdx() *searchIndex {
	h := g.searchHolder()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ix == nil {
		h.ix = buildSearchIndex(g.nodes)
	}
	return h.ix
}

// searchEntry is one node as a query sees it. The lowercased fields are what scoreNode
// used to recompute per call; the slash counts are LeafScore's per-separator penalty,
// which depends on the path alone.
type searchEntry struct {
	node                 types.KnowledgeNode
	lcID, lcLabel, lcDoc string
	idSlashes, lblSlash  int
}

// projectOwner is the answer to "which project owns this node", the old projectOf pair.
type projectOwner struct {
	path string
	ok   bool
}

// searchIndex is immutable once built, apart from owner, which is filled once on the
// first query that names a project.
type searchIndex struct {
	entries []searchEntry
	// byKind lists each kind's entries by position, so a positive kind= filter visits
	// only those nodes instead of every node and rejecting most of them.
	byKind map[string][]int32

	// owner is parallel to entries. Built apart from the text because most queries never
	// filter by project, and a one-shot CLI query should not pay for it.
	ownOnce sync.Once
	owner   []projectOwner
}

func buildSearchIndex(nodes map[string]types.KnowledgeNode) *searchIndex {
	ix := &searchIndex{
		entries: make([]searchEntry, 0, len(nodes)),
		byKind:  map[string][]int32{},
	}
	for id, n := range nodes {
		n.ID = id // the map key is what Resolve always reported
		ix.byKind[n.Kind] = append(ix.byKind[n.Kind], int32(len(ix.entries)))
		ix.entries = append(ix.entries, searchEntry{
			node:      n,
			lcID:      strings.ToLower(id),
			lcLabel:   strings.ToLower(n.Label),
			lcDoc:     strings.ToLower(n.Doc),
			idSlashes: strings.Count(id, "/"),
			lblSlash:  strings.Count(n.Label, "/"),
		})
	}
	return ix
}

// owners returns the project owner of every entry, computed on first use. A project node
// and a target are owned by the project their ID names; every other node by the longest
// project path that prefixes its source (see owningProject). Built from the index's own
// entries, so it is consistent with the text it ships beside.
func (ix *searchIndex) owners() []projectOwner {
	ix.ownOnce.Do(func() {
		projects := map[string]struct{}{}
		for i := range ix.entries {
			if p, ok := strings.CutPrefix(ix.entries[i].node.ID, types.KindProject+":"); ok {
				projects[p] = struct{}{}
			}
		}
		ix.owner = make([]projectOwner, len(ix.entries))
		for i := range ix.entries {
			n := &ix.entries[i].node
			if p, ok := projectPathOf(n.ID); ok {
				ix.owner[i] = projectOwner{p, true}
				continue
			}
			src := n.Source
			if j := strings.IndexByte(src, ':'); j >= 0 {
				src = src[:j] // strip a :line suffix
			}
			if src == "" {
				continue
			}
			ix.owner[i].path, ix.owner[i].ok = owningProject(src, projects)
		}
	})
	return ix.owner
}

// owningProject returns the project path that owns a source path. The rule is the one
// the old per-node scan applied over the project paths sorted longest-first (ties
// lexically): the first path p with p == "." or src == p or src has the prefix p + "/".
// That scan was O(projects) per node; the candidates are only src itself and each
// directory above it, so this walks those, longest first, against a set.
//
// The "." catch-all stays a special case because its place in the old order is a quirk
// worth keeping byte-identical: it sorts before any single-character project that
// compares greater than ".", so it can beat a real one-character project, and it loses to
// everything longer. TestResolveProjectOwnerMatchesPrefixScan holds this to the scan.
func owningProject(src string, projects map[string]struct{}) (string, bool) {
	best, found := "", false
	for cand := src; ; {
		if _, ok := projects[cand]; ok {
			best, found = cand, true
			break
		}
		i := strings.LastIndexByte(cand, '/')
		if i < 0 {
			break
		}
		cand = cand[:i]
	}
	if _, dot := projects["."]; dot && (!found || projectPathBefore(".", best)) {
		return ".", true
	}
	return best, found
}

// projectPathBefore reports whether a sorts before b in the longest-first, then lexical,
// order the project paths were scanned in.
func projectPathBefore(a, b string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a < b
}

// glob is a '*' pattern lowered and split once, so a query pays that per term, not per
// node. Semantics are globMatch's.
type glob struct{ parts []string }

func compileGlob(pattern string) glob {
	return glob{parts: strings.Split(strings.ToLower(pattern), "*")}
}

// matchLower reports whether an already lowercased string matches. Middle segments match
// leftmost without backtracking, which is correct because the surrounding '*' absorb any
// slack.
func (gl glob) matchLower(str string) bool {
	parts := gl.parts
	if len(parts) == 1 {
		return parts[0] == str
	}
	if !strings.HasPrefix(str, parts[0]) {
		return false
	}
	str = str[len(parts[0]):]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(str, mid)
		if i < 0 {
			return false
		}
		str = str[i+len(mid):]
	}
	return strings.HasSuffix(str, parts[len(parts)-1])
}

// needle is one id= value, compiled: a glob when it has '*', else a lowercased substring.
type needle struct {
	lc   string
	glob *glob
}

func compileNeedles(vals []string) []needle {
	out := make([]needle, len(vals))
	for i, v := range vals {
		if hasWildcard(v) {
			gl := compileGlob(v)
			out[i].glob = &gl
		} else {
			out[i].lc = strings.ToLower(v)
		}
	}
	return out
}

// anyIn is containsAny over an already lowercased haystack.
func anyIn(lcHay string, needles []needle) bool {
	for _, n := range needles {
		if n.glob != nil {
			if n.glob.matchLower(lcHay) {
				return true
			}
		} else if strings.Contains(lcHay, n.lc) {
			return true
		}
	}
	return false
}

// attrFilter is one field's positive, negated and regex constraints on a single attr
// value, compared exactly (language, role, layer, family: closed vocabularies).
type attrFilter struct {
	pos, neg []string
	re       []*regexp.Regexp
}

func newAttrFilter(q parsedQuery, field string) attrFilter {
	return attrFilter{pos: q.fields[field], neg: q.negFields[field], re: q.reFields[field]}
}

func (f attrFilter) active() bool { return len(f.pos) > 0 || len(f.neg) > 0 || len(f.re) > 0 }

// allows reports whether v satisfies every constraint of the filter.
func (f attrFilter) allows(v string) bool {
	if len(f.pos) > 0 && !slices.Contains(f.pos, v) {
		return false
	}
	if slices.Contains(f.neg, v) {
		return false
	}
	return len(f.re) == 0 || matchesAnyRe(v, f.re)
}

// compiledQuery is a parsedQuery with everything that does not depend on the node
// resolved once: the field lookups, the lowercased terms, the compiled globs and the
// relation set. scoreNode used to redo each of these per node.
type compiledQuery struct {
	q parsedQuery

	kindPos, kindNeg []string
	kindRe           []*regexp.Regexp

	projPos, projNeg []string
	projRe           []*regexp.Regexp
	needsOwner       bool

	idPos, idNeg []needle
	idRe         []*regexp.Regexp

	language, role, layer, family attrFilter
	stamp                         bool

	negTerms []compiledTerm
	terms    []compiledTerm

	// relOnly is set for a relation-only query, which matches nodes touching a relation.
	relOnly bool
	relSet  map[string]bool
}

// compiledTerm is a free-text term: lowercased once, plus the compiled glob when it has a '*'.
type compiledTerm struct {
	lc   string
	glob *glob
	// span marks a term holding a space, which a negation must test against the joined
	// "id label doc" text because it can straddle two fields.
	span bool
}

func compileTerms(terms []string) []compiledTerm {
	out := make([]compiledTerm, len(terms))
	for i, t := range terms {
		if hasWildcard(t) {
			gl := compileGlob(t)
			out[i].glob = &gl
			continue
		}
		out[i].lc = strings.ToLower(t)
		out[i].span = strings.IndexByte(t, ' ') >= 0
	}
	return out
}

func (g *Graph) compileQuery(q parsedQuery) *compiledQuery {
	cq := &compiledQuery{q: q}
	cq.kindPos, cq.kindNeg, cq.kindRe = q.fields["kind"], q.negFields["kind"], q.reFields["kind"]
	cq.projPos, cq.projNeg, cq.projRe = q.fields["project"], q.negFields["project"], q.reFields["project"]
	cq.idPos, cq.idNeg, cq.idRe = compileNeedles(q.fields["id"]), compileNeedles(q.negFields["id"]), q.reFields["id"]
	cq.language, cq.role = newAttrFilter(q, "language"), newAttrFilter(q, "role")
	cq.layer, cq.family = newAttrFilter(q, "layer"), newAttrFilter(q, "family")
	cq.stamp = len(q.fields["stamp"]) > 0 || len(q.negFields["stamp"]) > 0 || len(q.reFields["stamp"]) > 0
	cq.negTerms, cq.terms = compileTerms(q.negTerms), compileTerms(q.terms)
	cq.needsOwner = len(cq.projPos) > 0 || len(cq.projNeg) > 0 || len(cq.projRe) > 0
	if rels, ok := q.fields["relation"]; ok && len(q.terms) == 0 {
		cq.relOnly, cq.relSet = true, toSet(rels)
		g.ensureAdj() // once per query, not once per node
	}
	return cq
}

// leafScoreLower is interactive.LeafScore for a path and query that are already
// lowercased, with the path's '/' count passed in: the same arithmetic without the two
// ToLower calls and the Count that made it the hot spot of Resolve. search_index_test.go
// holds the two together.
func leafScoreLower(lcPath, lcQuery string, slashes int) int {
	if lcQuery == "" || !strings.Contains(lcPath, lcQuery) {
		return 0
	}
	leaf := lcPath
	if i := strings.LastIndexByte(lcPath, '/'); i >= 0 {
		leaf = lcPath[i+1:]
	}
	score := 1
	if idx := strings.Index(leaf, lcQuery); idx >= 0 {
		score += 10000
		if idx == 0 {
			score += 5000
		}
		score += (len(lcQuery) * 1000) / max(len(leaf), 1)
	}
	return score - 10*slashes
}
