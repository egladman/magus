package job

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func tierSet(t *testing.T, bounds ...tier.Bounds) *tier.Set {
	t.Helper()
	if bounds == nil {
		bounds = []tier.Bounds{{MaxLive: 2}, {}, {}}
	}
	set, err := tier.NewSet(tier.Declaration{
		Enum:    "ModelTier",
		Site:    "magusfile.buzz:3",
		Cases:   []string{"routine", "bounded", "design"},
		Default: 1,
		Bounds:  bounds,
		Skills:  []tier.SkillVariant{tier.SkillVariantFull, tier.SkillVariantShort, tier.SkillVariantShort},
		Hosts: map[string][]string{
			"claude-code": {"haiku", "sonnet", "opus"},
			"other-host":  {"mini", "sonnet", "max"},
		},
	})
	require.NoError(t, err)
	return set
}

type countingMatrix struct {
	calls  int
	seen   types.ModelTierRequest
	answer *tier.Assignment
	err    error
}

func (m *countingMatrix) choose(_ context.Context, req types.ModelTierRequest) (*tier.Assignment, error) {
	m.calls++
	m.seen = req
	return m.answer, m.err
}

// requestedModelSuite pins how a requested {id, tier} resolves.
type requestedModelSuite struct {
	suite.Suite
}

func TestRequestedModel(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(requestedModelSuite))
}

func (s *requestedModelSuite) resolve(m job.Model) (tier.Assignment, error) {
	row := job.Job{ID: "a", Agent: &job.Agent{Model: m, Worker: job.Worker{Type: job.WorkerTypeResumed}}}
	return resolveModelTier(s.T().Context(), tierSet(s.T()), nil, nil, types.ModelTierRequest{}, row)
}

func (s *requestedModelSuite) TestATierAndTheIDItMapsToAgree() {
	got, err := s.resolve(job.Model{ID: "opus", Tier: "design"})
	s.Require().NoError(err)
	s.Equal(tier.Assignment{
		Name: "design", Rank: 2, Skills: tier.SkillVariantShort, Source: tier.SourceJob,
		Reason: "the job names the tier design", Model: "opus",
	}, got, "the tier decides, and the id is the model the host spawns")
}

func (s *requestedModelSuite) TestATierAndAnIDOfAnotherTierAreRefused() {
	_, err := s.resolve(job.Model{ID: "haiku", Tier: "design"})
	s.ErrorContains(err, "the job requests the tier design and the model haiku, which harness claude-code maps to the tier routine")
}

func (s *requestedModelSuite) TestATierTheWorkspaceDoesNotDeclareIsRefused() {
	_, err := s.resolve(job.Model{Tier: "opus"})
	s.ErrorContains(err, "the job requests the tier opus")
}

func (s *requestedModelSuite) TestAnIDNamingATierIsNotReadAsOne() {
	got, err := s.resolve(job.Model{ID: "design"})
	s.Require().NoError(err)
	s.Equal("bounded", got.Name, "an id no host maps takes the default")
}

func (s *requestedModelSuite) TestTheStoreReadsAModelWord() {
	st := NewStore(Location{ModelTiers: tierSet(s.T())})
	s.Equal(job.Model{Tier: "design"}, st.ModelRequest("design"))
	s.Equal(job.Model{ID: "opus"}, st.ModelRequest("opus"))
	s.Equal(job.Model{ID: "design"}, NewStore(Location{}).ModelRequest("design"), "with no tiers every word is an id")
}

func routineAnswer() *tier.Assignment {
	return &tier.Assignment{Name: "routine", Rank: 0, Skills: tier.SkillVariantFull, Reason: "small checked job", Site: "magusfile.buzz:20"}
}

func TestNewModelTierRequestDerivesKindFromTheRow(t *testing.T) {
	t.Parallel()

	target := &types.LeaseCheck{Target: "test", Project: "."}
	script := &types.LeaseCheck{Script: "probe.buzz"}
	symbol := job.Goal{ID: "moved", Kind: job.GoalKindSymbol, Symbols: []string{"A"}}
	paths := job.Goal{ID: "touched", Kind: job.GoalKindPaths, Paths: []string{"a.go"}}
	tests := []struct {
		name string
		row  job.Job
		want types.JobKind
	}{
		{"a script check outranks everything", job.Job{Check: rawOf(*script), Goals: []job.Goal{symbol, paths}}, types.JobKindScript},
		{"a script check inside a goal counts", job.Job{Goals: []job.Goal{{ID: "g", Kind: job.GoalKindCheck, Check: rawOf(*script)}, symbol}}, types.JobKindScript},
		{"symbol goals outrank paths and a target check", job.Job{Check: rawOf(*target), Goals: []job.Goal{paths, symbol}}, types.JobKindSymbol},
		{"path goals outrank a target check", job.Job{Check: rawOf(*target), Goals: []job.Goal{paths}}, types.JobKindPaths},
		{"a target check alone", job.Job{Check: rawOf(*target)}, types.JobKindCheck},
		{"nothing graded is prose", job.Job{Agent: job.Instructed("tidy the docs")}, types.JobKindProse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, requestOf(t, tt.row, nil, nil, nil, nil).Kind)
		})
	}
}

