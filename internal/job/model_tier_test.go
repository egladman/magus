package job

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tierSet(t *testing.T, bounds ...types.ModelTierBounds) *types.ModelTierSet {
	t.Helper()
	if bounds == nil {
		bounds = []types.ModelTierBounds{{MaxLive: 2}, {}, {}}
	}
	set, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum:    "ModelTier",
		Site:    "magusfile.buzz:3",
		Cases:   []string{"economy", "standard", "principal"},
		Default: 1,
		Bounds:  bounds,
		Skills:  []types.SkillVariant{types.SkillVariantFull, types.SkillVariantShort, types.SkillVariantShort},
	})
	require.NoError(t, err)
	return set
}

type countingMatrix struct {
	calls  int
	seen   types.ModelTierRequest
	answer *types.JobModelTier
	err    error
}

func (m *countingMatrix) choose(_ context.Context, req types.ModelTierRequest) (*types.JobModelTier, error) {
	m.calls++
	m.seen = req
	return m.answer, m.err
}

func economyAnswer() *types.JobModelTier {
	return &types.JobModelTier{Name: "economy", Rank: 0, Skills: types.SkillVariantFull, Reason: "small checked job", Site: "magusfile.buzz:20"}
}

func TestNewModelTierRequestDerivesKindFromTheRow(t *testing.T) {
	t.Parallel()

	target := &types.LeaseCheck{Target: "test", Project: "."}
	script := &types.LeaseCheck{Script: "probe.buzz"}
	symbol := types.Goal{ID: "moved", Kind: types.GoalKindSymbol, Symbols: []string{"A"}}
	paths := types.Goal{ID: "touched", Kind: types.GoalKindPaths, Paths: []string{"a.go"}}
	tests := []struct {
		name string
		row  types.Job
		want types.JobKind
	}{
		{"a script check outranks everything", types.Job{Check: script, Goals: []types.Goal{symbol, paths}}, types.JobKindScript},
		{"a script check inside a goal counts", types.Job{Goals: []types.Goal{{ID: "g", Kind: types.GoalKindCheck, Check: *script}, symbol}}, types.JobKindScript},
		{"symbol goals outrank paths and a target check", types.Job{Check: target, Goals: []types.Goal{paths, symbol}}, types.JobKindSymbol},
		{"path goals outrank a target check", types.Job{Check: target, Goals: []types.Goal{paths}}, types.JobKindPaths},
		{"a target check alone", types.Job{Check: target}, types.JobKindCheck},
		{"nothing graded is prose", types.Job{Criteria: "tidy the docs"}, types.JobKindProse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, NewModelTierRequest(tt.row, nil, nil, nil).Kind)
		})
	}
}

func TestNewModelTierRequestCountsFilesOnce(t *testing.T) {
	t.Parallel()

	row := types.Job{WritePaths: []string{"internal/job/*.go", "run.go#func A", "run.go#func B", "docs", "docs"}}
	assert.Equal(t, 3, NewModelTierRequest(row, nil, nil, nil).WriteFiles,
		"a glob is one, and two claims in one file are that file once")
}

func TestNewModelTierRequestCarriesTheRowAndItsParentsRank(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	parent := types.Job{ID: "wave", ModelTier: &types.JobModelTier{Name: "principal", Rank: 2}}
	row := types.Job{
		ID: "wave/a", Parent: "wave", Criteria: "do it", ReadOnly: false, Model: "economy",
		WritePaths: []string{"a.go"}, ReadPaths: []string{"b"}, DependsOn: []string{"wave/b"},
		Check: &types.LeaseCheck{Target: "test", Project: ".", Args: []string{"-run", "X"}},
		Goals: []types.Goal{{ID: "g", Kind: types.GoalKindPaths, Paths: []string{"a.go"}}},
	}
	history := []types.ModelTierRecord{{Rank: 0, ModelTier: "economy", Jobs: 12}}

	req := NewModelTierRequest(row, []types.Job{parent, row}, set, history)

	require.NotNil(t, req.ParentRank)
	assert.Equal(t, 2, *req.ParentRank)
	require.NotNil(t, req.DeclaredRank)
	assert.Equal(t, 0, *req.DeclaredRank)
	assert.Equal(t, "economy", req.DeclaredModel)
	assert.Equal(t, history, req.History)
	assert.Equal(t, row.Goals, req.Goals)
	assert.Equal(t, []string{"a.go"}, req.WritePaths)
	assert.Equal(t, []string{"b"}, req.ReadPaths)
	assert.Equal(t, []string{"wave/b"}, req.DependsOn)
	assert.Empty(t, req.WriteProjects, "a project is a graph fact the store does not hold")

	req.Check.Args[0] = "changed"
	assert.Equal(t, "-run", row.Check.Args[0], "the request owns its copy of the check")
}

