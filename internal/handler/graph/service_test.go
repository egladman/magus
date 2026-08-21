package graph

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/graph/knowledge"
	graphv1 "github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The engine's verbs are tested in internal/graph/knowledge. What is unverified here is the
// MAPPING - which domain field lands on which wire field, and which "not found" is an error
// versus an answer - so these assert that and lean on a small hand-built graph.
func fixture() *knowledge.Graph {
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "project:pkg/a", Kind: types.KindProject, Label: "pkg/a"})
	g.AddNode(types.KnowledgeNode{ID: "target:pkg/a:build", Kind: types.KindTarget, Label: "build"})
	g.AddNode(types.KnowledgeNode{ID: "target:pkg/a:test", Kind: types.KindTarget, Label: "test"})
	g.AddEdge(types.KnowledgeEdge{
		Source: "project:pkg/a", Target: "target:pkg/a:build",
		Relation: types.RelationContains, Confidence: types.ConfidenceExtracted, Score: 1,
	})
	g.AddEdge(types.KnowledgeEdge{
		Source: "target:pkg/a:test", Target: "target:pkg/a:build",
		Relation: types.RelationDependsOn, Confidence: types.ConfidenceExtracted, Score: 1,
		Provenance: "magusfile.buzz:4",
	})
	return g
}

// fakeResolver hands back one canned graph for every resolution path, so the RPCs are
// unit-testable through the seam without a real workspace. probed=false models the coverage
// probe itself failing, which must not read as verified coverage.
type fakeResolver struct {
	g      *knowledge.Graph
	err    error
	gaps   []types.KnowledgeSymbolGap
	probed bool
	aff    *types.AffectedResult
	affErr error
}

func (f fakeResolver) KnowledgeGraph(context.Context, bool) (*knowledge.Graph, error) {
	return f.g, f.err
}

func (f fakeResolver) KnowledgeGraphWithSymbols(context.Context) (*knowledge.Graph, error) {
	return f.g, f.err
}

func (f fakeResolver) SymbolGaps(context.Context) ([]types.KnowledgeSymbolGap, bool) {
	return f.gaps, f.probed
}

func (f fakeResolver) Affected(context.Context, string) (*types.AffectedResult, error) {
	return f.aff, f.affErr
}

func newService() *Service { return NewService(fakeResolver{g: fixture(), probed: true}) }

func TestQueryNodesMapsMatchesAndNeighborhood(t *testing.T) {
	resp, err := newService().QueryNodes(context.Background(),
		connect.NewRequest(&graphv1.QueryNodesRequest{Query: "kind:target"}))
	require.NoError(t, err)
	msg := resp.Msg
	assert.Equal(t, "kind:target", msg.GetQuery())
	assert.Equal(t, int32(2), msg.GetMatchCount())
	assert.Equal(t, int32(knowledge.DefaultBudget), msg.GetBudget())
	ids := make([]string, 0, len(msg.GetMatches()))
	for _, m := range msg.GetMatches() {
		ids = append(ids, m.GetId())
	}
	assert.ElementsMatch(t, []string{"target:pkg/a:build", "target:pkg/a:test"}, ids)
	assert.NotEmpty(t, msg.GetNodes(), "the neighborhood rides the response")
	assert.Equal(t, string(types.VerdictFound), msg.GetAnswer().GetVerdict())
}

// match_count is the TOTAL, not the page size: a client pages on offset + len(matches)
// against it, so a paged response reporting the page size would loop forever.
func TestQueryNodesPagesWithoutShrinkingMatchCount(t *testing.T) {
	resp, err := newService().QueryNodes(context.Background(),
		connect.NewRequest(&graphv1.QueryNodesRequest{Query: "kind:target", Offset: 1, PageSize: 1}))
	require.NoError(t, err)
	assert.Equal(t, int32(2), resp.Msg.GetMatchCount())
	assert.Equal(t, int32(1), resp.Msg.GetOffset())
	assert.Len(t, resp.Msg.GetMatches(), 1)
}

