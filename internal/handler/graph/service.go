package graph

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/graph/knowledge"
	graphv1 "github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1/graphv1alpha1connect"
	"github.com/egladman/magus/types"
)

// resolver is the slice of the workspace GraphService needs, the same one the MCP graph
// tools take: the graph, with or without symbols, plus the coverage probe. *magus.Magus
// satisfies it.
//
// SymbolGaps is not optional decoration. Every verb here can be asked about a code symbol,
// and the symbol shards are loaded lazily, so a lookup that returns nothing has two very
// different meanings: no such node, or nobody looked. The probe is what lets the response
// say which, and it is the whole reason Answer rides the query result.
type resolver interface {
	KnowledgeGraph(ctx context.Context, req types.GraphRequest) (*knowledge.Graph, error)
	SymbolGaps(ctx context.Context) ([]types.KnowledgeSymbolGap, bool)
	Affected(ctx context.Context, base string) (*types.AffectedResult, error)
	Graph() (*types.Graph, error)
	ClassifyFiles(ctx context.Context, paths []string) ([]types.FileEntry, error)
}

// Service implements graphv1alpha1connect.GraphServiceHandler over the workspace knowledge
// graph. Every RPC is read-only; the server mounts it behind the console read bearer.
type Service struct{ ws resolver }

// NewService builds a GraphService handler reading from ws.
func NewService(ws resolver) *Service { return &Service{ws: ws} }

var _ graphv1alpha1connect.GraphServiceHandler = (*Service)(nil)

// graph resolves the flavor a lookup needs. Only a symbol-seeded input pays for the
// @symbols shards; everything else answers from the warm, symbol-free graph, which is what
// keeps the default export lazy.
func (s *Service) graph(ctx context.Context, input string) (*knowledge.Graph, bool, error) {
	if knowledge.SeedsLazyLayer(input) {
		g, err := s.ws.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
		return g, true, err
	}
	g, err := s.ws.KnowledgeGraph(ctx, types.GraphRequest{})
	return g, false, err
}

// answer reports what was actually searched and lets knowledge.Answer judge it, so this
// service cannot reach a different verdict than the CLI or the MCP tools about one graph.
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

// indexStatus is the freshness probe a position query refuses on. The resolver seam does not
// carry it, so QueryNodes asks the workspace for it and refuses a position when it is absent.
type indexStatus interface {
	SymbolIndexStatus(ctx context.Context) []types.SymbolIndexStatus
}

// positionCoverage is what the symbol layer can vouch for under a position query: the gap
// probe, plus every index the freshness probe reports out of date, and which files an
// up-to-date index covers. ok is false when the workspace offers no freshness probe.
func (s *Service) positionCoverage(ctx context.Context) (cov knowledge.Coverage, fresh func(file string) bool, ok bool) {
	status, ok := s.ws.(indexStatus)
	if !ok {
		return knowledge.Coverage{}, nil, false
	}
	cov = knowledge.Coverage{Seeded: true}
	cov.Gaps, cov.Probed = s.ws.SymbolGaps(ctx)
	byProject := map[string]*projectIndexes{}
	for _, st := range status.SymbolIndexStatus(ctx) {
		p := byProject[st.Project.Path]
		if p == nil {
			p = &projectIndexes{changed: map[string]bool{}}
			byProject[st.Project.Path] = p
		}
		p.add(st)
		if st.Freshness == types.SymbolIndexStale {
			cov.Stale = append(cov.Stale, types.KnowledgeStaleIndex{
				Project: st.Project.Path, Language: st.Language, Op: st.Op, Changed: st.Changed, ChangedCount: st.ChangedCount,
			})
		}
	}
	return cov, freshFile(byProject), true
}

// projectIndexes is what one project's indexes vouch for: every file unless one is neither
// up to date nor stale with every change listed, and never a file listed as changed.
type projectIndexes struct {
	unvouched bool
	changed   map[string]bool
}

