package mcp

import (
	"context"
	"errors"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// The knowledge-graph retrieval tools (query/explain/path/stats) mirror the CLI
// verbs and sit on the same cache-first substrate. Each resolves the graph then
// answers. In the daemon, where a watcher keeps a warm graph invalidated on source
// changes (see Magus.WatchKnowledgeGraph, started in startMCPWithDaemon), this
// answers from memory without re-parsing magusfiles; otherwise it rebuilds
// cache-first. Either way it is fresh.

// graphResolver is the slice of the workspace the knowledge-graph tools need.
// *magus.Magus satisfies it.
type graphResolver interface {
	KnowledgeGraph(ctx context.Context, refresh bool) (*knowledge.Graph, error)
	KnowledgeGraphWithSymbols(ctx context.Context) (*knowledge.Graph, error)
	KnowledgeGraphWithSymbolsForRef(ctx context.Context, symbol string) (*knowledge.Graph, error)
	// SymbolGaps names the projects whose declared symbol index could not be read, so an
	// answer can say whether it is a fact or a blind spot. The second return is false when
	// the probe itself failed, which must not be reported as verified coverage.
	SymbolGaps(ctx context.Context) ([]types.KnowledgeSymbolGap, bool)
}

// answerFn classifies a result once its match count is known. The tools pass one rather
// than the two values it is built from, because those two only ever travel together and
// only ever feed this.
type answerFn func(matched bool) types.KnowledgeAnswer

// gapProbe defers the coverage lookup to the branch that needs it. It stats one file per
// declared index, and only an answer that reports nothing has anything to explain, so the
// tools pass a thunk rather than paying for it on every call. It also keeps
// pagedRefs/pagedQuery testable against a hand-built graph with no workspace behind them.
type gapProbe func() ([]types.KnowledgeSymbolGap, bool)

// coverageFor reports what a lookup could consult, for knowledge.Answer to judge. The gap
// probe is skipped when the lazy layer could not have held the answer, which is the same
// gate the CLI applies.
//
// These tools used to reach their own verdict, and it disagreed with the CLI's on the same
// graph: MCP set symbols-not-loaded on any unseeded query, CLI first asked whether the
// layer was relevant. Neither derives one now - both observe and call knowledge.Answer.
func coverageFor(input string, seeded bool, probe gapProbe) knowledge.Coverage {
	cov := knowledge.Coverage{Seeded: seeded}
	if knowledge.CouldMatchSymbol(input) {
		cov.Gaps, cov.Probed = probe()
	}
	return cov
}

// knowledgeGraph resolves the DOMAIN knowledge graph for a tool invocation - the warm
// graph, which excludes the lazily-loaded @symbols shards. explain/path/stats use it.
// A symbol-seeded magus_query and magus_refs instead use KnowledgeGraphWithSymbols,
// which merges symbols into a fresh graph so the shared warm graph is never polluted.
func knowledgeGraph(ctx context.Context, g graphResolver) (*knowledge.Graph, error) {
	return g.KnowledgeGraph(ctx, false)
}

type queryTool struct{ graph graphResolver }

func (t *queryTool) Name() string { return hint.ToolQuery.String() }

// paginatedQuery is the query result with the opaque cursor for the next page. It
// embeds KnowledgeQueryOutput so the wire shape is the plain result plus one
// additive next_cursor field (present only when more matches remain).
type paginatedQuery struct {
	types.KnowledgeQueryOutput
	NextCursor string `json:"next_cursor,omitempty"`
}

func (t *queryTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	terms := paramString(req.Params, "query", "")
	if terms == "" {
		return spells.InvokeResponse{}, errors.New("mcp: query is required")
	}
	budget := int(paramFloat(req.Params, "budget", 0))
	limit := int(paramFloat(req.Params, "limit", 0))
	cursor := paramString(req.Params, "cursor", "")
	// A symbol-seeded query needs the lazily-loaded symbol shards; everything else
	// answers from the symbol-free warm graph.
	var g *knowledge.Graph
	var err error
	seedsSymbols := knowledge.SeedsSymbols(terms)
	if seedsSymbols {
		g, err = t.graph.KnowledgeGraphWithSymbols(ctx)
	} else {
		g, err = knowledgeGraph(ctx, t.graph)
	}
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	probe := func() ([]types.KnowledgeSymbolGap, bool) { return t.graph.SymbolGaps(ctx) }
	resp, err := pagedQuery(g, terms, budget, limit, cursor, func(matched bool) types.KnowledgeAnswer {
		return knowledge.Answer(terms, matched, coverageFor(terms, seedsSymbols, probe))
	})
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: resp}, nil
}