func TestNewModelTierRequestCountsFilesOnce(t *testing.T) {
	t.Parallel()

	row := job.Job{Paths: job.Paths{Write: []string{"internal/job/*.go", "run.go#func A", "run.go#func B", "docs", "docs"}}}
	assert.Equal(t, 3, requestOf(t, row, nil, nil, nil, nil).WriteFiles,
		"a glob is one, and two claims in one file are that file once")
}

// tierRequest is an agent section requesting a tier, resumed.
func tierRequest(name string) *job.Agent {
	return &job.Agent{Model: job.Model{Tier: name}, Worker: job.Worker{Type: job.WorkerTypeResumed}}
}

// modelRequest is an agent section requesting a host model id, resumed.
func modelRequest(id string) *job.Agent {
	return &job.Agent{Model: job.Model{ID: id}, Worker: job.Worker{Type: job.WorkerTypeResumed}}
}

func TestNewModelTierRequestCarriesTheRowAndItsParentsRank(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	parent := job.Job{ID: "wave", ModelTier: &tier.Assignment{Name: "design", Rank: 2}}
	row := job.Job{
		ID: "wave/a", Parent: "wave", Agent: &job.Agent{Instructions: "do it", Model: job.Model{Tier: "routine"}, Worker: job.Worker{Type: job.WorkerTypeResumed}}, ReadOnly: false,
		Paths: job.Paths{Write: []string{"a.go"}, Read: []string{"b"}}, DependsOn: []string{"wave/b"},
		Check: rawOf(types.LeaseCheck{Target: "test", Project: ".", Args: []string{"-run", "X"}}),
		Goals: []job.Goal{{ID: "g", Kind: job.GoalKindPaths, Paths: []string{"a.go"}}},
	}
	history := []types.ModelTierRecord{{Rank: 0, ModelTier: "routine", Jobs: 12}}

	req := requestOf(t, row, []job.Job{parent, row}, set, nil, history)

	require.NotNil(t, req.ParentRank)
	assert.Equal(t, 2, *req.ParentRank)
	require.NotNil(t, req.DeclaredRank)
	assert.Equal(t, 0, *req.DeclaredRank)
	wantReq := req
	view, err := types.NewJob(row)
	require.NoError(t, err)
	wantReq.Model, wantReq.History, wantReq.Goals = job.Model{Tier: "routine"}, history, view.Goals
	wantReq.WritePaths, wantReq.ReadPaths, wantReq.DependsOn = []string{"a.go"}, []string{"b"}, []string{"wave/b"}
	assert.Equal(t, wantReq, req)
	assert.Empty(t, req.WriteProjects, "a project is a graph fact the store does not hold")

	req.Check.Args[0] = "changed"
	assert.Equal(t, "-run", checkIn(t, row.Check).Args[0], "the request owns its copy of the check")
}

func TestNewModelTierRequestLeavesRanksNilWhenNoneIsKnown(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	orphan := job.Job{ID: "a", Parent: "gone", Agent: modelRequest("gpt")}
	req := requestOf(t, orphan, nil, set, nil, nil)
	assert.Nil(t, req.ParentRank, "a parent that is not in the plan has no rank")
	assert.Nil(t, req.DeclaredRank, "a model that names no tier declares no rank")

	viaHost := requestOf(t, job.Job{ID: "b", Agent: modelRequest("opus")}, nil, set, nil, nil)
	require.NotNil(t, viaHost.DeclaredRank)
	assert.Equal(t, 2, *viaHost.DeclaredRank, "a host alias declares its tier's rank")

	untiered := job.Job{ID: "wave"}
	req = requestOf(t, job.Job{ID: "wave/a", Parent: "wave"}, []job.Job{untiered}, set, nil, nil)
	assert.Nil(t, req.ParentRank, "a parent without a tier has no rank")
}

func TestAssignModelTierPrecedence(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	tests := []struct {
		name        string
		row         job.Job
		matrix      countingMatrix
		want        tier.Assignment
		matrixCalls int
	}{
		{
			name:        "a row naming a tier wins and the matrix never runs",
			row:         job.Job{ID: "a", Agent: tierRequest("design")},
			matrix:      countingMatrix{answer: routineAnswer()},
			want:        tier.Assignment{Name: "design", Rank: 2, Skills: tier.SkillVariantShort, Source: tier.SourceJob, Reason: "the job names the tier design"},
			matrixCalls: 0,
		},
		{
			name:   "a model naming no tier is kept with the default's rank",
			row:    job.Job{ID: "a", Agent: modelRequest("gpt")},
			matrix: countingMatrix{answer: routineAnswer()},
			want: tier.Assignment{
				Name: "bounded", Rank: 1, Skills: tier.SkillVariantShort, Source: tier.SourceJob,
				Reason: "the job names the model gpt, which no model tier maps to", Model: "gpt",
			},
			matrixCalls: 0,
		},
		{
			name:   "a model a host maps to a tier takes that tier and keeps the model",
			row:    job.Job{ID: "a", Agent: modelRequest("opus")},
			matrix: countingMatrix{answer: routineAnswer()},
			want: tier.Assignment{
				Name: "design", Rank: 2, Skills: tier.SkillVariantShort, Source: tier.SourceJob,
				Reason: "the job names the model opus, which harness claude-code maps to the tier design", Model: "opus",
			},
			matrixCalls: 0,
		},
		{
			name:   "an alias two hosts map to the same tier is not ambiguous",
			row:    job.Job{ID: "a", Agent: modelRequest("sonnet")},
			matrix: countingMatrix{},
			want: tier.Assignment{
				Name: "bounded", Rank: 1, Skills: tier.SkillVariantShort, Source: tier.SourceJob,
				Reason: "the job names the model sonnet, which harness claude-code maps to the tier bounded", Model: "sonnet",
			},
			matrixCalls: 0,
		},
		{
			name:        "the matrix answers when the row names nothing",
			row:         job.Job{ID: "a"},
			matrix:      countingMatrix{answer: routineAnswer()},
			want:        tier.Assignment{Name: "routine", Rank: 0, Skills: tier.SkillVariantFull, Source: tier.SourceMatrix, Reason: "small checked job", Site: "magusfile.buzz:20"},
			matrixCalls: 1,
		},
		{
			name:        "a matrix that declines leaves the default",
			row:         job.Job{ID: "a"},
			matrix:      countingMatrix{},
			want:        tier.Assignment{Name: "bounded", Rank: 1, Skills: tier.SkillVariantShort, Source: tier.SourceDefault, Reason: "the declared default", Site: "magusfile.buzz:3"},
			matrixCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := requestOf(t, tt.row, nil, set, nil, nil)
			got, err := resolveModelTier(t.Context(), set, nil, tt.matrix.choose, req, tt.row)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.matrixCalls, tt.matrix.calls)
		})
	}
}

