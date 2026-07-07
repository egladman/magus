package knowledge

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// Query is deterministic name resolution and retrieval over the knowledge graph -
// no LLM. It reuses magus's existing fuzzy-finding score (interactive.LeafScore,
// which powers `magus x`/`magus where`), generalized from project paths to node
// IDs and labels. The fielded grammar here is a pragmatic subset - field:value
// filters (kind/project/relation/id), free-text terms (AND), and negation; the
// full boolean grammar (OR/parens/wildcards) and the search.js conformance
// fixture are a later increment.

// DefaultBudget bounds the neighborhood a query collects, so a match on a
// high-degree node cannot pull in the whole graph.
const DefaultBudget = 50

// knownFields are the recognized field:value prefixes. kind/project/id constrain
// which nodes match; relation constrains which edges a neighborhood traverses.
var knownFields = map[string]bool{"kind": true, "project": true, "id": true, "relation": true}

type parsedQuery struct {
	terms     []string            // positive free-text tokens (AND)
	negTerms  []string            // negated free-text tokens (must not appear)
	fields    map[string][]string // field -> allowed values (OR within, AND across)
	negFields map[string][]string // field -> excluded values
	raw       string
}

// parseQuery splits a query string into free-text terms and field filters.
// "kind:spell project:pkg/foo build -kind:op" -> terms[build], fields{kind:[spell],
// project:[pkg/foo]}, negFields{kind:[op]}. Double-quoted phrases stay one term.
func parseQuery(input string) parsedQuery {
	q := parsedQuery{fields: map[string][]string{}, negFields: map[string][]string{}, raw: strings.TrimSpace(input)}
	for _, tok := range tokenize(input) {
		neg := false
		if strings.HasPrefix(tok, "-") && len(tok) > 1 {
			neg, tok = true, tok[1:]
		}
		if field, val, ok := splitField(tok); ok {
			if neg {
				q.negFields[field] = append(q.negFields[field], val)
			} else {
				q.fields[field] = append(q.fields[field], val)
			}
			continue
		}
		if neg {
			q.negTerms = append(q.negTerms, tok)
		} else {
			q.terms = append(q.terms, tok)
		}
	}
	return q
}

// splitField returns (field, value, true) when tok is "<knownField>:<value>".
func splitField(tok string) (string, string, bool) {
	i := strings.IndexByte(tok, ':')
	if i <= 0 {
		return "", "", false
	}
	field := strings.ToLower(tok[:i])
	if !knownFields[field] {
		return "", "", false
	}
	return field, tok[i+1:], true
}