// A domain query never seeds the symbol shards, so nothing it fails to find says anything
// about whether a code symbol by that name exists. That is what the reason carries.
func TestQueryNodesReportsSymbolsNotLoaded(t *testing.T) {
	resp, err := newService().QueryNodes(context.Background(),
		connect.NewRequest(&graphv1.QueryNodesRequest{Query: "nothingmatchesthis"}))
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Msg.GetMatchCount())
	assert.Equal(t, string(types.VerdictUnknown), resp.Msg.GetAnswer().GetVerdict())
	assert.Equal(t, string(types.ReasonSymbolsNotLoaded), resp.Msg.GetAnswer().GetReason())
}

// The mirror of the case above, and the reason the probe is gated rather than always run: a
// query naming a non-symbol kind has ruled the symbol layer out itself, so finding nothing is
// a verified absence. Reporting it as unknown would point the reader at a layer that could
// not have held the answer.
func TestQueryNodesKindFilteredMissIsAbsentNotUnknown(t *testing.T) {
	svc := NewService(fakeResolver{
		g:      fixture(),
		probed: true,
		gaps:   []types.KnowledgeSymbolGap{{Project: types.ProjectRef{Path: "libs/api"}, State: types.SymbolIndexNotBuilt}},
	})
	resp, err := svc.QueryNodes(context.Background(),
		connect.NewRequest(&graphv1.QueryNodesRequest{Query: "kind:charm nothingmatchesthis"}))
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.Msg.GetMatchCount())
	assert.Equal(t, string(types.VerdictAbsent), resp.Msg.GetAnswer().GetVerdict())
	assert.Empty(t, resp.Msg.GetAnswer().GetGaps())
}

// A probe that could not run must not come back as an empty gap list: that reads as verified
// coverage and asserts the very fact the probe failed to establish.
func TestQueryNodesUnprobedCoverageIsUnknownNotAbsent(t *testing.T) {
	svc := NewService(fakeResolver{g: fixture(), probed: false})
	resp, err := svc.QueryNodes(context.Background(),
		connect.NewRequest(&graphv1.QueryNodesRequest{Query: "nothingmatchesthis"}))
	require.NoError(t, err)
	assert.Equal(t, string(types.VerdictUnknown), resp.Msg.GetAnswer().GetVerdict())
	assert.Equal(t, string(types.ReasonCoverageUnknown), resp.Msg.GetAnswer().GetReason())
	assert.Empty(t, resp.Msg.GetAnswer().GetGaps())
}

func TestQueryNodesFlattensSymbolGapProject(t *testing.T) {
	svc := NewService(fakeResolver{
		g:      fixture(),
		probed: true,
		gaps: []types.KnowledgeSymbolGap{{
			Project: types.ProjectRef{Path: "libs/api", Name: "libs/api", Dir: "/home/someone/repo/libs/api"},
			State:   types.SymbolIndexNotBuilt,
		}},
	})
	// A bare term, deliberately: an explicit non-symbol kind rules the symbol layer out and
	// skips the probe entirely, so there would be no gap to flatten.
	resp, err := svc.QueryNodes(context.Background(),
		connect.NewRequest(&graphv1.QueryNodesRequest{Query: "build"}))
	require.NoError(t, err)
	gaps := resp.Msg.GetAnswer().GetGaps()
	require.Len(t, gaps, 1)
	assert.Equal(t, "libs/api", gaps[0].GetProjectPath())
	assert.Equal(t, "libs/api", gaps[0].GetProjectName())
	assert.Equal(t, string(types.SymbolIndexNotBuilt), gaps[0].GetState())
}