func TestAssignModelTierWithoutAMatrixTakesTheDefault(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	row := job.Job{ID: "a"}
	got, err := resolveModelTier(t.Context(), set, nil, nil, requestOf(t, row, nil, set, nil, nil), row)
	require.NoError(t, err)
	wantTier := got
	wantTier.Source, wantTier.Name = tier.SourceDefault, "bounded"
	wantTier.Site = "magusfile.buzz:3" // the default record names the declaration
	assert.Equal(t, wantTier, got)
}

func TestResolveModelTierReadsHarnessModelRowAliases(t *testing.T) {
	t.Parallel()

	set, err := tier.NewSet(tier.Declaration{
		Enum: "ModelTier", Site: "magusfile.buzz:3", Cases: []string{"routine", "bounded", "design"}, Default: 1,
		Bounds: []tier.Bounds{{}, {}, {}},
		Skills: []tier.SkillVariant{tier.SkillVariantFull, tier.SkillVariantShort, tier.SkillVariantShort},
	})
	require.NoError(t, err)
	models := []model.Row{
		{ID: "claude-haiku-9", Alias: "haiku", Tier: "routine"},
		{ID: "claude-sonnet-9", Alias: "sonnet", Tier: "bounded"},
		{ID: "claude-opus-9", Alias: "opus", Tier: "design"},
		{ID: "claude-untiered-9", Alias: "loose"},
	}
	tests := []struct {
		model    string
		wantTier string
		wantRank int
		wantRow  bool
	}{
		{"haiku", "routine", 0, true},
		{"sonnet", "bounded", 1, true},
		{"opus", "design", 2, true},
		{"claude-opus-9", "design", 2, true},
		{"loose", "bounded", 1, false},
		{"unlisted", "bounded", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()

			row := job.Job{ID: "a", Agent: modelRequest(tt.model)}
			req := requestOf(t, row, nil, set, models, nil)
			got, err := resolveModelTier(t.Context(), set, models, nil, req, row)
			require.NoError(t, err)
			reason := got.Reason
			got.Reason = ""
			assert.Equal(t, tier.Assignment{Name: tt.wantTier, Rank: tt.wantRank, Model: tt.model, Skills: got.Skills, Source: got.Source, Site: got.Site}, got)
			assert.Equal(t, tt.wantRow, req.DeclaredRank != nil)
			if tt.wantRow {
				assert.NotContains(t, reason, "no model tier maps")
			} else {
				assert.Contains(t, reason, "no model tier maps")
			}
		})
	}
}

func TestAssignModelTierRefusesAnAliasThatNamesTwoTiers(t *testing.T) {
	t.Parallel()

	set, err := tier.NewSet(tier.Declaration{
		Enum: "ModelTier", Site: "magusfile.buzz:3", Cases: []string{"low", "high"}, Default: 0,
		Bounds: []tier.Bounds{{}, {}},
		Skills: []tier.SkillVariant{tier.SkillVariantFull, tier.SkillVariantShort},
		Hosts: map[string][]string{
			"a": {"x", "y"},
			"b": {"y", "x"},
		},
	})
	require.NoError(t, err)
	row := job.Job{ID: "j", Agent: modelRequest("x")}
	_, err = resolveModelTier(t.Context(), set, nil, nil, requestOf(t, row, nil, set, nil, nil), row)
	require.Error(t, err)
	assert.ErrorContains(t, err, "name a tier instead")
	assert.ErrorContains(t, err, "harness a")
	assert.ErrorContains(t, err, "harness b")
}

func TestAssignModelTierHandsTheMatrixItsRequest(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	row := job.Job{ID: "a", Paths: job.Paths{Write: []string{"a.go"}}, Check: rawOf(types.LeaseCheck{Target: "test", Project: "."})}
	matrix := countingMatrix{}
	req := requestOf(t, row, nil, set, nil, nil)
	_, err := resolveModelTier(t.Context(), set, nil, matrix.choose, req, row)
	require.NoError(t, err)
	assert.Equal(t, req, matrix.seen)
}

