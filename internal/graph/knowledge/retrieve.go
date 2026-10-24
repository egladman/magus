package knowledge

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/textrank"
	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/types"
)

// Names of the ranked lists Retrieve fuses, as KnowledgeListRank and fusion.lists name them.
const (
	listLexical = "lexical"
	listExact   = "exact"
	listGraph   = "graph"
	listEmbed   = "embed"
)

// pullTable is the command that fills the cache embedtable.Load reads.
const pullTable = "magus graph pull -table"

// RetrieveOptions are everything a ranked answer depends on besides the graph and the
// query. Reader is the reader's context as workspace paths (changed files, a lease's write
// paths); each one that names a file or dir node seeds the graph list. Lexical is the
// stored lexical index; nil ranks over one built in memory from the graph's nodes, which
// the ranking's Note says. Table loads the embedding table the embed list ranks by; nil
// means no table. A nil Table or a Table error skips the embed list, which the ranking's
// Skipped says; Retrieve reads no file of its own and never pulls.
// Limit truncates the matches; 0 keeps every one.
type RetrieveOptions struct {
	Reader  []string
	Weights textrank.Weights
	Lexical *textrank.Index
	Table   func() (*embed.Table, error)
	Limit   int
}

type (
	readerKey struct{}
	tableKey  struct{}
)

// WithTable returns ctx carrying the loader Store.QuerySymbols passes as
// RetrieveOptions.Table. Its callers' signatures carry no RetrieveOptions, so the loader
// rides ctx the way WithReader's paths do.
func WithTable(ctx context.Context, load func() (*embed.Table, error)) context.Context {
	return context.WithValue(ctx, tableKey{}, load)
}

// tableOn returns the loader WithTable put on ctx, nil when there is none.
func tableOn(ctx context.Context) func() (*embed.Table, error) {
	load, _ := ctx.Value(tableKey{}).(func() (*embed.Table, error))
	return load
}

// WithReader returns ctx carrying the reader's context paths, which Store.QuerySymbols
// ranks under. Its callers' signatures carry no RetrieveOptions, so the paths ride ctx.
func WithReader(ctx context.Context, paths []string) context.Context {
	return context.WithValue(ctx, readerKey{}, paths)
}

// readersOn returns the reader paths WithReader put on ctx, nil when there are none.
func readersOn(ctx context.Context) []string {
	paths, _ := ctx.Value(readerKey{}).([]string)
	return paths
}

// ShippedWeights returns the committed weight file. It panics when the embedded file does
// not load, which TestLoadWeightsShippedFile rules out for any build that passed its tests.
var ShippedWeights = sync.OnceValue(func() textrank.Weights {
	w, err := textrank.LoadWeights()
	if err != nil {
		panic(fmt.Sprintf("knowledge: %v", err))
	}
	return w
})

// Retrieve ranks the nodes matching input. Field matchers filter exactly as they always
// have. Free-text terms are split by embed.Tokens and scored by BM25F, any term
// matching (ranked OR): a match holding fewer of the query's tokens than it has is
// labeled partial by Why.Matched and Why.Of, never dropped. The lexical score is scaled
// by the node kind's prior. A personalized PageRank seeded by the top lexical hits and the
// reader's paths reranks the same matches, and so does the cosine of each match's
// embedding with the query's under opts.Table. The lists fuse by weighted reciprocal
// rank. Ties break by kindRank, then id, so the answer is a pure function of the graph,
// input and opts.
//
// A query with no free text ranks its matches by prior, then id. An error means opts
// carries weights the rankers refuse.
func (g *Graph) Retrieve(input string, opts RetrieveOptions) ([]types.KnowledgeMatch, types.KnowledgeRanking, error) {
	ranking := types.KnowledgeRanking{Weights: opts.Weights.Digest(), Reader: opts.Reader}
	q := g.normalizePaths(parseQuery(input))
	ix := g.ensureSearchIndex()
	cq := g.compileQuery(q)
	tokens, need := queryTokens(cq.terms)

	var matches []types.KnowledgeMatch
	if len(tokens) == 0 {
		matches = g.filterOnly(ix, cq, opts.Weights)
	} else {
		lex := opts.Lexical
		if lex == nil {
			lex = ix.lexicalIndex()
			ranking.Note = fmt.Sprintf("no stored lexical index: ranked over one built in memory from %d graph nodes, without section bodies", lex.Len())
		}
		var err error
		matches, err = g.rankText(ix, cq, tokens, need, exactPhrase(cq.terms), lex, opts, &ranking)
		if err != nil {
			return nil, ranking, err
		}
	}
	if opts.Limit > 0 && len(matches) > opts.Limit {
		matches = matches[:opts.Limit]
	}
	return matches, ranking, nil
}