func (p *projectIndexes) add(st types.SymbolIndexStatus) {
	switch {
	case st.Freshness == types.SymbolIndexFresh:
	case st.Freshness == types.SymbolIndexStale && len(st.Changed) == st.ChangedCount:
		for _, f := range st.Changed {
			p.changed[f] = true
		}
	default:
		p.unvouched = true
	}
}

// freshFile reports whether the indexes of the deepest project owning a file vouch for it.
func freshFile(byProject map[string]*projectIndexes) func(string) bool {
	return func(file string) bool {
		owner, found := "", false
		for project := range byProject {
			if (project == "." || file == project || strings.HasPrefix(file, project+"/")) && (!found || len(project) > len(owner)) {
				owner, found = project, true
			}
		}
		if !found {
			return false
		}
		p := byProject[owner]
		return !p.unvouched && !p.changed[file]
	}
}

// queryPosition answers a QueryNodes request that carries a position: the ranked symbols
// its window names, over the symbol graph, or a FAILED_PRECONDITION naming why the index
// cannot vouch for the position's file.
func (s *Service) queryPosition(ctx context.Context, msg *graphv1.QueryNodesRequest) (*connect.Response[graphv1.QueryNodesResponse], error) {
	p := msg.GetPosition()
	if p.GetPath() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("graph: position.path is required"))
	}
	pos := knowledge.Position{Path: p.GetPath(), Offset: int(p.GetOffset()), Prefix: p.GetPrefix(), Suffix: p.GetSuffix()}
	cov, fresh, ok := s.positionCoverage(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("graph: this workspace reports no symbol index freshness, so it cannot vouch for a position"))
	}
	g, err := s.ws.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := g.RefusePosition(pos, cov); err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("graph: %w", err))
	}
	out, sites, err := g.QueryPosition(msg.GetQuery(), int(msg.GetBudget()), int(msg.GetOffset()), int(msg.GetPageSize()), knowledge.RetrieveOptions{
		Weights:  knowledge.ShippedWeights(),
		Exclude:  msg.GetExcludePaths(),
		Position: &pos,
	}, fresh)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("graph: %w", err))
	}
	return connect.NewResponse(&graphv1.QueryNodesResponse{
		Query:      out.Query,
		Budget:     int32(out.Budget),
		MatchCount: int32(out.MatchCount),
		Offset:     int32(out.Offset),
		Matches:    matchesToProto(out.Matches),
		Nodes:      nodesToProto(out.Nodes),
		Links:      edgesToProto(out.Links),
		Answer:     answerToProto(knowledge.Answer(out.Query, out.MatchCount > 0, cov)),
		Context:    sitesToProto(sites),
	}), nil
}