func TestAssignModelTierRefusesAMatrixThatIsWrong(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	row := job.Job{ID: "a"}
	tests := []struct {
		name   string
		matrix countingMatrix
		want   string
	}{
		{"it raises", countingMatrix{err: errors.New("magus\\modelTier: the matrix raised: boom")}, "boom"},
		{"a tier the declaration lacks", countingMatrix{answer: &tier.Assignment{Name: "huge", Skills: tier.SkillVariantFull, Reason: "r"}}, `"huge" is not a model tier`},
		{"a rank the declaration disagrees with", countingMatrix{answer: &tier.Assignment{Name: "routine", Rank: 2, Skills: tier.SkillVariantFull, Reason: "r"}}, "ranks it 0"},
		{"skills that are no variant", countingMatrix{answer: &tier.Assignment{Name: "routine", Skills: "tiny", Reason: "r"}}, "the variants are"},
		{"no skills", countingMatrix{answer: &tier.Assignment{Name: "routine", Reason: "r"}}, "the variants are"},
		{"no reason", countingMatrix{answer: &tier.Assignment{Name: "routine", Skills: tier.SkillVariantFull}}, "gave no reason"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveModelTier(t.Context(), set, nil, tt.matrix.choose, requestOf(t, row, nil, set, nil, nil), row)
			require.Error(t, err, "a wrong matrix fails the assignment, it never falls through to the default")
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestAssignModelTierWithoutDeclarationFails(t *testing.T) {
	t.Parallel()

	_, err := resolveModelTier(t.Context(), nil, nil, nil, types.ModelTierRequest{}, job.Job{ID: "a"})
	require.Error(t, err)
}

func TestRefuseModelTierLimits(t *testing.T) {
	t.Parallel()

	set := tierSet(t, tier.Bounds{MaxLive: 2}, tier.Bounds{}, tier.Bounds{MaxLive: 1})
	at := func(id, name string, state job.State) job.Job {
		return job.Job{ID: id, State: state, ModelTier: &tier.Assignment{Name: name}}
	}
	routine := tier.Assignment{Name: "routine", Rank: 0}
	tests := []struct {
		name     string
		rows     []job.Job
		assigned tier.Assignment
		wantErr  string
	}{
		{"under the bound", []job.Job{at("a", "routine", job.StateRunning)}, routine, ""},
		{"at the bound", []job.Job{at("a", "routine", job.StateRunning), at("b", "routine", job.StateDeclared)}, routine, "3 live jobs at model tier routine"},
		{"ended jobs do not count", []job.Job{at("a", "routine", job.StatePass), at("b", "routine", job.StateFail), at("c", "routine", job.StateRunning)}, routine, ""},
		{"other tiers do not count", []job.Job{at("a", "design", job.StateRunning), at("b", "design", job.StateRunning)}, routine, ""},
		{"a row without a tier does not count", []job.Job{{ID: "a", State: job.StateRunning}, {ID: "b", State: job.StateRunning}}, routine, ""},
		{"no bound is unlimited", []job.Job{at("a", "bounded", job.StateRunning), at("b", "bounded", job.StateRunning), at("c", "bounded", job.StateRunning)}, tier.Assignment{Name: "bounded", Rank: 1}, ""},
		{"a bound of one", []job.Job{at("a", "design", job.StateExited)}, tier.Assignment{Name: "design", Rank: 2}, "allows 1"},
		{"a tier the declaration lacks", nil, tier.Assignment{Name: "huge"}, `"huge" is not a model tier`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := refuseModelTierLimits(tt.rows, tt.assigned, set)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestRefuseModelTierLimitsNamesTheBoundAndTheWayOut(t *testing.T) {
	t.Parallel()

	set := tierSet(t, tier.Bounds{MaxLive: 1}, tier.Bounds{}, tier.Bounds{})
	rows := []job.Job{{ID: "a", State: job.StateRunning, ModelTier: &tier.Assignment{Name: "routine"}}}
	err := refuseModelTierLimits(rows, tier.Assignment{Name: "routine"}, set)
	require.Error(t, err)
	assert.ErrorContains(t, err, `MaxLive bound declared at magusfile.buzz:3 allows 1`)
	assert.ErrorContains(t, err, "job exit")
}

func tieredStore(t *testing.T, set *tier.Set, matrix func(context.Context, types.ModelTierRequest) (*tier.Assignment, error)) *Store {
	t.Helper()
	loc := tempLocation(t, t.TempDir())
	loc.ModelTiers, loc.ModelTierMatrix, loc.SpawnRule = set, matrix, tierWithin
	loc.SpawnCeiling = ceilingAt(builtin.Setting{Decision: builtin.Advise})
	return NewStore(loc)
}

// ceilingAt reads spawn-above-parent-tier as s.
func ceilingAt(s builtin.Setting) func(context.Context) (builtin.Setting, error) {
	return func(context.Context) (builtin.Setting, error) { return s, nil }
}

// denied is spawn-above-parent-tier set to deny.
var denied = builtin.Setting{Decision: builtin.Deny}

// tierWithin is tier.Ceiling's ordering without its allowance for an unassigned tier.
func tierWithin(parent, child tier.Tier) error { return child.Within(parent) }

func TestStoreAssignsTheTierWhenARowIsCreated(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: routineAnswer()}
	s := tieredStore(t, tierSet(t), matrix.choose)
	row := lease("a")
	row.Agent.Model = job.Model{}

	stored := seed(t, s, row)

	require.NotNil(t, stored.ModelTier)
	assert.Equal(t, tier.Assignment{
		Name: "routine", Rank: 0, Skills: tier.SkillVariantFull, Source: tier.SourceMatrix,
		Reason: "small checked job", Site: "magusfile.buzz:20",
	}, *stored.ModelTier)
	assert.Equal(t, 1, matrix.calls)
	assert.Equal(t, "a", matrix.seen.ID)

	rows, err := s.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, stored.ModelTier, rows[0].ModelTier, "the tier is stored, not recomputed on read")
}

func TestStoreRecordsTheDefaultForAWorkspaceThatDeclaresNoMatrix(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	row := lease("a")
	row.Agent.Model = job.Model{}

	stored := seed(t, s, row)

	require.NotNil(t, stored.ModelTier)
	wantStored := *stored.ModelTier
	wantStored.Source, wantStored.Name = tier.SourceDefault, "bounded"
	assert.Equal(t, wantStored, *stored.ModelTier)
}

func TestStoreReassignsOnlyWhenTheModelChanges(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: routineAnswer()}
	s := tieredStore(t, tierSet(t), matrix.choose)
	row := lease("a")
	row.Agent.Model = job.Model{}
	seed(t, s, row)
	require.Equal(t, 1, matrix.calls)

	row.Agent = job.Instructed("reworded")
	same := seed(t, s, row)
	assert.Equal(t, 1, matrix.calls, "an edit that leaves the model alone does not run the matrix again")
	assert.Equal(t, "routine", same.ModelTier.Name)

	row.Agent.Model = job.Model{Tier: "design"}
	changed := seed(t, s, row)
	assert.Equal(t, 1, matrix.calls, "a row that names a tier never reaches the matrix")
	wantChanged := *changed.ModelTier
	wantChanged.Name, wantChanged.Source = "design", tier.SourceJob
	assert.Equal(t, wantChanged, *changed.ModelTier)
}

func TestStoreKeepsTheStoredTierAgainstAWholeRowWrite(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	row := lease("a")
	row.Agent.Model = job.Model{}
	first := seed(t, s, row)

	row.ModelTier = &tier.Assignment{Name: "design", Rank: 2, Source: tier.SourceJob, Reason: "forged"}
	again := seed(t, s, row)
	assert.Equal(t, first.ModelTier, again.ModelTier, "a caller's tier is replaced by the stored one")

	forged := lease("b")
	forged.Agent.Model = job.Model{}
	forged.ModelTier = &tier.Assignment{Name: "design", Rank: 2, Source: tier.SourceJob, Reason: "forged"}
	created := seed(t, s, forged)
	assert.Equal(t, "bounded", created.ModelTier.Name, "a created row is assigned, whatever it sent")
}

func TestStoreAssignsNothingWithoutTiers(t *testing.T) {
	t.Parallel()

	s := tempStore(t, t.TempDir())
	row := lease("a")
	row.Agent.Model = job.Model{}
	assert.Nil(t, seed(t, s, row).ModelTier)
}

func TestStoreRefusesAForkPastAMaxLiveBound(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: routineAnswer()}
	s := tieredStore(t, tierSet(t, tier.Bounds{MaxLive: 1}, tier.Bounds{}, tier.Bounds{}), matrix.choose)
	first := lease("a")
	first.Agent.Model = job.Model{}
	seed(t, s, first)

	second := lease("b")
	second.Agent.Model = job.Model{}
	_, err := s.Update(t.Context(), "b", func(cur *job.Job) { *cur = second })
	require.Error(t, err)
	assert.ErrorContains(t, err, "MaxLive")

	rows, err := s.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1, "a refused fork writes nothing")

	_, err = s.Update(t.Context(), "a", func(cur *job.Job) { cur.Agent = job.Instructed("edited") })
	require.NoError(t, err, "the job already at the bound is not counted against itself")
}

func TestStoreFailsAForkWhoseMatrixFails(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{err: errors.New("magus\\modelTier: the matrix raised: boom")}
	s := tieredStore(t, tierSet(t), matrix.choose)
	row := lease("a")
	row.Agent.Model = job.Model{}
	_, err := s.Update(t.Context(), "a", func(cur *job.Job) { *cur = row })
	require.Error(t, err)
	assert.ErrorContains(t, err, "magus\\modelTier")

	rows, listErr := s.List(t.Context())
	require.NoError(t, listErr)
	assert.Empty(t, rows)
}

func TestApplyDryRunAssignsNothingAndRunsNoMatrix(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: routineAnswer()}
	s := tieredStore(t, tierSet(t), matrix.choose)
	record := job.Declaration{ID: "wave/job", Paths: job.Paths{Write: []string{"a.go"}}, Check: forkCheck()}

	plan, err := Apply(t.Context(), s, []job.Declaration{record}, config.Jobs{}, nil, nil, true)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Nil(t, plan[0].Next.ModelTier)
	assert.Zero(t, matrix.calls)
	rows, err := s.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, rows)

	plan, err = Apply(t.Context(), s, []job.Declaration{record}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)
	require.NotNil(t, plan[0].Next.ModelTier)
	assert.Equal(t, "routine", plan[0].Next.ModelTier.Name)
	assert.Equal(t, 1, matrix.calls)
}