func TestNewModelTierRequestLeavesRanksNilWhenNoneIsKnown(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	orphan := types.Job{ID: "a", Parent: "gone", Model: "haiku"}
	req := NewModelTierRequest(orphan, nil, set, nil)
	assert.Nil(t, req.ParentRank, "a parent that is not in the plan has no rank")
	assert.Nil(t, req.DeclaredRank, "a model that names no tier declares no rank")

	untiered := types.Job{ID: "wave"}
	req = NewModelTierRequest(types.Job{ID: "wave/a", Parent: "wave"}, []types.Job{untiered}, set, nil)
	assert.Nil(t, req.ParentRank, "a parent without a tier has no rank")
}

func TestAssignModelTierPrecedence(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	tests := []struct {
		name        string
		row         types.Job
		matrix      countingMatrix
		want        types.JobModelTier
		matrixCalls int
	}{
		{
			name:        "a row naming a tier wins and the matrix never runs",
			row:         types.Job{ID: "a", Model: "principal"},
			matrix:      countingMatrix{answer: economyAnswer()},
			want:        types.JobModelTier{Name: "principal", Rank: 2, Skills: types.SkillVariantShort, Source: types.ModelTierSourceRow, Reason: "the job names the tier principal"},
			matrixCalls: 0,
		},
		{
			name:   "a model naming no tier is kept with the default's rank",
			row:    types.Job{ID: "a", Model: "haiku"},
			matrix: countingMatrix{answer: economyAnswer()},
			want: types.JobModelTier{
				Name: "standard", Rank: 1, Skills: types.SkillVariantShort, Source: types.ModelTierSourceRow,
				Reason: "the job names the model haiku, which no model tier maps to", Model: "haiku",
			},
			matrixCalls: 0,
		},
		{
			name:        "the matrix answers when the row names nothing",
			row:         types.Job{ID: "a"},
			matrix:      countingMatrix{answer: economyAnswer()},
			want:        types.JobModelTier{Name: "economy", Rank: 0, Skills: types.SkillVariantFull, Source: types.ModelTierSourceMatrix, Reason: "small checked job", Site: "magusfile.buzz:20"},
			matrixCalls: 1,
		},
		{
			name:        "a matrix that declines leaves the default",
			row:         types.Job{ID: "a"},
			matrix:      countingMatrix{},
			want:        types.JobModelTier{Name: "standard", Rank: 1, Skills: types.SkillVariantShort, Source: types.ModelTierSourceDefault, Reason: "the declared default"},
			matrixCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := NewModelTierRequest(tt.row, nil, set, nil)
			got, err := AssignModelTier(t.Context(), set, tt.matrix.choose, req, tt.row)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.matrixCalls, tt.matrix.calls)
		})
	}
}

func TestAssignModelTierWithoutAMatrixTakesTheDefault(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	row := types.Job{ID: "a"}
	got, err := AssignModelTier(t.Context(), set, nil, NewModelTierRequest(row, nil, set, nil), row)
	require.NoError(t, err)
	assert.Equal(t, types.ModelTierSourceDefault, got.Source)
	assert.Equal(t, "standard", got.Name)
}

func TestAssignModelTierHandsTheMatrixItsRequest(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	row := types.Job{ID: "a", WritePaths: []string{"a.go"}, Check: &types.LeaseCheck{Target: "test", Project: "."}}
	matrix := countingMatrix{}
	req := NewModelTierRequest(row, nil, set, nil)
	_, err := AssignModelTier(t.Context(), set, matrix.choose, req, row)
	require.NoError(t, err)
	assert.Equal(t, req, matrix.seen)
}

