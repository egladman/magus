package graph

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/graph/knowledge"
	graphv1 "github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1/graphv1alpha1connect"
	"github.com/egladman/magus/types"
)

// resolver is the slice of the workspace GraphService needs, the same one the MCP graph
// tools take: the two graph flavors plus the coverage probe. *magus.Magus satisfies it.
//
// SymbolGaps is not optional decoration. Every verb here can be asked about a code symbol,
// and the symbol shards are loaded lazily, so a lookup that returns nothing has two very
// different meanings - no such node, or nobody looked. The probe is what lets the response
// say which, and it is the whole reason Answer rides the query result.
type resolver interface {
	KnowledgeGraph(ctx context.Context, refresh bool) (*knowledge.Graph, error)
	KnowledgeGraphWithSymbols(ctx context.Context) (*knowledge.Graph, error)
	SymbolGaps(ctx context.Context) ([]types.KnowledgeSymbolGap, bool)
	Affected(ctx context.Context, base string) (*types.AffectedResult, error)
}

// Service implements graphv1alpha1connect.GraphServiceHandler over the workspace knowledge
// graph. Every RPC is read-only; the daemon mounts it behind the console read bearer.
type Service struct{ ws resolver }

// NewService builds a GraphService handler reading from ws.
func NewService(ws resolver) *Service { return &Service{ws: ws} }

var _ graphv1alpha1connect.GraphServiceHandler = (*Service)(nil)

// graphFor resolves the flavor a lookup needs. Only a symbol-seeded input pays for the
// @symbols shards; everything else answers from the warm, symbol-free graph, which is what
// keeps the default export lazy.
func (s *Service) graphFor(ctx context.Context, input string) (*knowledge.Graph, bool, error) {
	if knowledge.SeedsLazyLayer(input) {
		g, err := s.ws.KnowledgeGraphWithSymbols(ctx)
		return g, true, err
	}
	g, err := s.ws.KnowledgeGraph(ctx, false)
	return g, false, err
}

// answer reports what was actually searched and lets knowledge.Answer judge it, so this
// surface cannot reach a different verdict than the CLI or the MCP tools about one graph.
//
// The probe is skipped when the symbol layer could not have held the answer: `kind:author`
// returning nothing has no bearing on a missing symbol index, and caveating it would point
// the reader at a layer that was never in scope.
func (s *Service) answer(ctx context.Context, input string, matched, seededSymbols bool) types.KnowledgeAnswer {
	cov := knowledge.Coverage{Seeded: seededSymbols}
	if knowledge.CouldMatchLazyLayer(input) {
		cov.Gaps, cov.Probed = s.ws.SymbolGaps(ctx)
	}
	return knowledge.Answer(input, matched, cov)
}

func (s *Service) QueryNodes(
	ctx context.Context, req *connect.Request[graphv1.QueryNodesRequest],
) (*connect.Response[graphv1.QueryNodesResponse], error) {
	query := req.Msg.GetQuery()
	if query == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(`graph: query is required (e.g. "kind=target project=api")`))
	}
	g, seeded, err := s.graphFor(ctx, query)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := g.QueryPage(query, int(req.Msg.GetBudget()), int(req.Msg.GetOffset()), int(req.Msg.GetPageSize()))
	resp := &graphv1.QueryNodesResponse{
		Query:      out.Query,
		Budget:     int32(out.Budget),
		MatchCount: int32(out.MatchCount),
		Offset:     int32(out.Offset),
		Matches:    matchesToProto(out.Matches),
		Nodes:      nodesToProto(out.Nodes),
		Links:      edgesToProto(out.Links),
		// The verdict is computed from the UNPAGED match count, so page three says the same
		// thing about coverage as page one.
		Answer: answerToProto(s.answer(ctx, query, out.MatchCount > 0, seeded)),
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) ResolveNodes(
	ctx context.Context, req *connect.Request[graphv1.ResolveNodesRequest],
) (*connect.Response[graphv1.ResolveNodesResponse], error) {
	ref := req.Msg.GetReference()
	if ref == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("graph: reference is required"))
	}
	g, _, err := s.graphFor(ctx, ref)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	matches := g.Resolve(ref, int(req.Msg.GetLimit()))
	return connect.NewResponse(&graphv1.ResolveNodesResponse{Matches: matchesToProto(matches)}), nil
}