func TestStoreFillsTheMatrixRequestWithTheProjectsThatOwnTheWritePaths(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{}
	var asked [][]string
	loc := tempLocation(t, t.TempDir())
	loc.ModelTiers, loc.ModelTierMatrix = tierSet(t), matrix.choose
	loc.Owners = func(_ context.Context, files []string) ([]string, error) {
		asked = append(asked, files)
		return []string{"pkg/a"}, nil
	}
	s := NewStore(loc)

	row := lease("a")
	row.Agent.Model = job.Model{}
	row.Paths.Write = []string{"pkg/a/run.go#func A", "pkg/a/*.go"}
	seed(t, s, row)

	assert.Equal(t, [][]string{{"pkg/a/run.go", "pkg/a/*.go"}}, asked, "a claim is asked about as its file")
	assert.Equal(t, []string{"pkg/a"}, matrix.seen.WriteProjects)

	named := lease("b")
	named.Agent.Model = job.Model{Tier: "design"}
	seed(t, s, named)
	assert.Len(t, asked, 1, "a row that names its tier never reaches the matrix, so no one is asked")
}

func TestStoreAsksNoOneForOwnersWithoutAMatrix(t *testing.T) {
	t.Parallel()

	loc := tempLocation(t, t.TempDir())
	loc.ModelTiers = tierSet(t)
	loc.Owners = func(context.Context, []string) ([]string, error) {
		t.Error("Owners was asked with no matrix registered")
		return nil, nil
	}
	row := lease("a")
	row.Agent.Model = job.Model{}
	seed(t, NewStore(loc), row)
}

