package knowledge

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// Query is deterministic name resolution and retrieval over the knowledge graph -
// no LLM. It reuses magus's existing fuzzy-finding score (interactive.LeafScore,
// which powers `magus x`/`magus where`), generalized from project paths to node
// IDs and labels. The fielded grammar here is a pragmatic subset - field:value
// matchers (kind/project/relation/id), free-text terms (AND), and negation; the
// full boolean grammar (OR/parens/wildcards) and the search.js conformance
// fixture are a later increment.

// lazyLayerKinds are the node kinds the lazily-loaded @symbols shards can hold. The shards
// are named for symbols but carry more: assembleSymbols mints a file node per definition
// and reference path, and hangs it off the dir chain containsChain builds. So a Go source
// file is reachable ONLY through this layer, while a .buzz file of the same kind sits in
// the default graph.
//
// That is why the set is enumerated rather than assumed to be {symbol}. A query naming only
// kinds outside it cannot be answered by the layer, so skipping the load is honest; a query
// naming a kind INSIDE it that skips the load reports a node that exists as absent, which is
// the one failure the verdict machinery exists to prevent.
var lazyLayerKinds = []string{types.KindSymbol, types.KindFile, types.KindDir}

// SeedsSymbols reports whether an input targets the lazily-loaded @symbols shards, so a
// caller knows to merge them into the default graph: a symbol: ID, any kind the layer holds
// (incl. wildcard), a defines/references/calls relation, or any language filter. It must
// agree with scoreNode - a match that reaches those shards without seeding here returns
// empty. Over-eager is safe: it only loads shards a later filter may discard.
func SeedsSymbols(input string) bool {
	if strings.Contains(input, types.KindSymbol+":") { // an explicit symbol: node ID
		return true
	}
	q := parseQuery(input)
	if len(q.fields["language"]) > 0 || len(q.reFields["language"]) > 0 {
		return true
	}
	for _, k := range q.fields["kind"] {
		if slices.Contains(lazyLayerKinds, k) {
			return true
		}
		if hasWildcard(k) && slices.ContainsFunc(lazyLayerKinds, func(lk string) bool { return globMatch(k, lk) }) {
			return true
		}
	}
	// A kind or id regex that could reach the lazy layer seeds it too - over-seeding is safe
	// (a later filter discards), an unseeded symbol shard silently omits every code symbol.
	if slices.ContainsFunc(lazyLayerKinds, func(lk string) bool { return matchesAnyRe(lk, q.reFields["kind"]) }) || len(q.reFields["id"]) > 0 {
		return true
	}
	for _, id := range q.fields["id"] {
		if hasWildcard(id) && wildcardCouldMatchPrefix(id, types.KindSymbol+":") {
			return true
		}
	}
	return slices.ContainsFunc(q.fields["relation"], func(r string) bool {
		// calls spans both layers: buzz function->function lives in the default graph,
		// symbol->symbol only in the symbol shards. Seeding on it loads shards a buzz-only
		// query does not need, which is the safe direction - the alternative is a
		// relation:calls query that silently omits every code symbol.
		return r == types.RelationDefines || r == types.RelationReferences || r == types.RelationCalls
	})
}

// CouldMatchSymbol reports whether a query could ever match a node in the lazily-loaded
// layer, which is a weaker question than SeedsSymbols: it asks whether that layer is
// RELEVANT, not whether it was loaded.
//
// It is DERIVED from SeedsSymbols rather than deciding the same thing a second way, and
// that is the whole safety property: relevance is a strict superset of seeding by
// construction, so widening SeedsSymbols can never leave a query that now loads the layer
// outside the set of queries allowed to caveat it. Two parallel implementations is exactly
// how `kind=file <name>` came to skip the shards AND assert a verified absence about them.
//
// The two differ for exactly the queries where an unloaded-layer caveat would mislead.
// `kind:author` returning nothing has nothing to do with code symbols, so telling the
// reader that symbols were not searched points them at a layer that could not have held
// the answer. A query naming only kinds outside lazyLayerKinds has ruled the layer out
// itself; everything else leaves it open.
func CouldMatchSymbol(input string) bool {
	if SeedsSymbols(input) {
		return true
	}
	kinds := parseQuery(input).fields["kind"]
	if len(kinds) == 0 {
		return true // no kind filter, so the layer was in scope and simply was not loaded
	}
	// Every explicit kind is outside the lazy layer (and no wildcard reaches it -
	// SeedsSymbols already returned false, so none does), which excludes it outright.
	return false
}