func (s *Service) ExplainNode(
	ctx context.Context, req *connect.Request[graphv1.ExplainNodeRequest],
) (*connect.Response[graphv1.NodeContext], error) {
	name := req.Msg.GetName()
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("graph: name is required"))
	}
	g, _, err := s.graphFor(ctx, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out, ok := g.Explain(name)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("graph: no node matches "+name+"; try `magus query "+name+"` for a wider search"))
	}
	resp := &graphv1.NodeContext{
		Node:        nodeToProto(out.Node),
		BlastRadius: int32(out.BlastRadius),
		Out:         edgeRefsToProto(out.Out),
		In:          edgeRefsToProto(out.In),
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) FindPath(
	ctx context.Context, req *connect.Request[graphv1.FindPathRequest],
) (*connect.Response[graphv1.Path], error) {
	from, to := req.Msg.GetFrom(), req.Msg.GetTo()
	if from == "" || to == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("graph: from and to are both required"))
	}
	g, _, err := s.graphFor(ctx, from+" "+to)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// ok=false is an unresolvable ENDPOINT, which is the caller's mistake and an error.
	// out.Found=false is two real nodes with no chain between them, which is an answer -
	// collapsing the two would report "no path" for a typo.
	out, ok := g.Path(from, to)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no node matches "+from+" or "+to))
	}
	resp := &graphv1.Path{
		From:  out.From,
		To:    out.To,
		Found: out.Found,
		Steps: make([]*graphv1.PathStep, 0, len(out.Steps)),
	}
	for _, st := range out.Steps {
		resp.Steps = append(resp.Steps, &graphv1.PathStep{
			From: st.From, To: st.To, Relation: st.Relation, Forward: st.Forward,
		})
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) FindDependents(
	ctx context.Context, req *connect.Request[graphv1.FindDependentsRequest],
) (*connect.Response[graphv1.Dependents], error) {
	name := req.Msg.GetName()
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("graph: name is required"))
	}
	// The warm graph, always. depends_on edges never touch the @symbols shards, so loading them
	// would cost the shard read and change nothing about the answer.
	g, err := s.ws.KnowledgeGraph(ctx, false)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Resolve first: the caller may pass a reference rather than an id, and Dependents on an
	// unknown id returns nil - which would report "nothing rebuilds" for a typo.
	matches := g.Resolve(name, 1)
	if len(matches) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("graph: no node matches "+name+"; try `magus query "+name+"` for a wider search"))
	}
	id := matches[0].ID
	return connect.NewResponse(&graphv1.Dependents{Node: id, Ids: g.Dependents(id)}), nil
}

func (s *Service) FindAffected(
	ctx context.Context, req *connect.Request[graphv1.FindAffectedRequest],
) (*connect.Response[graphv1.Affected], error) {
	// An empty base is not a missing argument: Affected resolves it from magus.yaml, the env and
	// the run history, so a browser caller never has to know the repo's base ref.
	r, err := s.ws.Affected(ctx, req.Msg.GetBase())
	if err != nil {
		// A VCS that cannot produce a definitive diff is an ANSWER, not a failure: the caller must
		// tell "nothing is affected" from "I could not tell", and CodeInternal collapses both.
		if errors.Is(err, types.ErrAffectedFallback) {
			return connect.NewResponse(&graphv1.Affected{Fallback: err.Error()}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	ids := make([]string, 0, len(r.Affected))
	for _, p := range r.Affected {
		ids = append(ids, types.KindProject+":"+p)
	}
	return connect.NewResponse(&graphv1.Affected{
		Base:         r.Base,
		ChangedFiles: int32(len(r.Changed)),
		Ids:          ids,
	}), nil
}

func (s *Service) GetGraphStats(
	ctx context.Context, req *connect.Request[graphv1.GetGraphStatsRequest],
) (*connect.Response[graphv1.GraphStats], error) {
	// Stats counts the whole graph, so it must not be computed over a symbol-loaded flavor on
	// some calls and the warm one on others: the node count would move with the caller's
	// unrelated query history. Always the warm graph.
	g, err := s.ws.KnowledgeGraph(ctx, false)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(statsToProto(g.Stats(req.Msg.GetKind()))), nil
}