func TestStoreFailsAForkWhoseOwnersCannotBeFound(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{}
	loc := tempLocation(t, t.TempDir())
	loc.ModelTiers, loc.ModelTierMatrix = tierSet(t), matrix.choose
	loc.Owners = func(context.Context, []string) ([]string, error) { return nil, errors.New("graph does not build") }
	s := NewStore(loc)
	row := lease("a")
	row.Agent.Model = job.Model{}

	_, err := s.Update(t.Context(), "a", func(cur *job.Job) { *cur = row })

	require.Error(t, err)
	assert.ErrorContains(t, err, "graph does not build")
	assert.Zero(t, matrix.calls, "the matrix is not fed a request with the projects missing")
}

func TestStoreRefusesAChildAboveItsParentsTierUnderDeny(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	s.spawnCeiling = ceilingAt(denied)
	parent := lease("p")
	parent.Agent.Model = job.Model{Tier: "routine"}
	seed(t, s, parent)

	child := lease("c")
	child.Parent, child.Agent.Model = "p", job.Model{Tier: "design"}
	_, err := s.Update(t.Context(), "c", func(cur *job.Job) { *cur = child })
	require.Error(t, err)
	assert.ErrorContains(t, err, "c would run at model tier design (rank 2)")
	assert.ErrorContains(t, err, "its parent p was forked at routine (rank 0)")
	assert.ErrorContains(t, err, "this workspace sets spawn-above-parent-tier to deny, so fork it at routine or lower (`--model routine`)")
	verdict := types.ClassifyRetry(err)
	assert.False(t, verdict.Retryable)
	assert.Equal(t, job.RetryAfterPerson, verdict.Change)

	child.Agent.Model = job.Model{}
	_, err = s.Update(t.Context(), "c", func(cur *job.Job) { *cur = child })
	require.Error(t, err, "the declared default is held to the parent's tier too")
	assert.ErrorContains(t, err, "c would run at model tier bounded (rank 1)")

	rows, err := s.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1, "a refused fork writes nothing")
}

func TestStoreForksAChildAtOrBelowItsParentsTier(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	parent := lease("p")
	parent.Agent.Model = job.Model{Tier: "bounded"}
	seed(t, s, parent)

	equal := lease("p/equal")
	equal.Parent, equal.Agent.Model = "p", job.Model{Tier: "bounded"}
	assert.Equal(t, "bounded", seed(t, s, equal).ModelTier.Name)

	lower := lease("p/lower")
	lower.Parent, lower.Agent.Model = "p", job.Model{ID: "haiku"}
	assert.Equal(t, "routine", seed(t, s, lower).ModelTier.Name, "a host's model resolves to its tier and is held by rank")

	orphan := lease("o")
	orphan.Agent.Model = job.Model{Tier: "design"}
	assert.Equal(t, "design", seed(t, s, orphan).ModelTier.Name, "a row with no parent has no ceiling")
}

func TestStoreRefusesReparentingUnderALowerTierUnderDeny(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	s.spawnCeiling = ceilingAt(denied)
	low := lease("low")
	low.Agent.Model = job.Model{Tier: "routine"}
	seed(t, s, low)
	row := lease("a")
	row.Agent.Model = job.Model{Tier: "bounded"}
	seed(t, s, row)

	_, err := s.Update(t.Context(), "a", func(cur *job.Job) { cur.Parent = "low" })
	require.Error(t, err)
	assert.ErrorContains(t, err, "its parent low was forked at routine (rank 0)")
}

