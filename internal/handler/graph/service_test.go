package graph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	graphv1 "github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// The engine's verbs are tested in internal/graph/knowledge. What is unverified here is the
// MAPPING (which domain field lands on which wire field, and which "not found" is an error
// versus an answer), so these assert that and lean on a small hand-built graph.
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

// cannedResolver hands back one canned graph for every resolution path, so the RPCs are
// unit-testable through the seam without a real workspace. probed=false models the coverage
// probe itself failing, which must not read as verified coverage.
type cannedResolver struct {
	g      *knowledge.Graph
	err    error
	gaps   []types.KnowledgeSymbolGap
	probed bool
	aff    *types.AffectedResult
	affErr error
}

func (f cannedResolver) KnowledgeGraph(context.Context, types.GraphRequest) (*knowledge.Graph, error) {
	return f.g, f.err
}

func (f cannedResolver) SymbolGaps(context.Context) ([]types.KnowledgeSymbolGap, bool) {
	return f.gaps, f.probed
}

func (f cannedResolver) Affected(context.Context, string) (*types.AffectedResult, error) {
	return f.aff, f.affErr
}

func (cannedResolver) Graph() (*types.Graph, error) { return nil, errors.New("canned: no project graph") }

func (cannedResolver) ClassifyFiles(context.Context, []string) ([]types.FileEntry, error) {
	return nil, nil
}