func TestAssignModelTierRefusesAMatrixThatIsWrong(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	row := types.Job{ID: "a"}
	tests := []struct {
		name   string
		matrix countingMatrix
		want   string
	}{
		{"it raises", countingMatrix{err: errors.New("magus\\job.modelTier: the matrix raised: boom")}, "boom"},
		{"a tier the declaration lacks", countingMatrix{answer: &types.JobModelTier{Name: "huge", Skills: types.SkillVariantFull, Reason: "r"}}, `"huge" is not a model tier`},
		{"a rank the declaration disagrees with", countingMatrix{answer: &types.JobModelTier{Name: "economy", Rank: 2, Skills: types.SkillVariantFull, Reason: "r"}}, "ranks it 0"},
		{"skills that are no variant", countingMatrix{answer: &types.JobModelTier{Name: "economy", Skills: "tiny", Reason: "r"}}, "the variants are"},
		{"no skills", countingMatrix{answer: &types.JobModelTier{Name: "economy", Reason: "r"}}, "the variants are"},
		{"no reason", countingMatrix{answer: &types.JobModelTier{Name: "economy", Skills: types.SkillVariantFull}}, "gave no reason"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := AssignModelTier(t.Context(), set, tt.matrix.choose, NewModelTierRequest(row, nil, set, nil), row)
			require.Error(t, err, "a wrong matrix fails the assignment, it never falls through to the default")
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestAssignModelTierWithoutDeclarationFails(t *testing.T) {
	t.Parallel()

	_, err := AssignModelTier(t.Context(), nil, nil, types.ModelTierRequest{}, types.Job{ID: "a"})
	require.Error(t, err)
}

func TestRefuseModelTierLimits(t *testing.T) {
	t.Parallel()

	set := tierSet(t, types.ModelTierBounds{MaxLive: 2}, types.ModelTierBounds{}, types.ModelTierBounds{MaxLive: 1})
	at := func(id, tier string, state types.JobState) types.Job {
		return types.Job{ID: id, State: state, ModelTier: &types.JobModelTier{Name: tier}}
	}
	economy := types.JobModelTier{Name: "economy", Rank: 0}
	tests := []struct {
		name     string
		rows     []types.Job
		assigned types.JobModelTier
		wantErr  string
	}{
		{"under the bound", []types.Job{at("a", "economy", types.StateRunning)}, economy, ""},
		{"at the bound", []types.Job{at("a", "economy", types.StateRunning), at("b", "economy", types.StateDeclared)}, economy, "3 live jobs at model tier economy"},
		{"ended jobs do not count", []types.Job{at("a", "economy", types.StatePass), at("b", "economy", types.StateFail), at("c", "economy", types.StateRunning)}, economy, ""},
		{"other tiers do not count", []types.Job{at("a", "principal", types.StateRunning), at("b", "principal", types.StateRunning)}, economy, ""},
		{"a row without a tier does not count", []types.Job{{ID: "a", State: types.StateRunning}, {ID: "b", State: types.StateRunning}}, economy, ""},
		{"no bound is unlimited", []types.Job{at("a", "standard", types.StateRunning), at("b", "standard", types.StateRunning), at("c", "standard", types.StateRunning)}, types.JobModelTier{Name: "standard", Rank: 1}, ""},
		{"a bound of one", []types.Job{at("a", "principal", types.StateExited)}, types.JobModelTier{Name: "principal", Rank: 2}, "allows 1"},
		{"a tier the declaration lacks", nil, types.JobModelTier{Name: "huge"}, `"huge" is not a model tier`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := RefuseModelTierLimits(tt.rows, tt.assigned, set)
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

	set := tierSet(t, types.ModelTierBounds{MaxLive: 1}, types.ModelTierBounds{}, types.ModelTierBounds{})
	rows := []types.Job{{ID: "a", State: types.StateRunning, ModelTier: &types.JobModelTier{Name: "economy"}}}
	err := RefuseModelTierLimits(rows, types.JobModelTier{Name: "economy"}, set)
	require.Error(t, err)
	assert.ErrorContains(t, err, `MaxLive bound in magus\job.modelTiers allows 1`)
	assert.ErrorContains(t, err, "job exit")
}

func tieredStore(t *testing.T, set *types.ModelTierSet, matrix func(context.Context, types.ModelTierRequest) (*types.JobModelTier, error)) *Store {
	t.Helper()
	loc := tmpLoc(t, t.TempDir())
	loc.ModelTiers, loc.ModelTierMatrix = set, matrix
	return NewStore(loc)
}

func TestStoreAssignsTheTierWhenARowIsCreated(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: economyAnswer()}
	s := tieredStore(t, tierSet(t), matrix.choose)
	row := lease("a")
	row.Model = ""

	stored := seed(t, s, row)

	require.NotNil(t, stored.ModelTier)
	assert.Equal(t, types.JobModelTier{
		Name: "economy", Rank: 0, Skills: types.SkillVariantFull, Source: types.ModelTierSourceMatrix,
		Reason: "small checked job", Site: "magusfile.buzz:20",
	}, *stored.ModelTier)
	assert.Equal(t, 1, matrix.calls)
	assert.Equal(t, "a", matrix.seen.ID)

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, stored.ModelTier, rows[0].ModelTier, "the tier is stored, not recomputed on read")
}

func TestStoreRecordsTheDefaultForAWorkspaceThatDeclaresNoMatrix(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	row := lease("a")
	row.Model = ""

	stored := seed(t, s, row)

	require.NotNil(t, stored.ModelTier)
	assert.Equal(t, types.ModelTierSourceDefault, stored.ModelTier.Source)
	assert.Equal(t, "standard", stored.ModelTier.Name)
}

func TestStoreReassignsOnlyWhenTheModelChanges(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: economyAnswer()}
	s := tieredStore(t, tierSet(t), matrix.choose)
	row := lease("a")
	row.Model = ""
	seed(t, s, row)
	require.Equal(t, 1, matrix.calls)

	row.Criteria = "reworded"
	same := seed(t, s, row)
	assert.Equal(t, 1, matrix.calls, "an edit that leaves the model alone does not run the matrix again")
	assert.Equal(t, "economy", same.ModelTier.Name)

	row.Model = "principal"
	changed := seed(t, s, row)
	assert.Equal(t, 1, matrix.calls, "a row that names a tier never reaches the matrix")
	assert.Equal(t, "principal", changed.ModelTier.Name)
	assert.Equal(t, types.ModelTierSourceRow, changed.ModelTier.Source)
}

func TestStoreKeepsTheStoredTierAgainstAWholeRowWrite(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	row := lease("a")
	row.Model = ""
	first := seed(t, s, row)

	row.ModelTier = &types.JobModelTier{Name: "principal", Rank: 2, Source: types.ModelTierSourceRow, Reason: "forged"}
	again := seed(t, s, row)
	assert.Equal(t, first.ModelTier, again.ModelTier, "a caller's tier is replaced by the stored one")

	forged := lease("b")
	forged.Model = ""
	forged.ModelTier = &types.JobModelTier{Name: "principal", Rank: 2, Source: types.ModelTierSourceRow, Reason: "forged"}
	created := seed(t, s, forged)
	assert.Equal(t, "standard", created.ModelTier.Name, "a created row is assigned, whatever it sent")
}

func TestStoreAssignsNothingWithoutTiers(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	row := lease("a")
	row.Model = ""
	assert.Nil(t, seed(t, s, row).ModelTier)
}

func TestStoreRefusesAForkPastAMaxLiveBound(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: economyAnswer()}
	s := tieredStore(t, tierSet(t, types.ModelTierBounds{MaxLive: 1}, types.ModelTierBounds{}, types.ModelTierBounds{}), matrix.choose)
	first := lease("a")
	first.Model = ""
	seed(t, s, first)

	second := lease("b")
	second.Model = ""
	_, err := s.Update(t.Context(), "b", func(cur *types.Job) { *cur = second })
	require.Error(t, err)
	assert.ErrorContains(t, err, "MaxLive")

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1, "a refused fork writes nothing")

	_, err = s.Update(t.Context(), "a", func(cur *types.Job) { cur.Criteria = "edited" })
	require.NoError(t, err, "the job already at the bound is not counted against itself")
}

