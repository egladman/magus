//go:build mcp

package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
)

// The knowledge-graph retrieval tools (query/explain/path/stats) mirror the CLI
// verbs and sit on the same cache-first substrate. Each resolves the graph then
// answers. In the daemon, where a watcher keeps a warm graph invalidated on source
// changes (see Magus.WatchKnowledgeGraph, started in startMCPWithDaemon), this
// answers from memory without re-parsing magusfiles; otherwise it rebuilds
// cache-first. Either way it is fresh.

// knowledgeGraph resolves the DOMAIN knowledge graph for a tool invocation - the warm
// graph, which excludes the lazily-loaded @symbols shards. explain/path/stats use it.
// A symbol-seeded magus_query and magus_refs instead use KnowledgeGraphWithSymbols,
// which merges symbols into a fresh graph so the shared warm graph is never polluted.
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

// outputRefResult is the wire shape for a ref-routed magus_query: the captured
// output plus the run's identity, so an agent that saw a ref in a run fetches the
// exact bytes without re-reading a wall of text.
type outputRefResult struct {
	Ref        string `json:"ref"`
	Project    string `json:"project,omitempty"`
	Target     string `json:"target,omitempty"`
	Failed     bool   `json:"failed"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Output     string `json:"output"`
}

func (t *queryTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	terms := paramString(req.Params, "query", "")
	if terms == "" {
		return types.InvokeResponse{}, errors.New("mcp: query is required")
	}
	// Output-ref routing mirrors the CLI: a query shaped like a target-output ref
	// (strict ^ref[0-9a-f]+$) returns that execution's captured output instead of
	// searching the graph. A free-text query like "refactor" falls through.
	if cache.LooksLikeRef(terms) {
		return t.invokeOutputRef(terms)
	}
	budget := int(paramFloat(req.Params, "budget", 0))
	limit := int(paramFloat(req.Params, "limit", 0))
	cursor := paramString(req.Params, "cursor", "")
	// A symbol-seeded query needs the lazily-loaded symbol shards; everything else
	// answers from the symbol-free warm graph.
	var g *knowledge.Graph
	var err error
	if knowledge.SeedsSymbols(terms) {
		g, err = t.opts.Magus.KnowledgeGraphWithSymbols(ctx)
	} else {
		g, err = knowledgeGraph(ctx, t.opts)
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

// invokeOutputRef resolves a target-output ref (or unique prefix) to its stored
// bytes and metadata - the MCP analog of `magus query ref...`.
func (t *queryTool) invokeOutputRef(ref string) (types.InvokeResponse, error) {
	data, meta, err := t.opts.Magus.OutputByRef(ref)
	if err != nil {
		var amb *cache.AmbiguousRefError
		switch {
		case errors.As(err, &amb):
			return types.InvokeResponse{}, fmt.Errorf("mcp: %w", amb)
		case errors.Is(err, fs.ErrNotExist):
			return types.InvokeResponse{}, fmt.Errorf("mcp: no stored output for ref %q: %w", ref, err)
		default:
			return types.InvokeResponse{}, err
		}
	}
	return types.InvokeResponse{Data: outputRefResult{
		Ref:        meta.Ref,
		Project:    meta.Project,
		Target:     meta.Target,
		Failed:     meta.Failed,
		DurationMs: meta.DurationMs,
		Output:     string(data),
	}}, nil
}

type refsTool struct{ opts ServerOptions }

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
	g, err := t.opts.Magus.KnowledgeGraphWithSymbolsForRef(ctx, symbol)
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
	_ types.SpellDriver = (*refsTool)(nil)
	_ types.SpellDriver = (*explainTool)(nil)
	_ types.SpellDriver = (*pathTool)(nil)
	_ types.SpellDriver = (*statsTool)(nil)
)
