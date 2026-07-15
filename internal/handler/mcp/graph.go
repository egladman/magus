package mcp

import (
	"context"
	"errors"

	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/internal/render"
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
}

// knowledgeGraph resolves the DOMAIN knowledge graph for a tool invocation - the warm
// graph, which excludes the lazily-loaded @symbols shards. explain/path/stats use it.
// A symbol-seeded magus_query and magus_refs instead use KnowledgeGraphWithSymbols,
// which merges symbols into a fresh graph so the shared warm graph is never polluted.
func knowledgeGraph(ctx context.Context, g graphResolver) (*knowledge.Graph, error) {
	return g.KnowledgeGraph(ctx, false)
}

type queryTool struct{ graph graphResolver }

func (t *queryTool) Name() string { return "magus_query" }

// paginatedQuery is the query result with the opaque cursor for the next page. It
// embeds KnowledgeQueryOutput so the wire shape is the plain result plus one
// additive next_cursor field (present only when more matches remain).
type paginatedQuery struct {
	types.KnowledgeQueryOutput
	NextCursor string `json:"next_cursor,omitempty"`
}

func (t *queryTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	terms := paramString(req.Params, "query", "")
	if terms == "" {
		return types.InvokeResponse{}, errors.New("mcp: query is required")
	}
	budget := int(paramFloat(req.Params, "budget", 0))
	limit := int(paramFloat(req.Params, "limit", 0))
	cursor := paramString(req.Params, "cursor", "")
	// A symbol-seeded query needs the lazily-loaded symbol shards; everything else
	// answers from the symbol-free warm graph.
	var g *knowledge.Graph
	var err error
	if knowledge.SeedsSymbols(terms) {
		g, err = t.graph.KnowledgeGraphWithSymbols(ctx)
	} else {
		g, err = knowledgeGraph(ctx, t.graph)
	}
	if err != nil {
		return types.InvokeResponse{}, err
	}
	resp, err := pagedQuery(g, terms, budget, limit, cursor)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	return types.InvokeResponse{Data: resp}, nil
}

type refsTool struct{ graph graphResolver }

func (t *refsTool) Name() string { return "magus_refs" }

// paginatedRefs is the refs result with the opaque cursor for the next page of
// referencing files. It embeds KnowledgeRefsOutput so the wire shape is the plain
// result plus one additive next_cursor field.
type paginatedRefs struct {
	types.KnowledgeRefsOutput
	NextCursor string `json:"next_cursor,omitempty"`
}

func (t *refsTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	symbol := paramString(req.Params, "symbol", "")
	if symbol == "" {
		return types.InvokeResponse{}, errors.New("mcp: symbol is required")
	}
	g, err := t.graph.KnowledgeGraphWithSymbolsForRef(ctx, symbol)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	resp, err := pagedRefs(g, symbol, int(paramFloat(req.Params, "limit", 0)), paramString(req.Params, "cursor", ""))
	if err != nil {
		return types.InvokeResponse{}, err
	}
	return types.InvokeResponse{Data: resp}, nil
}

// pagedRefs resolves the symbol and pages its referencing files. A refs list is the
// first result set that genuinely overflows an agent's budget, so the same stateless
// cursor as pagedQuery windows it - offset plus a query hash and graph fingerprint so
// a stale cursor fails loudly. Defs and the totals reflect the whole set; only Refs
// is the page. Split from Invoke so it is testable with a hand-built graph.
func pagedRefs(g *knowledge.Graph, symbol string, limit int, cursor string) (paginatedRefs, error) {
	out, ok := g.Refs(symbol)
	if !ok {
		// No symbol index at all is a distinct, more common failure than a wrong
		// name: nothing could ever match, so tell the agent to build the index (via
		// the CLI, since no MCP tool builds it) rather than to fix the symbol name.
		if !g.HasSymbols() {
			return paginatedRefs{}, errors.New("mcp: no symbol index has been built, so there are no symbols to match " + symbol + "; build one with `" + clihint.GraphBuild.String() + "` (the daemon auto-indexer also keeps it current while the server runs)")
		}
		return paginatedRefs{}, errors.New("mcp: no symbol matches " + symbol)
	}
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
func pagedQuery(g *knowledge.Graph, terms string, budget, limit int, cursor string) (paginatedQuery, error) {
	if limit <= 0 && cursor == "" {
		return paginatedQuery{KnowledgeQueryOutput: g.Query(terms, budget)}, nil
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

	out := g.QueryPage(terms, budget, offset, limit)
	resp := paginatedQuery{KnowledgeQueryOutput: out}
	if end := out.Offset + len(out.Matches); end < out.MatchCount {
		resp.NextCursor = encodeCursor(queryCursor{Offset: end, QueryHash: qh, GraphFP: fp})
	}
	return resp, nil
}

type explainTool struct{ graph graphResolver }

func (t *explainTool) Name() string { return "magus_explain" }

func (t *explainTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	node := paramString(req.Params, "node", "")
	if node == "" {
		return types.InvokeResponse{}, errors.New("mcp: node is required")
	}
	g, err := knowledgeGraph(ctx, t.graph)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	out, ok := g.Explain(node)
	if !ok {
		return types.InvokeResponse{}, errors.New("mcp: no node matches " + node)
	}
	// Return the compact, natural-language rendering (not the verbose JSON struct):
	// for a result an agent reads and reasons about, aligned text with full IDs is
	// more token-efficient and less error-prone than repeated-key JSON.
	return types.InvokeResponse{Text: render.ExplainText(out)}, nil
}

type pathTool struct{ graph graphResolver }

func (t *pathTool) Name() string { return "magus_path" }

func (t *pathTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	from := paramString(req.Params, "from", "")
	to := paramString(req.Params, "to", "")
	if from == "" || to == "" {
		return types.InvokeResponse{}, errors.New("mcp: from and to are required")
	}
	g, err := knowledgeGraph(ctx, t.graph)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	out, ok := g.Path(from, to)
	if !ok {
		return types.InvokeResponse{}, errors.New("mcp: could not resolve " + from + " or " + to + " to a node")
	}
	return types.InvokeResponse{Text: render.PathText(out)}, nil
}

type statsTool struct{ graph graphResolver }

func (t *statsTool) Name() string { return "magus_stats" }

func (t *statsTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	g, err := knowledgeGraph(ctx, t.graph)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	return types.InvokeResponse{Data: g.Stats(paramString(req.Params, "kind", ""))}, nil
}

var (
	_ types.SpellDriver = (*queryTool)(nil)
	_ types.SpellDriver = (*refsTool)(nil)
	_ types.SpellDriver = (*explainTool)(nil)
	_ types.SpellDriver = (*pathTool)(nil)
	_ types.SpellDriver = (*statsTool)(nil)
)