// tokenize splits on whitespace, keeping "double quoted" spans as one token.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case (r == ' ' || r == '\t') && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// Resolve returns nodes matching the query, ranked by score (desc) then ID (asc),
// truncated to limit (0 = no limit).
func (g *Graph) Resolve(input string, limit int) []types.KnowledgeMatch {
	q := parseQuery(input)
	var matches []types.KnowledgeMatch
	for id, n := range g.nodes {
		score, ok := g.scoreNode(n, id, q)
		if !ok {
			continue
		}
		matches = append(matches, types.KnowledgeMatch{ID: id, Kind: n.Kind, Label: n.Label, Score: score})
	}
	slices.SortFunc(matches, func(a, b types.KnowledgeMatch) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// scoreNode applies node-level field filters and free-text scoring. It returns
// (score, true) when the node matches every positive constraint and no negation.
// A relation-only query (no node constraints) matches nodes that touch such an
// edge, so `magus query relation:uses` still resolves seeds.
func (g *Graph) scoreNode(n types.KnowledgeNode, id string, q parsedQuery) (int, bool) {
	if vals, ok := q.fields["kind"]; ok && !slices.Contains(vals, n.Kind) {
		return 0, false
	}
	if vals := q.negFields["kind"]; slices.Contains(vals, n.Kind) {
		return 0, false
	}
	if vals, ok := q.fields["project"]; ok && !matchesAnyProject(id, vals) {
		return 0, false
	}
	if vals := q.negFields["project"]; matchesAnyProject(id, vals) {
		return 0, false
	}
	if vals, ok := q.fields["id"]; ok && !containsAny(id, vals) {
		return 0, false
	}
	if vals := q.negFields["id"]; containsAny(id, vals) {
		return 0, false
	}

	// Negated free text must not appear anywhere in the node's text.
	hay := strings.ToLower(id + " " + n.Label + " " + n.Doc)
	for _, t := range q.negTerms {
		if strings.Contains(hay, strings.ToLower(t)) {
			return 0, false
		}
	}

	if len(q.terms) == 0 {
		if _, relOnly := q.fields["relation"]; relOnly && !g.touchesRelation(id, q.fields["relation"]) {
			return 0, false
		}
		return 1 + kindRank(n.Kind), true // field-only match; flat score plus kind bias
	}

	// Every positive term must match (AND); score is the sum of best per-term
	// leaf-anchored scores against ID and label, with a small credit for a doc hit.
	total := 0
	for _, t := range q.terms {
		best := max(interactive.LeafScore(id, t), interactive.LeafScore(n.Label, t))
		if best == 0 {
			if strings.Contains(strings.ToLower(n.Doc), strings.ToLower(t)) {
				best = 1
			} else {
				return 0, false
			}
		}
		total += best
	}
	return total + kindRank(n.Kind), true
}

// kindRank biases resolution toward primary domain entities (target, spell, ...)
// over source-level nodes (function, file, import, rationale, doc) when text
// relevance ties, so a bare `explain build` resolves the target, not its source
// function. The bonus is small relative to a real leaf match, so it only breaks
// ties; it never lets a weaker text match outrank a stronger one.
func kindRank(kind string) int {
	switch kind {
	case types.KindProject, types.KindTarget, types.KindSpell, types.KindOp,
		types.KindCharm, types.KindModule, types.KindMethod, types.KindDiagnostic:
		return 100
	default:
		return 0
	}
}

// Neighborhood collects the induced subgraph reachable from seeds within a node
// budget, treating edges as bidirectional so a query surfaces both what a node
// depends on and what depends on it. When relations is non-empty, only edges with
// those relations are traversed. Returns a fresh Graph for deterministic output.
func (g *Graph) Neighborhood(seeds []string, budget int, relations []string) *Graph {
	g.ensureAdj()
	relSet := toSet(relations)
	visited := map[string]bool{}
	queue := make([]string, 0, len(seeds))
	for _, s := range seeds {
		if _, ok := g.node(s); ok && !visited[s] {
			visited[s] = true
			queue = append(queue, s)
		}
	}
	visit := func(e types.KnowledgeEdge, cur string) {
		if len(relSet) > 0 && !relSet[e.Relation] {
			return
		}
		next := e.Target
		if next == cur {
			next = e.Source
		}
		if !visited[next] && len(visited) < budget {
			visited[next] = true
			queue = append(queue, next)
		}
	}
	for len(queue) > 0 && len(visited) < budget {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.out[cur] {
			visit(e, cur)
		}
		for _, e := range g.in[cur] {
			visit(e, cur)
		}
	}

	sub := NewGraph()
	for id := range visited {
		if n, ok := g.node(id); ok {
			sub.AddNode(n)
		}
		// optimization: collect induced edges by walking each visited node's
		// out-adjacency, not by scanning (and sorting) the whole edge set. Every
		// induced edge has its source in visited, so this finds all of them once.
		//   measured: BenchmarkQueryNeighborhood -65.8% sec/op, -51.6% B/op
		//             (benchstat, n=8, 16k-target fixture) - removes an O(E log E)
		//             g.Edges() sort per query, replaced by O(visited x out-degree).
		//             allocs are ~flat: Resolve's node scan dominates them, not this.
		//   trade-off: none; sub.Output() still sorts the (small) result.
		for _, e := range g.out[id] {
			if !visited[e.Target] {
				continue
			}
			if len(relSet) > 0 && !relSet[e.Relation] {
				continue
			}
			sub.AddEdge(e)
		}
	}
	return sub
}

// Query resolves the input to seeds and returns the ranked matches plus their
// neighborhood subgraph, bounded by budget.
func (g *Graph) Query(input string, budget int) types.KnowledgeQueryOutput {
	if budget <= 0 {
		budget = DefaultBudget
	}
	q := parseQuery(input)
	matches := g.Resolve(input, 0)
	seeds := make([]string, len(matches))
	for i, m := range matches {
		seeds[i] = m.ID
	}
	sub := g.Neighborhood(seeds, budget, q.fields["relation"])
	out := sub.Output()
	return types.KnowledgeQueryOutput{
		Definition:    types.KnowledgeQueryDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Query:         strings.TrimSpace(input),
		Budget:        budget,
		MatchCount:    len(matches),
		Matches:       matches,
		Nodes:         out.Nodes,
		Links:         out.Links,
	}
}

// Explain resolves ref to a node and returns its context card, or ok=false when
// nothing resolves.
func (g *Graph) Explain(ref string) (types.KnowledgeExplainOutput, bool) {
	id, ok := g.resolveOne(ref)
	if !ok {
		return types.KnowledgeExplainOutput{}, false
	}
	g.ensureAdj()
	n, _ := g.node(id)
	out := types.KnowledgeExplainOutput{
		Definition:    types.KnowledgeExplainDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Node:          n,
		BlastRadius:   g.blastRadius(id),
	}
	for _, e := range g.out[id] {
		out.Out = append(out.Out, g.edgeRef(e, "out", e.Target))
	}
	for _, e := range g.in[id] {
		out.In = append(out.In, g.edgeRef(e, "in", e.Source))
	}
	return out, true
}

// Path resolves both endpoints and returns the shortest connecting path (edges
// bidirectional). ok=false only when an endpoint fails to resolve; a resolved
// pair with no connection returns Found=false.
func (g *Graph) Path(a, b string) (types.KnowledgePathOutput, bool) {
	from, ok := g.resolveOne(a)
	if !ok {
		return types.KnowledgePathOutput{}, false
	}
	to, ok := g.resolveOne(b)
	if !ok {
		return types.KnowledgePathOutput{}, false
	}
	out := types.KnowledgePathOutput{
		Definition:    types.KnowledgePathDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		From:          from,
		To:            to,
	}
	steps, found := g.shortestPath(from, to)
	out.Found = found
	out.Steps = steps
	return out, true
}

// --- resolution & traversal helpers ---

// resolveOne maps a ref to a single node ID: an exact ID hit wins; otherwise the
// top-ranked match for the ref as a query (nil-safe, deterministic).
func (g *Graph) resolveOne(ref string) (string, bool) {
	if _, ok := g.node(ref); ok {
		return ref, true
	}
	matches := g.Resolve(ref, 1)
	if len(matches) == 0 {
		return "", false
	}
	return matches[0].ID, true
}

// blastRadius returns how many other nodes can reach id by walking edges in their
// natural direction. It is unbounded (walks the whole reachable component); a
// budget/cap for hub nodes on very large graphs is Phase 8 scale work.
func (g *Graph) blastRadius(id string) int {
	g.ensureAdj()
	seen := map[string]bool{id: true}
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.in[cur] { // predecessors: e.Source -> cur
			if !seen[e.Source] {
				seen[e.Source] = true
				queue = append(queue, e.Source)
			}
		}
	}
	return len(seen) - 1 // exclude the node itself
}

// shortestPath runs a BFS treating edges as undirected, reconstructing the hop
// list oriented as walked. Returns (nil, false) when unconnected.
func (g *Graph) shortestPath(from, to string) ([]types.KnowledgePathStep, bool) {
	g.ensureAdj()
	if from == to {
		return nil, true
	}
	type crumb struct {
		prev string
		edge types.KnowledgeEdge
		fwd  bool
	}
	back := map[string]crumb{from: {}}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			break
		}
		// Deterministic neighbor order: outgoing (already sorted) then incoming.
		for _, e := range g.out[cur] {
			if _, seen := back[e.Target]; !seen {
				back[e.Target] = crumb{prev: cur, edge: e, fwd: true}
				queue = append(queue, e.Target)
			}
		}
		for _, e := range g.in[cur] {
			if _, seen := back[e.Source]; !seen {
				back[e.Source] = crumb{prev: cur, edge: e, fwd: false}
				queue = append(queue, e.Source)
			}
		}
	}
	if _, ok := back[to]; !ok {
		return nil, false
	}
	var rev []types.KnowledgePathStep
	for cur := to; cur != from; {
		c := back[cur]
		rev = append(rev, types.KnowledgePathStep{From: c.prev, To: cur, Relation: c.edge.Relation, Forward: c.fwd})
		cur = c.prev
	}
	slices.Reverse(rev)
	return rev, true
}

