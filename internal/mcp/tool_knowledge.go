//go:build mcp

package mcp

import (
	"context"
	"errors"

	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
)

// The knowledge-graph retrieval tools (query/explain/path/stats) mirror the CLI
// verbs and sit on the same cache-first substrate. Each resolves the graph then
// answers. In the daemon, where a watcher keeps a warm graph invalidated on source
// changes (see Magus.WatchKnowledgeGraph, started in startMCPWithDaemon), this
// answers from memory without re-parsing magusfiles; otherwise it rebuilds
// cache-first. Either way it is fresh.

// knowledgeGraph resolves the workspace knowledge graph for a tool invocation.
func knowledgeGraph(ctx context.Context, opts ServerOptions) (*knowledge.Graph, error) {
	return opts.Magus.KnowledgeGraph(ctx, false)
}

type queryTool struct{ opts ServerOptions }

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
	g, err := knowledgeGraph(ctx, t.opts)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	resp, err := pagedQuery(g, terms, budget, limit, cursor)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	return types.InvokeResponse{Data: resp}, nil
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

type explainTool struct{ opts ServerOptions }

func (t *explainTool) Name() string { return "magus_explain" }

func (t *explainTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	node := paramString(req.Params, "node", "")
	if node == "" {
		return types.InvokeResponse{}, errors.New("mcp: node is required")
	}
	g, err := knowledgeGraph(ctx, t.opts)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	out, ok := g.Explain(node)
	if !ok {
		return types.InvokeResponse{}, errors.New("mcp: no node matches " + node)
	}
	return types.InvokeResponse{Data: out}, nil
}

type pathTool struct{ opts ServerOptions }

func (t *pathTool) Name() string { return "magus_path" }

func (t *pathTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	from := paramString(req.Params, "from", "")
	to := paramString(req.Params, "to", "")
	if from == "" || to == "" {
		return types.InvokeResponse{}, errors.New("mcp: from and to are required")
	}
	g, err := knowledgeGraph(ctx, t.opts)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	out, ok := g.Path(from, to)
	if !ok {
		return types.InvokeResponse{}, errors.New("mcp: could not resolve " + from + " or " + to + " to a node")
	}
	return types.InvokeResponse{Data: out}, nil
}

type statsTool struct{ opts ServerOptions }

func (t *statsTool) Name() string { return "magus_stats" }

func (t *statsTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	g, err := knowledgeGraph(ctx, t.opts)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	return types.InvokeResponse{Data: g.Stats(paramString(req.Params, "kind", ""))}, nil
}

var (
	_ types.SpellDriver = (*queryTool)(nil)
	_ types.SpellDriver = (*explainTool)(nil)
	_ types.SpellDriver = (*pathTool)(nil)
	_ types.SpellDriver = (*statsTool)(nil)
)