func TestStoreFailsAForkWhoseMatrixFails(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{err: errors.New("magus\\job.modelTier: the matrix raised: boom")}
	s := tieredStore(t, tierSet(t), matrix.choose)
	row := lease("a")
	row.Model = ""
	_, err := s.Update(t.Context(), "a", func(cur *types.Job) { *cur = row })
	require.Error(t, err)
	assert.ErrorContains(t, err, "magus\\job.modelTier")

	rows, listErr := s.List()
	require.NoError(t, listErr)
	assert.Empty(t, rows)
}

func TestApplyDryRunAssignsNothingAndRunsNoMatrix(t *testing.T) {
	t.Parallel()

	matrix := countingMatrix{answer: economyAnswer()}
	s := tieredStore(t, tierSet(t), matrix.choose)
	record := types.Declaration{ID: "wave/job", WritePaths: []string{"a.go"}, Check: forkCheck()}

	plan, err := Apply(t.Context(), s, []types.Declaration{record}, config.Jobs{}, nil, nil, true)
	require.NoError(t, err)
	require.Len(t, plan, 1)
	assert.Nil(t, plan[0].Next.ModelTier)
	assert.Zero(t, matrix.calls)
	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows)

	plan, err = Apply(t.Context(), s, []types.Declaration{record}, config.Jobs{}, nil, nil, false)
	require.NoError(t, err)
	require.NotNil(t, plan[0].Next.ModelTier)
	assert.Equal(t, "economy", plan[0].Next.ModelTier.Name)
	assert.Equal(t, 1, matrix.calls)
}