func newService() *Service { return NewService(cannedResolver{g: fixture(), probed: true}) }

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
	svc := NewService(cannedResolver{
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
	svc := NewService(cannedResolver{g: fixture(), probed: false})
	resp, err := svc.QueryNodes(context.Background(),
		connect.NewRequest(&graphv1.QueryNodesRequest{Query: "nothingmatchesthis"}))
	require.NoError(t, err)
	assert.Equal(t, string(types.VerdictUnknown), resp.Msg.GetAnswer().GetVerdict())
	assert.Equal(t, string(types.ReasonCoverageUnknown), resp.Msg.GetAnswer().GetReason())
	assert.Empty(t, resp.Msg.GetAnswer().GetGaps())
}

func TestQueryNodesFlattensSymbolGapProject(t *testing.T) {
	svc := NewService(cannedResolver{
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
		if e.GetRelation() == string(types.RelationDependsOn) {
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
	svc := NewService(cannedResolver{g: g, probed: true})

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
	svc := NewService(cannedResolver{g: g, probed: true})
	resp, err := svc.FindDependents(context.Background(),
		connect.NewRequest(&graphv1.FindDependentsRequest{Name: "spell:go"}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.GetIds(), "a spell is used, never depended on")
}

// An unresolvable name must not read as "nothing rebuilds"; that is the same shape as a real
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

// A diff that reaches nothing and a diff that could not be computed are different answers, so the
// fallback has to survive as data rather than collapsing into an error code.
func TestFindAffectedFallbackIsAnAnswerNotAnError(t *testing.T) {
	svc := NewService(cannedResolver{probed: true,
		affErr: fmt.Errorf("shallow clone: %w", types.ErrAffectedFallback)})
	resp, err := svc.FindAffected(context.Background(),
		connect.NewRequest(&graphv1.FindAffectedRequest{}))
	require.NoError(t, err)
	assert.Empty(t, resp.Msg.GetIds())
	assert.Contains(t, resp.Msg.GetFallback(), "shallow clone")
}

// An empty plan would read as "nothing runs".
func TestFindAffectedWithoutAPlannerIsUnimplemented(t *testing.T) {
	svc := NewService(cannedResolver{probed: true, aff: &types.AffectedResult{Base: "main"}})
	_, err := svc.FindAffected(context.Background(),
		connect.NewRequest(&graphv1.FindAffectedRequest{}))
	assert.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

type findAffectedSuite struct {
	suite.Suite
	svc *Service
}

func TestFindAffected(t *testing.T) { suite.Run(t, new(findAffectedSuite)) }

// app depends on lib, and the working tree edits a file in lib.
func (s *findAffectedSuite) SetupTest() {
	repo := testkit.NewRepository(s.T(), map[string]string{
		"magusfile.buzz":     "",
		"lib/magusfile.buzz": "",
		"lib/lib.txt":        "one\n",
		"app/magusfile.buzz": "import \"magus\";\nmagus\\project({\"depends_on\": [\"../lib\"]});\n",
	})
	repo.Write("lib/lib.txt", "two\n")
	m, err := magus.Open(context.Background(), repo.Root(), magus.WithSpawnRule(tier.Ceiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	s.svc = NewService(workspace.ViewAs[resolver](m))
}

func (s *findAffectedSuite) find() *graphv1.Affected {
	resp, err := s.svc.FindAffected(context.Background(),
		connect.NewRequest(&graphv1.FindAffectedRequest{Base: "HEAD"}))
	s.Require().NoError(err)
	s.Require().Empty(resp.Msg.GetFallback())
	return resp.Msg
}

func (s *findAffectedSuite) TestSeedsNameThePathItsProjectAndRole() {
	msg := s.find()
	s.Equal([]string{"project:app", "project:lib"}, msg.GetIds())
	files, err := s.svc.ws.ClassifyFiles(context.Background(), []string{"lib/lib.txt"})
	s.Require().NoError(err)
	s.Require().Len(files, 1)
	s.Require().Len(msg.GetSeeds(), 1)
	seed := msg.GetSeeds()[0]
	s.Equal("lib/lib.txt", seed.GetPath())
	s.Equal("project:lib", seed.GetProject())
	s.NotEmpty(seed.GetRole())
	s.Equal(files[0].Role, seed.GetRole(), "the role is the one `magus describe file` gives")
}

func (s *findAffectedSuite) TestADependentCarriesTheChainFromItsSeed() {
	msg := s.find()
	s.Require().Len(msg.GetReasons(), 2)
	app, lib := msg.GetReasons()[0], msg.GetReasons()[1]

	s.Equal("project:app", app.GetId())
	s.False(app.GetSeed())
	s.Require().Len(app.GetChains(), 1)
	s.Equal("project:lib", app.GetChains()[0].GetSeed())
	s.Require().Len(app.GetChains()[0].GetSteps(), 1)
	step := app.GetChains()[0].GetSteps()[0]
	s.Equal("project:lib", step.GetFrom())
	s.Equal("project:app", step.GetTo())
	s.Equal(string(types.RelationDependsOn), step.GetRelation())
	s.False(step.GetForward(), "the dependent is the depends_on edge's source")

	s.Equal("project:lib", lib.GetId())
	s.True(lib.GetSeed())
	for _, c := range lib.GetChains() {
		s.Empty(c.GetSteps(), "a seed reaches itself in no steps")
	}
}

func (s *findAffectedSuite) TestThePlanRunsTheAffectedProjects() {
	plan := s.find().GetPlan()
	s.Equal(types.TargetCI, plan.GetTarget(), "an empty target plans ci")
	s.Equal("magus affected ci --base HEAD", plan.GetCommand())
	projects := map[string]bool{}
	for _, u := range plan.GetUnits() {
		projects[u.GetProject()] = true
	}
	s.Equal(map[string]bool{"project:app": true, "project:lib": true}, projects,
		"the plan runs what the diff reaches and nothing else")
}

func TestFindAffectedOtherErrorsAreInternal(t *testing.T) {
	svc := NewService(cannedResolver{probed: true, affErr: errors.New("no workspace")})
	_, err := svc.FindAffected(context.Background(),
		connect.NewRequest(&graphv1.FindAffectedRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestGraphResolutionFailureIsInternal(t *testing.T) {
	svc := NewService(cannedResolver{err: errors.New("no workspace"), probed: true})
	_, err := svc.GetGraphStats(context.Background(),
		connect.NewRequest(&graphv1.GetGraphStatsRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// statusResolver is cannedResolver plus the index freshness probe a position query needs.
type statusResolver struct {
	cannedResolver
	status []types.SymbolIndexStatus
}

func (f statusResolver) SymbolIndexStatus(context.Context) []types.SymbolIndexStatus { return f.status }

type queryPositionSuite struct {
	suite.Suite
	g *knowledge.Graph
}

func TestQueryPosition(t *testing.T) { suite.Run(t, new(queryPositionSuite)) }

// lib/a.buzz defines parse on line 2 under a doc comment; app/main.buzz holds the cursor.
func (s *queryPositionSuite) SetupTest() {
	root := s.T().TempDir()
	for path, text := range map[string]string{
		"lib/a.buzz":    "// parse reads a config.\nfun parse() > void {}\n",
		"app/main.buzz": "parse()\n",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		s.Require().NoError(os.MkdirAll(filepath.Dir(full), 0o755))
		s.Require().NoError(os.WriteFile(full, []byte(text), 0o600))
	}
	g := knowledge.NewGraph()
	g.SetRoot(root)
	for _, path := range []string{"lib/a.buzz", "app/main.buzz"} {
		g.AddNode(types.KnowledgeNode{ID: "file:" + path, Kind: types.KindFile, Label: path, Source: path, Attrs: map[string]string{knowledge.AttrLanguage: "buzz"}})
	}
	g.AddNode(types.KnowledgeNode{ID: "symbol:a/parse", Kind: types.KindSymbol, Label: "parse", Source: "lib/a.buzz:2"})
	g.AddEdge(types.KnowledgeEdge{Source: "file:lib/a.buzz", Target: "symbol:a/parse", Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1})
	s.g = g
}

func (s *queryPositionSuite) service(status ...types.SymbolIndexStatus) *Service {
	if status == nil {
		status = []types.SymbolIndexStatus{{Project: types.ProjectRef{Path: "."}, Language: "buzz", Freshness: types.SymbolIndexFresh}}
	}
	return NewService(statusResolver{cannedResolver: cannedResolver{g: s.g, probed: true}, status: status})
}

func (s *queryPositionSuite) request() *connect.Request[graphv1.QueryNodesRequest] {
	return connect.NewRequest(&graphv1.QueryNodesRequest{Position: &graphv1.Position{Path: "app/main.buzz", Offset: 5, Prefix: "parse(", Suffix: ")\n"}})
}

func (s *queryPositionSuite) TestCarriesTheVerifiedContext() {
	resp, err := s.service().QueryNodes(context.Background(), s.request())
	s.Require().NoError(err)
	s.Equal(int32(1), resp.Msg.GetMatchCount())
	s.Require().Len(resp.Msg.GetContext(), 1)
	site := resp.Msg.GetContext()[0]
	s.Equal("lib/a.buzz", site.GetFile())
	s.Equal(int32(2), site.GetStartLine())
	s.Equal(int32(1), site.GetSourceStartLine())
	s.Equal("// parse reads a config.\nfun parse() > void {}", site.GetSource())
	s.Equal(string(types.DefinitionVerified), site.GetStatus())
	s.Equal("parse", site.GetSymbol())
	s.Equal("symbol:a/parse", site.GetMatchId())
}

func (s *queryPositionSuite) TestExcludedPathsLeaveNoContext() {
	req := s.request()
	req.Msg.ExcludePaths = []string{"lib"}
	resp, err := s.service().QueryNodes(context.Background(), req)
	s.Require().NoError(err)
	s.Zero(resp.Msg.GetMatchCount())
	s.Empty(resp.Msg.GetContext())
}

func (s *queryPositionSuite) TestAStaleIndexIsAFailedPrecondition() {
	_, err := s.service(types.SymbolIndexStatus{Project: types.ProjectRef{Path: "."}, Language: "buzz", Freshness: types.SymbolIndexStale}).
		QueryNodes(context.Background(), s.request())
	s.Require().Error(err)
	s.Equal(connect.CodeFailedPrecondition, connect.CodeOf(err))
	s.Contains(err.Error(), "stale")
}

// The cursor's own file is newer than its index while it is edited; that alone answers.
func (s *queryPositionSuite) TestAnIndexStaleOnlyForTheOwnFileAnswers() {
	resp, err := s.service(types.SymbolIndexStatus{Project: types.ProjectRef{Path: "."}, Language: "buzz", Freshness: types.SymbolIndexStale,
		Changed: []string{"app/main.buzz"}, ChangedCount: 1}).QueryNodes(context.Background(), s.request())
	s.Require().NoError(err)
	s.Len(resp.Msg.GetContext(), 1, "lib/a.buzz did not change, so its site still verifies")
}

func (s *queryPositionSuite) TestAnIndexStaleForAnotherFileIsAFailedPrecondition() {
	_, err := s.service(types.SymbolIndexStatus{Project: types.ProjectRef{Path: "."}, Language: "buzz", Freshness: types.SymbolIndexStale,
		Changed: []string{"app/main.buzz", "lib/a.buzz"}, ChangedCount: 2}).QueryNodes(context.Background(), s.request())
	s.Equal(connect.CodeFailedPrecondition, connect.CodeOf(err))
}

func (s *queryPositionSuite) TestAWorkspaceWithNoFreshnessProbeIsUnimplemented() {
	_, err := NewService(cannedResolver{g: s.g, probed: true}).QueryNodes(context.Background(), s.request())
	s.Equal(connect.CodeUnimplemented, connect.CodeOf(err))
}

func (s *queryPositionSuite) TestFreeTextBesideAPositionIsInvalidArgument() {
	req := s.request()
	req.Msg.Query = "parse"
	_, err := s.service().QueryNodes(context.Background(), req)
	s.Equal(connect.CodeInvalidArgument, connect.CodeOf(err))
}

func (s *queryPositionSuite) TestAPositionWithNoPathIsInvalidArgument() {
	req := s.request()
	req.Msg.Position.Path = ""
	_, err := s.service().QueryNodes(context.Background(), req)
	s.Equal(connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestQueryNodesExcludePathsDropsMatches(t *testing.T) {
	g := fixture()
	g.AddNode(types.KnowledgeNode{ID: "file:held/x.buzz", Kind: types.KindFile, Label: "held/x.buzz", Source: "held/x.buzz"})
	resp, err := NewService(cannedResolver{g: g, probed: true}).QueryNodes(context.Background(),
		connect.NewRequest(&graphv1.QueryNodesRequest{Query: "kind:file", ExcludePaths: []string{"held"}}))
	require.NoError(t, err)
	assert.Zero(t, resp.Msg.GetMatchCount())
}