func TestExplainNodeMapsEdgesWithDirectionAndProvenance(t *testing.T) {
	resp, err := newService().ExplainNode(context.Background(),
		connect.NewRequest(&graphv1.ExplainNodeRequest{Name: "target:pkg/a:build"}))
	require.NoError(t, err)
	msg := resp.Msg
	assert.Equal(t, "target:pkg/a:build", msg.GetNode().GetId())
	require.NotEmpty(t, msg.GetIn())
	var dep *graphv1.EdgeRef
	for _, e := range msg.GetIn() {
		if e.GetRelation() == types.RelationDependsOn {
			dep = e
		}
	}
	require.NotNil(t, dep, "the depends_on edge arrives as an incoming ref")
	assert.Equal(t, graphv1.EdgeDirection_EDGE_DIRECTION_IN, dep.GetDirection())
	assert.Equal(t, "target:pkg/a:test", dep.GetOther())
	assert.Equal(t, "magusfile.buzz:4", dep.GetProvenance())
}

func TestExplainNodeUnresolvableIsNotFound(t *testing.T) {
	_, err := newService().ExplainNode(context.Background(),
		connect.NewRequest(&graphv1.ExplainNodeRequest{Name: "target:nope:nope"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestFindPathReturnsTheWalkedChain(t *testing.T) {
	resp, err := newService().FindPath(context.Background(),
		connect.NewRequest(&graphv1.FindPathRequest{From: "target:pkg/a:test", To: "target:pkg/a:build"}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.GetFound())
	require.Len(t, resp.Msg.GetSteps(), 1)
	step := resp.Msg.GetSteps()[0]
	assert.Equal(t, "target:pkg/a:test", step.GetFrom())
	assert.Equal(t, "target:pkg/a:build", step.GetTo())
	assert.True(t, step.GetForward(), "walked along the edge's own direction")
}

// Two real nodes with no chain between them is an ANSWER (found=false); an endpoint that
// resolves to nothing is the caller's mistake (NOT_FOUND). Collapsing the two would report
// "no path" for a typo.
func TestFindPathDistinguishesNoChainFromNoSuchNode(t *testing.T) {
	g := fixture()
	g.AddNode(types.KnowledgeNode{ID: "target:pkg/z:lonely", Kind: types.KindTarget, Label: "lonely"})
	svc := NewService(fakeResolver{g: g, probed: true})

	resp, err := svc.FindPath(context.Background(),
		connect.NewRequest(&graphv1.FindPathRequest{From: "target:pkg/a:test", To: "target:pkg/z:lonely"}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.GetFound())
	assert.Empty(t, resp.Msg.GetSteps())

	_, err = svc.FindPath(context.Background(),
		connect.NewRequest(&graphv1.FindPathRequest{From: "target:pkg/a:test", To: "target:nope:nope"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestResolveNodesRanksCandidates(t *testing.T) {
	resp, err := newService().ResolveNodes(context.Background(),
		connect.NewRequest(&graphv1.ResolveNodesRequest{Reference: "build", Limit: 5}))
	require.NoError(t, err)
	require.NotEmpty(t, resp.Msg.GetMatches())
	assert.Equal(t, "target:pkg/a:build", resp.Msg.GetMatches()[0].GetId())
}

func TestFindDependentsReturnsTheRebuildSet(t *testing.T) {
	resp, err := newService().FindDependents(context.Background(),
		connect.NewRequest(&graphv1.FindDependentsRequest{Name: "target:pkg/a:build"}))
	require.NoError(t, err)
	assert.Equal(t, "target:pkg/a:build", resp.Msg.GetNode())
	assert.ElementsMatch(t, []string{"target:pkg/a:test"}, resp.Msg.GetIds())
}

// The distinction the verb exists for: a node reached only by non-depends_on edges has NO
// dependents, however much of the graph points at it. Reporting blast_radius here instead would
// claim things rebuild that do not.
func TestFindDependentsIgnoresNonDependsOnEdges(t *testing.T) {
	g := fixture()
	g.AddNode(types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell, Label: "go"})
	g.AddEdge(types.KnowledgeEdge{
		Source: "target:pkg/a:build", Target: "spell:go", Relation: types.RelationUses,
		Confidence: types.ConfidenceExtracted, Score: 1,
	})
	svc := NewService(fakeResolver{g: g, probed: true})
	resp, err := svc.FindDependents(context.Background(),
		connect.NewRequest(&graphv1.FindDependentsRequest{Name: "spell:go"}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.GetIds(), "a spell is used, never depended on")
}

// An unresolvable name must not read as "nothing rebuilds" - that is the same shape as a real
// answer and the caller cannot tell them apart.
func TestFindDependentsUnresolvableIsNotFound(t *testing.T) {
	_, err := newService().FindDependents(context.Background(),
		connect.NewRequest(&graphv1.FindDependentsRequest{Name: "target:nope:nope"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestGetGraphStatsMapsCountsAndConnectivity(t *testing.T) {
	resp, err := newService().GetGraphStats(context.Background(),
		connect.NewRequest(&graphv1.GetGraphStatsRequest{}))
	require.NoError(t, err)
	assert.Equal(t, int32(3), resp.Msg.GetNodeCount())
	assert.Equal(t, int32(2), resp.Msg.GetEdgeCount())
	assert.Equal(t, int32(1), resp.Msg.GetComponentCount())
	assert.Equal(t, int32(3), resp.Msg.GetLargestComponentSize())
}

func TestEmptyRequestFieldsAreInvalidArgument(t *testing.T) {
	svc := newService()
	ctx := context.Background()
	_, err := svc.QueryNodes(ctx, connect.NewRequest(&graphv1.QueryNodesRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = svc.ResolveNodes(ctx, connect.NewRequest(&graphv1.ResolveNodesRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = svc.ExplainNode(ctx, connect.NewRequest(&graphv1.ExplainNodeRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = svc.FindPath(ctx, connect.NewRequest(&graphv1.FindPathRequest{From: "a"}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = svc.FindDependents(ctx, connect.NewRequest(&graphv1.FindDependentsRequest{}))
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestFindAffectedMapsProjectPathsToNodeIds(t *testing.T) {
	svc := NewService(fakeResolver{probed: true, aff: &types.AffectedResult{
		Base:     "main",
		Changed:  []string{"console/src/a.ts", "console/src/b.ts"},
		Affected: []string{"console", "docs"},
	}})
	resp, err := svc.FindAffected(context.Background(),
		connect.NewRequest(&graphv1.FindAffectedRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "main", resp.Msg.GetBase())
	assert.Equal(t, int32(2), resp.Msg.GetChangedFiles())
	assert.Equal(t, []string{"project:console", "project:docs"}, resp.Msg.GetIds())
	assert.Empty(t, resp.Msg.GetFallback())
}

// A diff that reaches nothing and a diff that could not be computed are different answers, and
// the view renders them differently - one says "nothing affected" and the other must not - so
// the fallback has to survive as data rather than collapsing into an error code.
func TestFindAffectedFallbackIsAnAnswerNotAnError(t *testing.T) {
	svc := NewService(fakeResolver{probed: true,
		affErr: fmt.Errorf("shallow clone: %w", types.ErrAffectedFallback)})
	resp, err := svc.FindAffected(context.Background(),
		connect.NewRequest(&graphv1.FindAffectedRequest{}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.GetIds())
	assert.Contains(t, resp.Msg.GetFallback(), "shallow clone")

	empty := NewService(fakeResolver{probed: true, aff: &types.AffectedResult{Base: "main"}})
	resp, err = empty.FindAffected(context.Background(),
		connect.NewRequest(&graphv1.FindAffectedRequest{}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.GetIds())
	assert.Empty(t, resp.Msg.GetFallback())
}

func TestFindAffectedOtherErrorsAreInternal(t *testing.T) {
	svc := NewService(fakeResolver{probed: true, affErr: errors.New("no workspace")})
	_, err := svc.FindAffected(context.Background(),
		connect.NewRequest(&graphv1.FindAffectedRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestGraphResolutionFailureIsInternal(t *testing.T) {
	svc := NewService(fakeResolver{err: errors.New("no workspace"), probed: true})
	_, err := svc.GetGraphStats(context.Background(),
		connect.NewRequest(&graphv1.GetGraphStatsRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}