type refsTool struct{ graph graphResolver }

func (t *refsTool) Name() string { return hint.ToolRefs.String() }

// paginatedRefs is the refs result with the opaque cursor for the next page of
// referencing files. It embeds KnowledgeRefsOutput so the wire shape is the plain
// result plus one additive next_cursor field.
type paginatedRefs struct {
	types.KnowledgeRefsOutput
	NextCursor string `json:"next_cursor,omitempty"`
}

func (t *refsTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	symbol := paramString(req.Params, "symbol", "")
	if symbol == "" {
		return spells.InvokeResponse{}, errors.New("mcp: symbol is required")
	}
	g, err := t.graph.KnowledgeGraphWithSymbolsForRef(ctx, symbol)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	resp, err := pagedRefs(g, symbol, int(paramFloat(req.Params, "limit", 0)), paramString(req.Params, "cursor", ""), func() ([]types.KnowledgeSymbolGap, bool) { return t.graph.SymbolGaps(ctx) })
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: resp}, nil
}

// pagedRefs resolves the symbol and pages its referencing files. A refs list is the
// first result set that genuinely overflows an agent's budget, so the same stateless
// cursor as pagedQuery windows it - offset plus a query hash and graph fingerprint so
// a stale cursor fails loudly. Defs and the totals reflect the whole set; only Refs
// is the page. Split from Invoke so it is testable with a hand-built graph.
func pagedRefs(g *knowledge.Graph, symbol string, limit int, cursor string, probe gapProbe) (paginatedRefs, error) {
	out, ok := g.Refs(symbol)
	if !ok {
		// refs always resolves against a symbol-merged graph, so the question is never
		// whether symbols were loaded, only whether every declared index was readable.
		// Both verdicts come back as a RESULT, never an error. The tool succeeded at
		// working out what it could say, and that conclusion has to be machine-branchable:
		// an agent told to read answer.verdict cannot read it off an error string, and
		// returning absent out-of-band would leave one of the three verdicts reachable
		// only by pattern-matching prose.
		return paginatedRefs{KnowledgeRefsOutput: types.KnowledgeRefsOutput{
			Definition:    types.KnowledgeRefsDefinition,
			SchemaVersion: types.KnowledgeSchemaVersion,
			Symbol:        symbol,
			Answer:        knowledge.Answer(symbol, false, coverageFor(symbol, true, probe)),
		}}, nil
	}
	out.Answer = knowledge.Answer(symbol, len(out.Refs) > 0, coverageFor(symbol, true, probe))
	if limit <= 0 && cursor == "" {
		return paginatedRefs{KnowledgeRefsOutput: out}, nil
	}
	qh := queryHash(symbol)
	fp := g.Fingerprint()
	offset := 0
	if cursor != "" {
		cur, err := decodeCursor(cursor)
		if err != nil {
			return paginatedRefs{}, errors.New("mcp: invalid cursor")
		}
		if cur.QueryHash != qh {
			return paginatedRefs{}, errors.New("mcp: cursor does not match this symbol; restart pagination without a cursor")
		}
		if cur.GraphFP != fp {
			return paginatedRefs{}, errors.New("mcp: graph changed since this cursor was issued; restart pagination without a cursor")
		}
		offset = cur.Offset
	}

	total := len(out.Refs)
	var page []types.KnowledgeRefSite
	if offset < total {
		page = out.Refs[offset:]
	}
	if limit > 0 && len(page) > limit {
		page = page[:limit]
	}
	out.Refs = page
	resp := paginatedRefs{KnowledgeRefsOutput: out}
	if end := offset + len(page); end < total {
		resp.NextCursor = encodeCursor(queryCursor{Offset: end, QueryHash: qh, GraphFP: fp})
	}
	return resp, nil
}