// queryTokens returns the distinct tokens Retrieve scores, every plain term's in query
// order, and the ones a full match must hold. A wildcard term is a filter, not a scored
// term. A path term's tokens score, but admits already holds a node to the whole path,
// a stricter test than its words, so a full match need not hold them as tokens.
func queryTokens(terms []compiledTerm) (score, need []string) {
	seen := map[string]bool{}
	needed := map[string]bool{}
	for _, t := range terms {
		if t.glob != nil {
			continue
		}
		for _, tok := range embed.Tokens(t.lc) {
			if !seen[tok] {
				seen[tok] = true
				score = append(score, tok)
			}
			if !t.path && !needed[tok] {
				needed[tok] = true
				need = append(need, tok)
			}
		}
	}
	return score, need
}

// termsHeld counts the tokens of need that h holds.
func termsHeld(h textrank.Hit, need []string) int {
	held := map[string]bool{}
	for _, ts := range h.Terms {
		held[ts.Term] = true
	}
	n := 0
	for _, tok := range need {
		if held[tok] {
			n++
		}
	}
	return n
}

// filterOnly answers a query with no scored term: every node the filters admit, by prior,
// then kindRank, then id.
func (g *Graph) filterOnly(ix *searchIndex, cq *compiledQuery, w textrank.Weights) []types.KnowledgeMatch {
	var matches []types.KnowledgeMatch
	add := func(i int) {
		if !g.admits(ix, i, cq) {
			return
		}
		prior := g.prior(&ix.entries[i].node, w)
		matches = append(matches, newMatch(&ix.entries[i].node, prior, &types.KnowledgeMatchWhy{Prior: prior}))
	}
	if len(cq.kindPos) > 0 {
		// A kind= filter names a handful of kinds; the partition skips every other node
		// without testing it.
		for kind, positions := range ix.byKind {
			if !matchesKind(kind, cq.kindPos) {
				continue
			}
			for _, i := range positions {
				add(int(i))
			}
		}
	} else {
		for i := range ix.entries {
			add(i)
		}
	}
	slices.SortFunc(matches, func(a, b types.KnowledgeMatch) int {
		if a.Why.Prior != b.Why.Prior {
			return cmp.Compare(b.Why.Prior, a.Why.Prior)
		}
		return cmp.Or(cmp.Compare(kindRank(b.Kind), kindRank(a.Kind)), cmp.Compare(a.ID, b.ID))
	})
	return matches
}

// candidate is one admitted lexical hit with its prior-scaled score.
type candidate struct {
	pos   int
	hit   textrank.Hit
	prior float64
	score float64
}

// rankText scores tokens over lex, keeps the hits the filters admit, and fuses their
// lexical order with the ones named phrase exactly and a personalized PageRank over the
// same matches. Why counts a match's tokens against need.
func (g *Graph) rankText(ix *searchIndex, cq *compiledQuery, tokens, need []string, phrase string, lex *textrank.Index, opts RetrieveOptions, ranking *types.KnowledgeRanking) ([]types.KnowledgeMatch, error) {
	var cands []candidate
	for _, h := range lex.Score(tokens, opts.Weights.BM25F) {
		pos, ok := ix.byID[h.ID]
		if !ok || !g.admits(ix, int(pos), cq) {
			continue
		}
		prior := g.prior(&ix.entries[pos].node, opts.Weights)
		cands = append(cands, candidate{pos: int(pos), hit: h, prior: prior, score: h.Score * prior})
	}
	if len(cands) == 0 {
		return nil, nil
	}
	// kindRank breaks an exact tie, and only a tie: a bare `explain build` resolves the
	// target over the magusfile function of the same name.
	slices.SortFunc(cands, func(a, b candidate) int {
		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		ka, kb := kindRank(ix.entries[a.pos].node.Kind), kindRank(ix.entries[b.pos].node.Kind)
		return cmp.Or(cmp.Compare(kb, ka), cmp.Compare(a.hit.ID, b.hit.ID))
	})

	why := func(c *candidate) *types.KnowledgeMatchWhy {
		return &types.KnowledgeMatchWhy{Matched: termsHeld(c.hit, need), Of: len(need), Terms: termScores(c.hit.Terms), Prior: c.prior}
	}
	seedsOf := map[string][]string{}
	var graph *textrank.RankedList
	if seeds := g.readerSeeds(opts.Reader); len(seeds) > 0 {
		var err error
		if graph, err = g.graphList(cands, opts, seeds, seedsOf); err != nil {
			return nil, err
		}
	} else {
		ranking.Skipped = append(ranking.Skipped, listGraph+": no reader path names a node")
	}
	matches := make([]types.KnowledgeMatch, 0, len(cands))
	lexWeight := listWeight(opts.Weights, listLexical)
	exact := exactList(ix, cands, phrase, opts.Weights)
	emb, skipped := embedList(ix, cands, tokens, opts)
	if emb == nil {
		ranking.Skipped = append(ranking.Skipped, fmt.Sprintf("%s: %s", listEmbed, skipped))
	}
	if graph == nil && exact == nil && emb == nil {
		// One list fuses to its own order; the score is its reciprocal rank term.
		for i := range cands {
			c := &cands[i]
			w := why(c)
			w.Lists = []types.KnowledgeListRank{{List: listLexical, Rank: i + 1}}
			matches = append(matches, newMatch(&ix.entries[c.pos].node, lexWeight/(opts.Weights.Fusion.K+float64(i+1)), w))
		}
		return matches, nil
	}

	lexical := make([]string, len(cands))
	byID := make(map[string]*candidate, len(cands))
	for i := range cands {
		lexical[i] = cands[i].hit.ID
		byID[cands[i].hit.ID] = &cands[i]
	}
	lists := []textrank.RankedList{{Name: listLexical, Weight: lexWeight, IDs: lexical}}
	for _, l := range []*textrank.RankedList{exact, graph, emb} {
		if l != nil {
			lists = append(lists, *l)
		}
	}
	fused, err := textrank.Fuse(lists, opts.Weights.Fusion.K)
	if err != nil {
		return nil, err
	}
	for _, f := range fused {
		c := byID[f.ID]
		w := why(c)
		w.Seeds = seedsOf[f.ID]
		for _, l := range lists {
			if rank, ok := f.Ranks[l.Name]; ok {
				w.Lists = append(w.Lists, types.KnowledgeListRank{List: l.Name, Rank: rank})
			}
		}
		matches = append(matches, newMatch(&ix.entries[c.pos].node, f.Score, w))
	}
	return matches, nil
}