// refuseAboveParentSuite judges a design child c under a parent p.
type refuseAboveParentSuite struct {
	suite.Suite
	set    *tier.Set
	child  job.Job
	proven []job.Job
}

func TestRefuseAboveParent(t *testing.T) { suite.Run(t, new(refuseAboveParentSuite)) }

func (s *refuseAboveParentSuite) SetupTest() {
	s.set = tierSet(s.T())
	s.child = job.Job{ID: "c", Parent: "p", ModelTier: &tier.Assignment{Name: "design", Rank: 2}}
	s.proven = []job.Job{{ID: "p", ModelTier: &tier.Assignment{Name: "routine", Rank: 0}}}
}

func (s *refuseAboveParentSuite) refuse(plan []job.Job, ceiling builtin.Setting) error {
	return refuseAboveParent(s.T().Context(), s.set, tierWithin, ceilingAt(ceiling), s.child, plan)
}

func (s *refuseAboveParentSuite) TestAParentMissingFromThePlanBoundsNothing() {
	s.NoError(s.refuse(nil, denied))
}

func (s *refuseAboveParentSuite) TestAParentRecordingNoTierBoundsNothing() {
	s.NoError(s.refuse([]job.Job{{ID: "p"}}, denied))
}

func (s *refuseAboveParentSuite) TestAParentsTierAtAnotherRankBoundsNothing() {
	s.NoError(s.refuse([]job.Job{{ID: "p", ModelTier: &tier.Assignment{Name: "routine", Rank: 1}}}, denied))
}

func (s *refuseAboveParentSuite) TestAParentsTierNoLongerDeclaredBoundsNothing() {
	s.NoError(s.refuse([]job.Job{{ID: "p", ModelTier: &tier.Assignment{Name: "tiny", Rank: 0}}}, denied))
}

func (s *refuseAboveParentSuite) TestAProvenPairUnderDenyIsRefused() {
	s.ErrorContains(s.refuse(s.proven, denied), "this workspace sets spawn-above-parent-tier to deny")
}

func (s *refuseAboveParentSuite) TestAProvenPairUnderAdviseIsAllowed() {
	s.NoError(s.refuse(s.proven, builtin.Setting{Decision: builtin.Advise}))
}

func (s *refuseAboveParentSuite) TestAProvenPairUnderOffIsAllowed() {
	s.NoError(s.refuse(s.proven, builtin.Setting{Decision: builtin.Off}))
}

func (s *refuseAboveParentSuite) TestAWorkspaceWithoutTiersBoundsNothing() {
	s.NoError(refuseAboveParent(s.T().Context(), nil, tierWithin, ceilingAt(denied), s.child, s.proven))
}

// A proven pair with no rule is refused, never allowed.
func (s *refuseAboveParentSuite) TestAProvenPairWithNoRuleIsRefused() {
	s.ErrorContains(refuseAboveParent(s.T().Context(), s.set, nil, ceilingAt(denied), s.child, s.proven), "no spawn rule")
}

func (s *refuseAboveParentSuite) TestAProvenPairWithNoSettingReaderIsRefused() {
	err := refuseAboveParent(s.T().Context(), s.set, tierWithin, nil, s.child, s.proven)
	s.ErrorContains(err, "opened with no spawn-above-parent-tier setting to judge it")
}

func (s *refuseAboveParentSuite) TestASettingThatFailsToReadIsRefused() {
	failing := func(context.Context) (builtin.Setting, error) {
		return builtin.Setting{}, errors.New("magus.yaml does not load")
	}
	err := refuseAboveParent(s.T().Context(), s.set, tierWithin, failing, s.child, s.proven)
	s.ErrorContains(err, "c ranks above its parent p: magus.yaml does not load")
}

// forkAboveParentSuite forks c on opus under p on haiku in a tiered store.
type forkAboveParentSuite struct {
	suite.Suite
	store *Store
	child job.Job
}

func TestForkAboveParent(t *testing.T) { suite.Run(t, new(forkAboveParentSuite)) }

func (s *forkAboveParentSuite) SetupTest() {
	s.store = tieredStore(s.T(), tierSet(s.T()), nil)
	s.store.modelRows = func() []model.Row {
		return []model.Row{
			{ID: "claude-haiku-5-5", Alias: "haiku", Price: budget.Price{Input: 1, Output: 5}},
			{ID: "claude-opus-5-5", Alias: "opus", Price: budget.Price{Input: 5, Output: 25}},
		}
	}
	parent := lease("p")
	parent.Agent.Model = job.Model{ID: "haiku"}
	seed(s.T(), s.store, parent)
	s.child = lease("c")
	s.child.Parent, s.child.Agent.Model = "p", job.Model{ID: "opus"}
}

// notes forks the child and returns the notes the fork owes its reader.
func (s *forkAboveParentSuite) notes() []string {
	stored := seed(s.T(), s.store, s.child)
	rows, err := s.store.List(s.T().Context())
	s.Require().NoError(err)
	return ForkNotes(s.T().Context(), s.store, rows, stored)
}

func (s *forkAboveParentSuite) TestAForkAboveItsParentIsAllowedByDefault() {
	stored := seed(s.T(), s.store, s.child)

	s.Equal("design", stored.ModelTier.Name)
}