func (g *Graph) edgeRef(e types.KnowledgeEdge, dir, other string) types.KnowledgeEdgeRef {
	n, _ := g.node(other)
	return types.KnowledgeEdgeRef{
		Relation:   e.Relation,
		Direction:  dir,
		Other:      other,
		OtherKind:  n.Kind,
		OtherLabel: n.Label,
		Provenance: e.Provenance,
	}
}

// touchesRelation reports whether id is an endpoint of any edge with one of rels.
func (g *Graph) touchesRelation(id string, rels []string) bool {
	g.ensureAdj()
	relSet := toSet(rels)
	for _, e := range g.out[id] {
		if relSet[e.Relation] {
			return true
		}
	}
	for _, e := range g.in[id] {
		if relSet[e.Relation] {
			return true
		}
	}
	return false
}

// matchesAnyProject reports whether a node ID belongs to any of the given
// projects (the project node itself or one of its targets).
func matchesAnyProject(id string, projects []string) bool {
	for _, p := range projects {
		if id == types.KindProject+":"+p || strings.HasPrefix(id, types.KindTarget+":"+p+":") {
			return true
		}
	}
	return false
}

func containsAny(hay string, needles []string) bool {
	lh := strings.ToLower(hay)
	for _, n := range needles {
		if strings.Contains(lh, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

func toSet(vals []string) map[string]bool {
	if len(vals) == 0 {
		return nil
	}
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}