// exactList is the cands, in lexical order, whose label, last id segment or whole id is
// phrase, the query's plain terms as typed: the node a reader names outright. BM25F alone
// lets a node whose doc repeats a word outrank the one called by it, so `explain build`
// would name an op that mentions build over the target named build. nil when none is.
func exactList(ix *searchIndex, cands []candidate, phrase string, w textrank.Weights) *textrank.RankedList {
	if phrase == "" {
		return nil
	}
	var ids []string
	for _, c := range cands {
		e := &ix.entries[c.pos]
		if e.lcLabel == phrase || e.lcID == phrase || e.lcID[strings.LastIndexAny(e.lcID, "/:")+1:] == phrase {
			ids = append(ids, c.hit.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return &textrank.RankedList{Name: listExact, Weight: listWeight(w, listExact), IDs: ids}
}

// embedList is the cands with an embedding under opts.Table, by its cosine with the
// query's embedding of tokens, ties in lexical order. With no list it returns why: the
// table is nil or did not load, or the query or every cand holds no token of its vocabulary.
func embedList(ix *searchIndex, cands []candidate, tokens []string, opts RetrieveOptions) (*textrank.RankedList, string) {
	if opts.Table == nil {
		return nil, "no table loaded: run `" + pullTable + "`"
	}
	table, err := opts.Table()
	if err != nil {
		reason := err.Error()
		if !strings.Contains(reason, pullTable) {
			reason += ": run `" + pullTable + "` to replace it"
		}
		return nil, reason
	}
	q, ok := table.Embed([]embed.Field{{Tokens: tokens, Weight: 1}})
	if !ok {
		return nil, "no query token is in the table's vocabulary"
	}
	docs := make([][]embed.Field, len(cands))
	for i, c := range cands {
		fields := lexicalDocument(ix.entries[c.pos].node, "").Fields
		for _, name := range slices.Sorted(maps.Keys(fields)) {
			docs[i] = append(docs[i], embed.Field{Tokens: fields[name], Weight: 1})
		}
	}
	hits := embed.NewIndex(table, docs).Search(q, embed.SearchOptions{Depth: len(cands)})
	if len(hits) == 0 {
		return nil, "no match holds a token of the table's vocabulary"
	}
	list := &textrank.RankedList{Name: listEmbed, Weight: listWeight(opts.Weights, listEmbed), IDs: make([]string, len(hits))}
	for i, h := range hits {
		list.IDs[i] = cands[h.Doc].hit.ID
	}
	return list, ""
}

// exactPhrase is the query's plain free-text terms, lowercased and joined by a space, ""
// when a wildcard or path term is among them: neither names one node outright.
func exactPhrase(terms []compiledTerm) string {
	parts := make([]string, 0, len(terms))
	for _, t := range terms {
		if t.glob != nil || t.path {
			return ""
		}
		parts = append(parts, t.lc)
	}
	return strings.Join(parts, " ")
}

// graphList ranks cands by a personalized PageRank seeded by every reader path that names
// a node and by the top lexical hits, each weighted by its score relative to the best.
// The fusion constant bounds the lexical seeds: a lexical rank past k carries under half
// the weight of rank 1 in the fusion, so it is no stronger as a seed.
//
// seeds holds the reader seeds, which graphList adds the lexical seeds to.
func (g *Graph) graphList(cands []candidate, opts RetrieveOptions, seeds map[string]float64, seedsOf map[string][]string) (*textrank.RankedList, error) {
	if top := cands[0].score; top > 0 {
		for _, c := range cands[:min(len(cands), max(1, int(opts.Weights.Fusion.K)))] {
			seeds[c.hit.ID] = c.score / top
		}
	}
	res, err := g.PersonalizedPageRank(seeds, opts.Weights.PPR)
	if err != nil {
		return nil, err
	}
	in := make(map[string]bool, len(cands))
	for _, c := range cands {
		in[c.hit.ID] = true
	}
	list := &textrank.RankedList{Name: listGraph, Weight: listWeight(opts.Weights, listGraph)}
	for _, s := range res.Scores {
		if in[s.ID] {
			list.IDs = append(list.IDs, s.ID)
			seedsOf[s.ID] = s.Seeds
		}
	}
	return list, nil
}

// readerSeeds weighs 1 each file and directory node a reader path names. With none there
// is no graph list: seeded by the lexical hits alone, the walk reranked them to no gain on
// the retrieval eval (MRR .260 either way over 2548 judgments) at four times the explain
// latency.
func (g *Graph) readerSeeds(reader []string) map[string]float64 {
	seeds := map[string]float64{}
	for _, p := range reader {
		for _, id := range []string{fileID(p), dirID(p)} {
			if _, ok := g.node(id); ok {
				seeds[id] = 1
			}
		}
	}
	return seeds
}

// listWeight is the fusion weight fusion.lists names for list. A list the file does not
// name weighs 1, the unweighted reciprocal rank fusion of Cormack et al. 2009.
func listWeight(w textrank.Weights, list string) float64 {
	if v, ok := w.Fusion.Lists[list]; ok {
		return v
	}
	return 1
}

// prior is the multiplicative prior on n's lexical score: kind_prior for its kind, 1 for a
// kind the file does not name.
func (g *Graph) prior(n *types.KnowledgeNode, w textrank.Weights) float64 {
	p, ok := w.KindPrior[n.Kind]
	if !ok {
		p = 1
	}
	return p
}

// termScores is a hit's term contributions, largest first, ties by term then field.
func termScores(ts []textrank.TermScore) []types.KnowledgeTermScore {
	out := make([]types.KnowledgeTermScore, len(ts))
	for i, t := range ts {
		out[i] = types.KnowledgeTermScore{Term: t.Term, Field: t.Field, Score: t.Score}
	}
	slices.SortFunc(out, func(a, b types.KnowledgeTermScore) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score)
		}
		return cmp.Or(cmp.Compare(a.Term, b.Term), cmp.Compare(a.Field, b.Field))
	})
	return out
}