func (s *Service) QueryNodes(
	ctx context.Context, req *connect.Request[graphv1.QueryNodesRequest],
) (*connect.Response[graphv1.QueryNodesResponse], error) {
	if req.Msg.GetPosition() != nil {
		return s.queryPosition(ctx, req.Msg)
	}
	query := req.Msg.GetQuery()
	if query == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(`graph: query is required (e.g. "kind=target project=api")`))
	}
	g, seeded, err := s.graph(ctx, query)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out, err := g.QueryRanked(query, int(req.Msg.GetBudget()), int(req.Msg.GetOffset()), int(req.Msg.GetPageSize()), knowledge.RetrieveOptions{
		Weights: knowledge.ShippedWeights(), Table: embedtable.Cached, Exclude: req.Msg.GetExcludePaths(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
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
	g, _, err := s.graph(ctx, ref)
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
	g, _, err := s.graph(ctx, name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out, ok := g.Explain(name)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("graph: no node matches %s, try `magus query %s` for a wider search", name, name))
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
	g, _, err := s.graph(ctx, fmt.Sprintf("%s %s", from, to))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// ok=false is an unresolvable ENDPOINT, which is the caller's mistake and an error.
	// out.Found=false is two real nodes with no chain between them, which is an answer;
	// collapsing the two would report "no path" for a typo.
	out, ok := g.Path(from, to)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no node matches %s or %s", from, to))
	}
	resp := &graphv1.Path{
		From:  out.From,
		To:    out.To,
		Found: out.Found,
		Steps: make([]*graphv1.PathStep, 0, len(out.Steps)),
	}
	for _, st := range out.Steps {
		resp.Steps = append(resp.Steps, &graphv1.PathStep{
			From: st.From, To: st.To, Relation: string(st.Relation), Forward: st.Forward,
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
	g, err := s.ws.KnowledgeGraph(ctx, types.GraphRequest{})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Resolve first: the caller may pass a reference rather than an id, and Dependents on an
	// unknown id returns nil, which would report "nothing rebuilds" for a typo.
	matches := g.Resolve(name, 1)
	if len(matches) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("graph: no node matches %s, try `magus query %s` for a wider search", name, name))
	}
	id := matches[0].ID
	return connect.NewResponse(&graphv1.Dependents{Node: id, Ids: g.Dependents(id)}), nil
}

func (s *Service) FindAffected(
	ctx context.Context, req *connect.Request[graphv1.FindAffectedRequest],
) (*connect.Response[graphv1.Affected], error) {
	// An empty base is not a missing argument: Affected resolves it from magus.yaml, the env and
	// the run history, so a browser caller never has to know the repo's base ref.
	base := req.Msg.GetBase()
	r, err := s.ws.Affected(ctx, base)
	if err != nil {
		return affectedError(err)
	}
	p, ok := s.ws.(planner)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("graph: this workspace offers no shard planner, so it cannot say what to run"))
	}
	target := cmp.Or(req.Msg.GetTarget(), types.TargetCI)
	plan, err := p.Plan(ctx, target, magus.PlanOptions{BaseRef: base, MaxShards: -1})
	if err != nil {
		return affectedError(err)
	}
	g, err := s.ws.Graph()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	files, err := s.ws.ClassifyFiles(ctx, r.Changed)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	ids := make([]string, 0, len(r.Affected))
	reasons := make([]*graphv1.AffectedReason, 0, len(r.Affected))
	for _, path := range r.Affected {
		ids = append(ids, projectID(path))
		reasons = append(reasons, &graphv1.AffectedReason{
			Id:     projectID(path),
			Seed:   slices.Contains(r.Seed, path),
			Chains: chainsToProto(g.PathsFromSeeds(r.Seed, path)),
		})
	}
	return connect.NewResponse(&graphv1.Affected{
		Base:         r.Base,
		ChangedFiles: int32(len(r.Changed)),
		Ids:          ids,
		Seeds:        seedsToProto(r, files),
		Reasons:      reasons,
		Plan:         planToProto(target, base, plan),
	}), nil
}

// planner is the shard planner behind `magus affected --plan`. Its options are the root
// package's, so the resolver seam does not carry it.
type planner interface {
	Plan(ctx context.Context, target string, opts magus.PlanOptions) (types.ShardPlan, error)
}

// affectedError keeps a diff the VCS could not compute an answer. "I could not tell" differs
// from "nothing is affected", and CodeInternal would collapse the two.
func affectedError(err error) (*connect.Response[graphv1.Affected], error) {
	if errors.Is(err, types.ErrAffectedFallback) {
		return connect.NewResponse(&graphv1.Affected{Fallback: err.Error()}), nil
	}
	return nil, connect.NewError(connect.CodeInternal, err)
}

func projectID(path string) string { return fmt.Sprintf("%s:%s", types.KindProject, path) }

func (s *Service) GetGraphStats(
	ctx context.Context, req *connect.Request[graphv1.GetGraphStatsRequest],
) (*connect.Response[graphv1.GraphStats], error) {
	// Stats counts the whole graph, so it must not be computed over a symbol-loaded flavor on
	// some calls and the warm one on others: the node count would move with the caller's
	// unrelated query history. Always the warm graph.
	g, err := s.ws.KnowledgeGraph(ctx, types.GraphRequest{})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(statsToProto(g.Stats(req.Msg.GetKind()))), nil
}