// wildcardCouldMatchPrefix reports whether a glob could match a string starting with
// prefix, by comparing the pattern's literal head (before the first '*') against it. A
// leading '*' matches anything, so it seeds conservatively.
func wildcardCouldMatchPrefix(pattern, prefix string) bool {
	head, _, _ := strings.Cut(pattern, "*")
	return strings.HasPrefix(prefix, head) || strings.HasPrefix(head, prefix)
}

// DefaultBudget bounds the neighborhood a query collects, so a match on a
// high-degree node cannot pull in the whole graph.
const DefaultBudget = 50

// wildcardTermScore is the flat credit a matched wildcard term contributes: a glob is
// a boolean filter, not a relevance signal, so it scores like a doc hit (1) and lets
// kindRank and ID order break ties among wildcard matches.
const wildcardTermScore = 1

// knownFields are the recognized field:value prefixes. kind/project/id/language
// constrain which nodes match; relation constrains which edges a neighborhood traverses.
var knownFields = map[string]bool{"kind": true, "project": true, "id": true, "relation": true, "language": true, "role": true}

type parsedQuery struct {
	terms     []string                    // positive free-text tokens (AND)
	negTerms  []string                    // negated free-text tokens (must not appear)
	fields    map[string][]string         // field -> allowed values (OR within, AND across)
	negFields map[string][]string         // field -> excluded values
	reFields  map[string][]*regexp.Regexp // field -> regexes its target must match (=~), OR within
	raw       string
}