// newMatch is n as a match scoring score, which KnowledgeMatch.Score carries scaled by
// 10000 and rounded.
func newMatch(n *types.KnowledgeNode, score float64, why *types.KnowledgeMatchWhy) types.KnowledgeMatch {
	m := types.KnowledgeMatch{ID: n.ID, Kind: n.Kind, Label: n.Label, Score: int(math.Round(score * 10000)), Why: why}
	// Prose whose subject moved on is LABELED, never reordered; see stalenessLabel.
	m.Staleness, m.OutrunDays = stalenessLabel(n.Attrs)
	return m
}

// Root is the workspace root g's paths are relative to, "" for a graph built without one.
func (g *Graph) Root() string { return g.root }

// QueryRanked is QueryPage under opts: the matches in [offset, offset+limit), their
// neighborhood within budget, and the ranking record.
func (g *Graph) QueryRanked(input string, budget, offset, limit int, opts RetrieveOptions) (types.KnowledgeQueryOutput, error) {
	if budget <= 0 {
		budget = DefaultBudget
	}
	offset = max(offset, 0)
	opts.Limit = 0
	matches, ranking, err := g.Retrieve(input, opts)
	if err != nil {
		return types.KnowledgeQueryOutput{}, err
	}
	var page []types.KnowledgeMatch
	if offset < len(matches) {
		page = matches[offset:]
	}
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}
	seeds := make([]string, len(page))
	for i, m := range page {
		seeds[i] = m.ID
	}
	out := g.Neighborhood(seeds, budget, parseQuery(input).fields["relation"]).Output()
	return types.KnowledgeQueryOutput{
		Definition:    types.KnowledgeQueryDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Query:         strings.TrimSpace(input),
		Budget:        budget,
		MatchCount:    len(matches),
		Offset:        offset,
		Matches:       page,
		Nodes:         out.Nodes,
		Links:         out.Links,
		Ranking:       &ranking,
	}, nil
}