func (s *forkAboveParentSuite) TestForkNotesAdviseAForkAboveItsParentWithItsCost() {
	s.Contains(s.notes(), "c runs at model tier design (rank 2), above its parent p at routine (rank 0);"+
		" per million tokens, opus costs $5.00 input and $25.00 output, and haiku costs $1.00 input and $5.00 output."+
		" `--model routine` keeps it at the parent's tier, and setting spawn-above-parent-tier off in magus\\guard.builtins stops this advice")
}

func (s *forkAboveParentSuite) TestForkNotesSayNothingOfAForkAtItsParentsTier() {
	s.child.Agent.Model = job.Model{ID: "haiku"}

	for _, note := range s.notes() {
		s.NotContains(note, "above its parent")
	}
}

func (s *forkAboveParentSuite) TestOffLetsAForkThroughAndSilencesForkNotes() {
	s.store.spawnCeiling = ceilingAt(builtin.Setting{Decision: builtin.Off})

	notes := s.notes()

	for _, note := range notes {
		s.NotContains(note, "above its parent")
	}
}

func (s *forkAboveParentSuite) TestForkNotesSayASettingThatFailsToRead() {
	stored := seed(s.T(), s.store, s.child)
	s.store.spawnCeiling = func(context.Context) (builtin.Setting, error) {
		return builtin.Setting{}, errors.New("magus.yaml does not load")
	}
	rows, err := s.store.List(s.T().Context())
	s.Require().NoError(err)

	s.Contains(ForkNotes(s.T().Context(), s.store, rows, stored),
		"c ranks above its parent p, and the spawn-above-parent-tier setting did not read: magus.yaml does not load")
}

// fourModelTiersSuite pins the migration that respells the three shipped
// tiers as the four that replaced them.
type fourModelTiersSuite struct {
	suite.Suite
}

func TestFourModelTiersMigration(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(fourModelTiersSuite))
}

// migrated is the row after the job store's migrations ran over a temp
// file already through the tries move.
func (s *fourModelTiersSuite) migrated(line string) (job.Job, map[string]any) {
	st := tempStore(s.T(), s.T().TempDir())
	st.notices = &bytes.Buffer{}
	path := plantRaw(s.T(), st, fmt.Sprintf("%s\n%s", `{"api_version":"safetyscissors.job/v1alpha1","migration":3}`, line))
	f, err := st.Read(s.T().Context())
	s.Require().NoError(err)
	s.Require().Len(f.Jobs, 1)
	return f.Jobs[0], storedSpec(s.T(), path, f.Jobs[0].ID)
}

func (s *fourModelTiersSuite) TestEachOldRequestedTierTakesItsNewName() {
	for old, renamed := range map[string]string{"economy": "routine", "standard": "bounded", "principal": "design"} {
		row, stored := s.migrated(fmt.Sprintf(`{"id":"a","state":"running","created":1,"updated":1,`+
			`"agent":{"model":{"tier":%q},"worker":{"type":"resumed"}}}`, old))
		s.Equal(job.Model{Tier: renamed}, row.Agent.Model, old)
		s.Equal(map[string]any{"tier": renamed}, stored["agent"].(map[string]any)["model"], "the file holds %s", renamed)
	}
}

func (s *fourModelTiersSuite) TestTheAssignedTierTakesItsNewNameAndKeepsTheRest() {
	row, _ := s.migrated(`{"id":"a","state":"running","created":1,"updated":1,` +
		`"model_tier":{"name":"economy","rank":0,"skills":"full","source":"matrix","reason":"1 file","site":"magusfile.buzz:9"}}`)
	s.Equal(&tier.Assignment{Name: "routine", Rank: 0, Skills: tier.SkillVariantFull, Source: tier.SourceMatrix,
		Reason: "1 file", Site: "magusfile.buzz:9"}, row.ModelTier)
}

func (s *fourModelTiersSuite) TestAModelIDKeepsItsSpelling() {
	row, _ := s.migrated(`{"id":"a","state":"running","created":1,"updated":1,` +
		`"agent":{"model":{"id":"principal"},"worker":{"type":"resumed"}}}`)
	s.Equal(job.Model{ID: "principal"}, row.Agent.Model, "an id is a host's word, not a tier")
}

func (s *fourModelTiersSuite) TestATierTheOldSetNeverNamedIsUntouched() {
	row, _ := s.migrated(`{"id":"a","state":"running","created":1,"updated":1,` +
		`"agent":{"model":{"tier":"frontier"},"worker":{"type":"resumed"}},` +
		`"model_tier":{"name":"high","rank":1,"skills":"short","source":"row"}}`)
	s.Equal(job.Model{Tier: "frontier"}, row.Agent.Model)
	s.Equal("high", row.ModelTier.Name)
}

func (s *fourModelTiersSuite) TestARowWithNoTierIsUntouched() {
	row, stored := s.migrated(`{"id":"a","state":"running","created":1,"updated":1}`)
	s.Nil(row.Agent)
	s.Nil(row.ModelTier)
	s.NotContains(stored, "agent")
}

// requestOf is newModelTierRequest, failing t when the row's checks do not read.
func requestOf(t testing.TB, row job.Job, rows []job.Job, set *tier.Set, models []model.Row, history []types.ModelTierRecord) types.ModelTierRequest {
	t.Helper()
	req, err := newModelTierRequest(row, rows, set, models, history)
	require.NoError(t, err)
	return req
}