// parseQuery splits a query string into free-text terms and field matchers.
// "kind=spell project=pkg/foo build kind!=op" -> terms[build], fields{kind:[spell],
// project:[pkg/foo]}, negFields{kind:[op]}. Double-quoted phrases stay one term.
func parseQuery(input string) parsedQuery {
	q := parsedQuery{fields: map[string][]string{}, negFields: map[string][]string{}, reFields: map[string][]*regexp.Regexp{}, raw: strings.TrimSpace(input)}
	for _, tok := range tokenize(input) {
		neg := false
		if strings.HasPrefix(tok, "-") && len(tok) > 1 { // compat: -kind:op / -term negation, pre-!= grammar
			neg, tok = true, tok[1:]
		}
		if field, op, val, ok := parseMatcher(tok); ok {
			switch {
			case op == matchRe && field == "relation":
				// relation is an edge-traversal filter, not a node string, so it has no regex
				// target; the bare relation name after =~ is what a user meant, so match it exactly.
				q.fields[field] = append(q.fields[field], val)
			case op == matchRe:
				if re, err := regexp.Compile(val); err == nil {
					q.reFields[field] = append(q.reFields[field], re)
				} else {
					q.fields[field] = append(q.fields[field], val) // a malformed regex degrades to a literal rather than an error
				}
			case op == matchNe || neg:
				q.negFields[field] = append(q.negFields[field], val)
			default:
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

type matchOp int

const (
	matchEq matchOp = iota // field=value or field:value (compat)
	matchNe                // field!=value
	matchRe                // field=~regex
)

// parseMatcher recognizes a field matcher: "<knownField><op><value>". The operators are
// checked most-specific first so "!=" and "=~" win over a bare "=". The colon is the pre-!=
// spelling of "=", kept as a compat alias.
func parseMatcher(tok string) (field string, op matchOp, value string, ok bool) {
	for _, m := range []struct {
		sep string
		op  matchOp
	}{{"!=", matchNe}, {"=~", matchRe}, {"=", matchEq}, {":", matchEq}} {
		if i := strings.Index(tok, m.sep); i > 0 {
			f := strings.ToLower(tok[:i])
			if knownFields[f] {
				return f, m.op, tok[i+len(m.sep):], true
			}
		}
	}
	return "", matchEq, "", false
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
		m := types.KnowledgeMatch{ID: id, Kind: n.Kind, Label: n.Label, Score: score}
		// Prose whose subject moved on is LABELED, never reordered - see stalenessLabel for
		// why ranking on it is the wrong repair. The label travels with the match so a caller
		// can show "400 days behind its subject" beside the result and let the reader judge.
		m.Staleness, m.OutrunDays = stalenessLabel(n.Attrs)
		matches = append(matches, m)
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
// edge, so `magus query relation=uses` still resolves seeds.
func (g *Graph) scoreNode(n types.KnowledgeNode, id string, q parsedQuery) (int, bool) {
	if vals, ok := q.fields["kind"]; ok && !matchesKind(n.Kind, vals) {
		return 0, false
	}
	if vals := q.negFields["kind"]; matchesKind(n.Kind, vals) {
		return 0, false
	}
	if res := q.reFields["kind"]; len(res) > 0 && !matchesAnyRe(n.Kind, res) {
		return 0, false
	}
	if vals, ok := q.fields["project"]; ok {
		proj, owned := g.projectOf(n, id)
		if !owned || !matchesProject(proj, vals) {
			return 0, false
		}
	}
	if vals := q.negFields["project"]; len(vals) > 0 {
		if proj, owned := g.projectOf(n, id); owned && matchesProject(proj, vals) {
			return 0, false
		}
	}
	if res := q.reFields["project"]; len(res) > 0 {
		if proj, owned := g.projectOf(n, id); !owned || !matchesAnyRe(proj, res) {
			return 0, false
		}
	}
	if vals, ok := q.fields["id"]; ok && !containsAny(id, vals) {
		return 0, false
	}
	if vals := q.negFields["id"]; containsAny(id, vals) {
		return 0, false
	}
	if res := q.reFields["id"]; len(res) > 0 && !matchesAnyRe(id, res) {
		return 0, false
	}
	// language filters on the node's language attr (set on file and symbol nodes), so
	// `language:go` groups every source file and symbol of a language regardless of
	// whether magus's AST walk or a foreign SCIP index produced it. A node without the
	// attr never matches a positive language constraint.
	if vals, ok := q.fields["language"]; ok && !slices.Contains(vals, n.Attrs["language"]) {
		return 0, false
	}
	if vals := q.negFields["language"]; slices.Contains(vals, n.Attrs["language"]) {
		return 0, false
	}
	if res := q.reFields["language"]; len(res) > 0 && !matchesAnyRe(n.Attrs["language"], res) {
		return 0, false
	}
	// role filters on the doc-classification attr (readme/agent/changelog/...), so
	// `kind=doc role=agent` finds the agent-instruction files. A node without the attr
	// never matches a positive role constraint.
	if vals, ok := q.fields["role"]; ok && !slices.Contains(vals, n.Attrs[attrRole]) {
		return 0, false
	}
	if vals := q.negFields["role"]; slices.Contains(vals, n.Attrs[attrRole]) {
		return 0, false
	}
	if res := q.reFields["role"]; len(res) > 0 && !matchesAnyRe(n.Attrs[attrRole], res) {
		return 0, false
	}

	// Negated free text must not appear anywhere in the node's text (a wildcard term
	// excludes any node whose ID or label matches the glob).
	hay := strings.ToLower(id + " " + n.Label + " " + n.Doc)
	for _, t := range q.negTerms {
		if hasWildcard(t) {
			if globMatch(t, id) || globMatch(t, n.Label) {
				return 0, false
			}
		} else if strings.Contains(hay, strings.ToLower(t)) {
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
	// A wildcard term is a boolean glob filter (no fuzzy score), matched against ID and
	// label - NOT the doc (prose is not glob-shaped, unlike the plain-substring path
	// which does search the doc). It contributes a flat credit so it ranks like a doc
	// hit, not a leaf match.
	total := 0
	for _, t := range q.terms {
		if hasWildcard(t) {
			if !globMatch(t, id) && !globMatch(t, n.Label) {
				return 0, false
			}
			total += wildcardTermScore
			continue
		}
		best := max(interactive.LeafScore(id, t), interactive.LeafScore(n.Label, t))
		if best <= 0 {
			// LeafScore anchors on the leaf and charges 10 per '/', so a non-leaf hit
			// inside a slash-heavy ID (kind:function docs -> function:docs/f.buzz:x)
			// comes back zero or negative. A substring hit anywhere in the ID or doc still
			// matches, with the flat doc-hit credit, instead of dropping the node.
			switch lt := strings.ToLower(t); {
			case strings.Contains(strings.ToLower(id), lt),
				strings.Contains(strings.ToLower(n.Doc), lt):
				best = 1
			default:
				return 0, false
			}
		}
		total += best
	}
	return total + kindRank(n.Kind), true
}

// kindRank biases resolution toward primary domain entities over source-level nodes on
// a text-relevance tie, so a bare `explain build` resolves the target, not its function.
// The bonus only breaks ties; it never outranks a stronger text match.
func kindRank(kind string) int {
	switch kind {
	case types.KindProject, types.KindTarget, types.KindSpell, types.KindOp,
		types.KindTool, types.KindCharm, types.KindModule, types.KindMethod,
		types.KindDiagnostic, types.KindAuthor, types.KindOwner, types.KindPackage:
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
	// Seeds are ranked (best match first); cap them at the budget so a query that
	// matches thousands of nodes (e.g. a common term across every project) returns
	// the top budget matches, not the whole graph. Without this the budget only
	// bounds neighborhood expansion, not the seed set - so the node count could
	// exceed the documented "max nodes = budget" contract.
	for _, s := range seeds {
		if len(visited) >= budget {
			break
		}
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
// neighborhood subgraph, bounded by budget. It is the unpaged view: every match, in
// one response - QueryPage with a zero offset and no limit, so the two cannot drift.
func (g *Graph) Query(input string, budget int) types.KnowledgeQueryOutput {
	return g.QueryPage(input, budget, 0, 0)
}

// QueryPage is Query with a match window: it returns the total MatchCount but only
// the matches in [offset, offset+limit) (limit <= 0 means "to the end"), and builds
// the neighborhood from that page's seeds so a page is a self-contained result. It
// is the substrate for MCP pagination, where a large match set (symbol references)
// must be returned across several bounded responses. offset past the end yields an
// empty page with the true MatchCount, so a caller can stop.
func (g *Graph) QueryPage(input string, budget, offset, limit int) types.KnowledgeQueryOutput {
	if budget <= 0 {
		budget = DefaultBudget
	}
	if offset < 0 {
		offset = 0
	}
	q := parseQuery(input)
	matches := g.Resolve(input, 0)
	total := len(matches)

	var page []types.KnowledgeMatch
	if offset < total {
		page = matches[offset:]
	}
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}

	seeds := make([]string, len(page))
	for i, m := range page {
		seeds[i] = m.ID
	}
	sub := g.Neighborhood(seeds, budget, q.fields["relation"])
	out := sub.Output()
	return types.KnowledgeQueryOutput{
		Definition:    types.KnowledgeQueryDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Query:         strings.TrimSpace(input),
		Budget:        budget,
		MatchCount:    total,
		Offset:        offset,
		Matches:       page,
		Nodes:         out.Nodes,
		Links:         out.Links,
	}
}

// Select resolves the input to seeds and returns the induced neighborhood as a
// node-link export (the emit side of `magus graph export --select`), sharing the
// seed+neighborhood logic with Query so graph and query stay one substrate. An
// input that resolves to nothing yields an empty graph.
func (g *Graph) Select(input string, budget int) types.KnowledgeGraphOutput {
	if budget <= 0 {
		budget = DefaultBudget
	}
	q := parseQuery(input)
	matches := g.Resolve(input, 0)
	seeds := make([]string, len(matches))
	for i, m := range matches {
		seeds[i] = m.ID
	}
	return g.Neighborhood(seeds, budget, q.fields["relation"]).Output()
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
		out.Out = append(out.Out, g.edgeRef(e, types.EdgeOut, e.Target))
	}
	for _, e := range g.in[id] {
		out.In = append(out.In, g.edgeRef(e, types.EdgeIn, e.Source))
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

// Refs resolves ref to a node and lists where it is defined and every file that
// references it, as occurrence-shaped sites (file + count + lines) rather than a
// node-link neighborhood. The reference counts and lines come from the SCIP-ingested
// `references` edges' provenance; `defines` edges give the definition file(s). Sites
// are sorted by file for determinism. ok=false when ref does not resolve.
func (g *Graph) Refs(ref string) (types.KnowledgeRefsOutput, bool) {
	id, ok := g.resolveSymbol(ref)
	if !ok {
		return types.KnowledgeRefsOutput{}, false
	}
	g.ensureAdj()
	n, _ := g.node(id)
	out := types.KnowledgeRefsOutput{
		Definition:    types.KnowledgeRefsDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Symbol:        id,
		Label:         n.Label,
	}
	// The definition's line lives on the symbol node's Source ("path:line"); the
	// defines edge provenance carries only the path. Surface the line on the def site
	// so an agent can jump straight to the definition and edit it without reading the
	// whole file - the same file:line refs already gives for references.
	defFile, defLine, _ := splitPathLine(n.Source)
	for _, e := range g.in[id] {
		file := strings.TrimPrefix(e.Source, types.KindFile+":")
		switch e.Relation {
		case types.RelationDefines:
			site := types.KnowledgeRefSite{File: file}
			if defLine > 0 && file == defFile {
				site.Lines = []int{defLine}
			}
			out.Defs = append(out.Defs, site)
		case types.RelationReferences:
			// Only a SCIP-ingested reference edge carries the count/lines provenance;
			// skip any other references edge into this node rather than emit a phantom
			// count-0 site that would inflate FileCount.
			count, lines, ok := parseRefProvenance(e.Provenance)
			if !ok {
				continue
			}
			out.Refs = append(out.Refs, types.KnowledgeRefSite{File: file, Count: count, Lines: lines})
			out.RefCount += count
		}
	}
	slices.SortFunc(out.Defs, func(a, b types.KnowledgeRefSite) int { return cmp.Compare(a.File, b.File) })
	slices.SortFunc(out.Refs, func(a, b types.KnowledgeRefSite) int { return cmp.Compare(a.File, b.File) })
	out.FileCount = len(out.Refs)
	return out, true
}

// splitPathLine splits a "path:line" provenance string (as carried on a symbol node's
// Source) into the path and its 1-based line, keying on the LAST colon so a drive
// letter or scheme in the path does not confuse it. ok is false (path/line zero) when
// there is no trailing ":<int>" to parse.
func splitPathLine(s string) (path string, line int, ok bool) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, false
	}
	return s[:i], n, true
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

// HasSymbols reports whether the graph holds any ingested code symbol node. refs
// and a symbol-seeded query load the @symbols shards lazily, so when this returns
// false after that load, no SCIP index has been ingested at all. Callers use it to
// tell "no index built" apart from "index built, but this symbol is absent": the
// former is fixed by building the index, the latter by correcting the symbol.
func (g *Graph) HasSymbols() bool {
	for _, n := range g.nodes {
		if n.Kind == types.KindSymbol {
			return true
		}
	}
	return false
}

// resolveSymbol maps a ref to a SYMBOL node specifically, so `magus refs Foo` resolves
// to the ingested symbol rather than a same-named buzz function or target. An exact
// symbol-ID hit wins; otherwise ref is resolved as-is and the highest-ranked SYMBOL
// match is taken. The ref is NOT concatenated into the query grammar (that would let
// a ref like "kind:file x" widen the kind filter and resolve a non-symbol); it is
// resolved plainly and filtered by kind here. No symbol match yields ok=false.
func (g *Graph) resolveSymbol(ref string) (string, bool) {
	if n, ok := g.node(ref); ok && n.Kind == types.KindSymbol {
		return ref, true
	}
	for _, m := range g.Resolve(ref, 0) {
		if m.Kind == types.KindSymbol {
			return m.ID, true
		}
	}
	return "", false
}

// Dependents returns every node that transitively DEPENDS ON id, as ids, nearest first.
//
// Deliberately narrower than blastRadius below, which is a different question wearing a similar
// name: blastRadius counts everything that reaches a node by ANY relation, so a doc that
// documents a spell is in it. This walks `depends_on` alone, which is what "what rebuilds if I
// change this" means - and the two diverge hard. Nothing depends_on a spell (a target USES one),
// so a spell's blastRadius is in the hundreds while its Dependents is empty, and both are
// correct answers to their own question.
//
// Ids rather than a count, because the caller highlights them. Unbudgeted: the result is bounded
// by the depends_on subgraph, which is the build DAG rather than the whole knowledge graph.
func (g *Graph) Dependents(id string) []string {
	g.ensureAdj()
	if _, ok := g.node(id); !ok {
		return nil
	}
	seen := map[string]bool{id: true}
	out := []string{}
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range g.in[cur] { // predecessors: e.Source depends_on cur
			if e.Relation != types.RelationDependsOn || seen[e.Source] {
				continue
			}
			seen[e.Source] = true
			out = append(out, e.Source)
			queue = append(queue, e.Source)
		}
	}
	return out
}

// blastRadius returns how many other nodes can reach id by walking edges in their
// natural direction, over ANY relation. It is unbounded (walks the whole reachable
// component); a budget/cap for hub nodes on very large graphs is Phase 8 scale work.
//
// A REACH measure, not a rebuild one - see Dependents above, which answers "what rebuilds"
// over depends_on alone. The two diverge to the point of contradiction on a spell.
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

func (g *Graph) edgeRef(e types.KnowledgeEdge, dir types.EdgeDirection, other string) types.KnowledgeEdgeRef {
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

// matchesProject reports whether a node's owning project path matches any of the
// given values: a value with '*' by glob, otherwise by exact path (project paths
// are a small fixed set, so substring would over-match).
func matchesProject(proj string, vals []string) bool {
	for _, v := range vals {
		if hasWildcard(v) {
			if globMatch(v, proj) {
				return true
			}
		} else if proj == v {
			return true
		}
	}
	return false
}

// projectOf resolves the workspace-relative project path owning a node: the path
// itself for a project node, the declaring project for a target, and for every
// other kind the longest project path prefixing its source (files, functions,
// docs, and symbols all carry one). This is what makes `project:web kind:function`
// select the functions INSIDE web, not just the project node and its targets.
// A node with no source (e.g. an unresolved import) is owned by nothing.
func (g *Graph) projectOf(n types.KnowledgeNode, id string) (string, bool) {
	if p, ok := projectPathOf(id); ok {
		return p, true
	}
	src := n.Source
	if i := strings.IndexByte(src, ':'); i >= 0 {
		src = src[:i] // strip a :line suffix
	}
	if src == "" {
		return "", false
	}
	for _, p := range g.projectPaths() {
		if p == "." || src == p || strings.HasPrefix(src, p+"/") {
			return p, true
		}
	}
	return "", false
}

// projectPaths returns every project node's path, longest first so projectOf's
// prefix scan resolves nested projects before the root "." catch-all. Built
// lazily and invalidated with the adjacency indices.
//
// Guarded by projMu for the same reason ensureAdj is guarded by adjMu: this runs
// on the query path against a *Graph the daemon's warm graph can hand to several
// concurrent requests, so a bare nil check would let two first-queries race
// writing g.projPaths - a concurrent map/slice write that crashes the process.
func (g *Graph) projectPaths() []string {
	g.projMu.Lock()
	defer g.projMu.Unlock()
	if g.projPaths != nil {
		return g.projPaths
	}
	var paths []string
	for id := range g.nodes {
		if p, ok := strings.CutPrefix(id, types.KindProject+":"); ok {
			paths = append(paths, p)
		}
	}
	// Longest first; ties break lexically so the order is deterministic.
	slices.SortFunc(paths, func(a, b string) int {
		if c := cmp.Compare(len(b), len(a)); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	g.projPaths = paths
	return g.projPaths
}

// containsAny reports whether hay matches any needle: a needle with a '*' matches by
// glob, otherwise by case-insensitive substring (the pre-wildcard behavior).
func containsAny(hay string, needles []string) bool {
	lh := strings.ToLower(hay)
	for _, n := range needles {
		if hasWildcard(n) {
			if globMatch(n, hay) {
				return true
			}
		} else if strings.Contains(lh, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// hasWildcard reports whether a term or field value uses the '*' glob metacharacter.
func hasWildcard(s string) bool { return strings.IndexByte(s, '*') >= 0 }

// matchesAnyRe reports whether s matches at least one of the regexes (OR within a field's
// =~ values). An empty target never matches, so a =~ on a node lacking the attr excludes it.
func matchesAnyRe(s string, res []*regexp.Regexp) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// globMatch reports whether s matches a case-insensitive glob where '*' matches any run
// of characters, separators ('/', ':') included - node IDs are full of them, so path.Match's
// slash-significance would surprise. No '*' means exact match. Middle segments match
// leftmost without backtracking, which is correct because the surrounding '*' absorb any slack.
func globMatch(pattern, s string) bool {
	p, str := strings.ToLower(pattern), strings.ToLower(s)
	parts := strings.Split(p, "*")
	if len(parts) == 1 {
		return p == str
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

// matchesKind reports whether kind matches any of vals: a val with '*' by glob, else
// by exact match (kinds are a small fixed set, so substring would over-match).
func matchesKind(kind string, vals []string) bool {
	for _, v := range vals {
		if hasWildcard(v) {
			if globMatch(v, kind) {
				return true
			}
		} else if v == kind {
			return true
		}
	}
	return false
}

// projectPathOf returns the workspace-relative project path a node ID belongs to: the
// path itself for a project node, or the owning project for a target node.
func projectPathOf(id string) (string, bool) {
	if p, ok := strings.CutPrefix(id, types.KindProject+":"); ok {
		return p, true
	}
	return projectOfTargetID(id)
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