// pagedQuery runs a (possibly paged) query against g. With no limit and no cursor
// it is the plain query (every match, no fingerprint cost). Otherwise it validates
// the cursor against the query and the current graph fingerprint - failing loudly
// on a mismatch - returns the requested page, and attaches a next_cursor when more
// matches remain. Split from Invoke so it is testable with a hand-built graph. The
// unpaged result is wrapped too (with an empty, omitted NextCursor) so the return
// type is always concrete.
func pagedQuery(g *knowledge.Graph, terms string, budget, limit int, cursor string, verdict answerFn) (paginatedQuery, error) {
	// The verdict is computed from the UNPAGED match count and copied onto every page, so
	// an agent reading page three sees the same coverage statement as page one.
	answer := func(out types.KnowledgeQueryOutput) types.KnowledgeQueryOutput {
		out.Answer = verdict(out.MatchCount > 0)
		return out
	}
	if limit <= 0 && cursor == "" {
		return paginatedQuery{KnowledgeQueryOutput: answer(g.Query(terms, budget))}, nil
	}
	qh := queryHash(terms)
	fp := g.Fingerprint()
	offset := 0
	if cursor != "" {
		cur, err := decodeCursor(cursor)
		if err != nil {
			return paginatedQuery{}, errors.New("mcp: invalid cursor")
		}
		if cur.QueryHash != qh {
			return paginatedQuery{}, errors.New("mcp: cursor does not match this query; restart pagination without a cursor")
		}
		if cur.GraphFP != fp {
			return paginatedQuery{}, errors.New("mcp: graph changed since this cursor was issued; restart pagination without a cursor")
		}
		offset = cur.Offset
	}

	out := answer(g.QueryPage(terms, budget, offset, limit))
	resp := paginatedQuery{KnowledgeQueryOutput: out}
	if end := out.Offset + len(out.Matches); end < out.MatchCount {
		resp.NextCursor = encodeCursor(queryCursor{Offset: end, QueryHash: qh, GraphFP: fp})
	}
	return resp, nil
}

type explainTool struct{ graph graphResolver }

func (t *explainTool) Name() string { return hint.ToolExplain.String() }

func (t *explainTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	node := paramString(req.Params, "node", "")
	if node == "" {
		return spells.InvokeResponse{}, errors.New("mcp: node is required")
	}
	g, err := knowledgeGraph(ctx, t.graph)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	out, ok := g.Explain(node)
	if !ok {
		// explain runs against the symbol-free warm graph, so a code symbol was never in
		// scope here. Saying so as a RESULT keeps the agent from recording "no such node"
		// as a fact about the workspace; the channel is text because that is this tool's
		// channel for everything.
		// explain runs against the symbol-free warm graph, so a code symbol was never in
		// scope - but only say so when the query could have named one. `kind=author` with
		// a typo has nothing to do with the symbol layer, and an absent verdict there is a
		// fact worth asserting, which is why this is not hardcoded.
		ans := knowledge.Answer(node, false, coverageFor(node, false,
			func() ([]types.KnowledgeSymbolGap, bool) { return t.graph.SymbolGaps(ctx) }))
		if ans.Verdict == types.VerdictUnknown {
			return spells.InvokeResponse{Text: render.MissText(node, ans)}, nil
		}
		return spells.InvokeResponse{}, errors.New("mcp: no node matches " + node)
	}
	// Return the compact, natural-language rendering (not the verbose JSON struct):
	// for a result an agent reads and reasons about, aligned text with full IDs is
	// more token-efficient and less error-prone than repeated-key JSON.
	return spells.InvokeResponse{Text: render.ExplainText(out)}, nil
}

type pathTool struct{ graph graphResolver }

func (t *pathTool) Name() string { return hint.ToolPath.String() }

func (t *pathTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	from := paramString(req.Params, "from", "")
	to := paramString(req.Params, "to", "")
	if from == "" || to == "" {
		return spells.InvokeResponse{}, errors.New("mcp: from and to are required")
	}
	g, err := knowledgeGraph(ctx, t.graph)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	out, ok := g.Path(from, to)
	if !ok {
		return spells.InvokeResponse{}, errors.New("mcp: could not resolve " + from + " or " + to + " to a node")
	}
	return spells.InvokeResponse{Text: render.PathText(out)}, nil
}

type statsTool struct{ graph graphResolver }

func (t *statsTool) Name() string { return hint.ToolStats.String() }

func (t *statsTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	g, err := knowledgeGraph(ctx, t.graph)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: g.Stats(paramString(req.Params, "kind", ""))}, nil
}

var (
	_ spells.Driver = (*queryTool)(nil)
	_ spells.Driver = (*refsTool)(nil)
	_ spells.Driver = (*explainTool)(nil)
	_ spells.Driver = (*pathTool)(nil)
	_ spells.Driver = (*statsTool)(nil)
)
