//go:build mcp

package mcp

import (
	"context"
	"errors"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
)

// The knowledge-graph retrieval tools (query/explain/path) mirror the CLI verbs
// and sit on the same cache-first substrate. Each builds the graph (cache-first,
// so steady state is a fingerprint check) then answers. A daemon-resident warm
// graph is a later phase; for now every call goes through the shared builder.

// knowledgeGraph builds the workspace knowledge graph for a tool invocation.
func knowledgeGraph(ctx context.Context, opts ServerOptions) (*knowledge.Graph, error) {
	return magus.BuildKnowledgeGraph(ctx, opts.Magus, opts.Magus.Root(), opts.Config, false, opts.Logger)
}

type queryTool struct{ opts ServerOptions }

func (t *queryTool) Name() string { return "magus_query" }

func (t *queryTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	terms := paramString(req.Params, "query", "")
	if terms == "" {
		return types.InvokeResponse{}, errors.New("mcp: query is required")
	}
	g, err := knowledgeGraph(ctx, t.opts)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	return types.InvokeResponse{Data: g.Query(terms, int(paramFloat(req.Params, "budget", 0)))}, nil
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

var (
	_ types.SpellDriver = (*queryTool)(nil)
	_ types.SpellDriver = (*explainTool)(nil)
	_ types.SpellDriver = (*pathTool)(nil)
)
