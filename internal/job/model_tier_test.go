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
			assert.Equal(t, tt.want, newModelTierRequest(tt.row, nil, nil, nil, nil).Kind)
		})
	}
}

func TestNewModelTierRequestCountsFilesOnce(t *testing.T) {
	t.Parallel()

	row := types.Job{WritePaths: []string{"internal/job/*.go", "run.go#func A", "run.go#func B", "docs", "docs"}}
	assert.Equal(t, 3, newModelTierRequest(row, nil, nil, nil, nil).WriteFiles,
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

	req := newModelTierRequest(row, []types.Job{parent, row}, set, nil, history)

	require.NotNil(t, req.ParentRank)
	assert.Equal(t, 2, *req.ParentRank)
	require.NotNil(t, req.DeclaredRank)
	assert.Equal(t, 0, *req.DeclaredRank)
	wantReq := req
	wantReq.DeclaredModel, wantReq.History, wantReq.Goals = "economy", history, row.Goals
	wantReq.WritePaths, wantReq.ReadPaths, wantReq.DependsOn = []string{"a.go"}, []string{"b"}, []string{"wave/b"}
	assert.Equal(t, wantReq, req)
	assert.Empty(t, req.WriteProjects, "a project is a graph fact the store does not hold")

	req.Check.Args[0] = "changed"
	assert.Equal(t, "-run", row.Check.Args[0], "the request owns its copy of the check")
}

func TestNewModelTierRequestLeavesRanksNilWhenNoneIsKnown(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	orphan := types.Job{ID: "a", Parent: "gone", Model: "gpt"}
	req := newModelTierRequest(orphan, nil, set, nil, nil)
	assert.Nil(t, req.ParentRank, "a parent that is not in the plan has no rank")
	assert.Nil(t, req.DeclaredRank, "a model that names no tier declares no rank")

	viaHost := newModelTierRequest(types.Job{ID: "b", Model: "opus"}, nil, set, nil, nil)
	require.NotNil(t, viaHost.DeclaredRank)
	assert.Equal(t, 2, *viaHost.DeclaredRank, "a host alias declares its tier's rank")

	untiered := types.Job{ID: "wave"}
	req = newModelTierRequest(types.Job{ID: "wave/a", Parent: "wave"}, []types.Job{untiered}, set, nil, nil)
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
			row:    types.Job{ID: "a", Model: "gpt"},
			matrix: countingMatrix{answer: economyAnswer()},
			want: types.JobModelTier{
				Name: "standard", Rank: 1, Skills: types.SkillVariantShort, Source: types.ModelTierSourceRow,
				Reason: "the job names the model gpt, which no model tier maps to", Model: "gpt",
			},
			matrixCalls: 0,
		},
		{
			name:   "a model a host maps to a tier takes that tier and keeps the model",
			row:    types.Job{ID: "a", Model: "opus"},
			matrix: countingMatrix{answer: economyAnswer()},
			want: types.JobModelTier{
				Name: "principal", Rank: 2, Skills: types.SkillVariantShort, Source: types.ModelTierSourceRow,
				Reason: "the job names the model opus, which harness claude-code maps to the tier principal", Model: "opus",
			},
			matrixCalls: 0,
		},
		{
			name:   "an alias two hosts map to the same tier is not ambiguous",
			row:    types.Job{ID: "a", Model: "sonnet"},
			matrix: countingMatrix{},
			want: types.JobModelTier{
				Name: "standard", Rank: 1, Skills: types.SkillVariantShort, Source: types.ModelTierSourceRow,
				Reason: "the job names the model sonnet, which harness claude-code maps to the tier standard", Model: "sonnet",
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
			want:        types.JobModelTier{Name: "standard", Rank: 1, Skills: types.SkillVariantShort, Source: types.ModelTierSourceDefault, Reason: "the declared default", Site: "magusfile.buzz:3"},
			matrixCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := newModelTierRequest(tt.row, nil, set, nil, nil)
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
	row := types.Job{ID: "a"}
	got, err := resolveModelTier(t.Context(), set, nil, nil, newModelTierRequest(row, nil, set, nil, nil), row)
	require.NoError(t, err)
	wantTier := got
	wantTier.Source, wantTier.Name = types.ModelTierSourceDefault, "standard"
	wantTier.Site = "magusfile.buzz:3" // the default record names the declaration
	assert.Equal(t, wantTier, got)
}

func TestResolveModelTierReadsHarnessModelRowAliases(t *testing.T) {
	t.Parallel()

	set, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum: "ModelTier", Site: "magusfile.buzz:3", Cases: []string{"economy", "standard", "principal"}, Default: 1,
		Bounds: []types.ModelTierBounds{{}, {}, {}},
		Skills: []types.SkillVariant{types.SkillVariantFull, types.SkillVariantShort, types.SkillVariantShort},
	})
	require.NoError(t, err)
	models := []types.ModelPricing{
		{ID: "claude-haiku-9", Alias: "haiku", Tier: "economy"},
		{ID: "claude-sonnet-9", Alias: "sonnet", Tier: "standard"},
		{ID: "claude-opus-9", Alias: "opus", Tier: "principal"},
		{ID: "claude-untiered-9", Alias: "loose"},
	}
	tests := []struct {
		model    string
		wantTier string
		wantRank int
		wantRow  bool
	}{
		{"haiku", "economy", 0, true},
		{"sonnet", "standard", 1, true},
		{"opus", "principal", 2, true},
		{"claude-opus-9", "principal", 2, true},
		{"loose", "standard", 1, false},
		{"unlisted", "standard", 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			t.Parallel()

			row := types.Job{ID: "a", Model: tt.model}
			req := newModelTierRequest(row, nil, set, models, nil)
			got, err := resolveModelTier(t.Context(), set, models, nil, req, row)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTier, got.Name)
			assert.Equal(t, tt.wantRank, got.Rank)
			assert.Equal(t, tt.model, got.Model)
			assert.Equal(t, tt.wantRow, req.DeclaredRank != nil)
			if tt.wantRow {
				assert.NotContains(t, got.Reason, "no model tier maps")
			} else {
				assert.Contains(t, got.Reason, "no model tier maps")
			}
		})
	}
}

func TestAssignModelTierRefusesAnAliasThatNamesTwoTiers(t *testing.T) {
	t.Parallel()

	set, err := types.NewModelTierSet(types.ModelTierDeclaration{
		Enum: "ModelTier", Site: "magusfile.buzz:3", Cases: []string{"low", "high"}, Default: 0,
		Bounds: []types.ModelTierBounds{{}, {}},
		Skills: []types.SkillVariant{types.SkillVariantFull, types.SkillVariantShort},
		Hosts: map[string][]string{
			"a": {"x", "y"},
			"b": {"y", "x"},
		},
	})
	require.NoError(t, err)
	row := types.Job{ID: "j", Model: "x"}
	_, err = resolveModelTier(t.Context(), set, nil, nil, newModelTierRequest(row, nil, set, nil, nil), row)
	require.Error(t, err)
	assert.ErrorContains(t, err, "name a tier instead")
	assert.ErrorContains(t, err, "harness a")
	assert.ErrorContains(t, err, "harness b")
}

func TestAssignModelTierHandsTheMatrixItsRequest(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	row := types.Job{ID: "a", WritePaths: []string{"a.go"}, Check: &types.LeaseCheck{Target: "test", Project: "."}}
	matrix := countingMatrix{}
	req := newModelTierRequest(row, nil, set, nil, nil)
	_, err := resolveModelTier(t.Context(), set, nil, matrix.choose, req, row)
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
			_, err := resolveModelTier(t.Context(), set, nil, tt.matrix.choose, newModelTierRequest(row, nil, set, nil, nil), row)
			require.Error(t, err, "a wrong matrix fails the assignment, it never falls through to the default")
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestAssignModelTierWithoutDeclarationFails(t *testing.T) {
	t.Parallel()

	_, err := resolveModelTier(t.Context(), nil, nil, nil, types.ModelTierRequest{}, types.Job{ID: "a"})
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

	set := tierSet(t, types.ModelTierBounds{MaxLive: 1}, types.ModelTierBounds{}, types.ModelTierBounds{})
	rows := []types.Job{{ID: "a", State: types.StateRunning, ModelTier: &types.JobModelTier{Name: "economy"}}}
	err := refuseModelTierLimits(rows, types.JobModelTier{Name: "economy"}, set)
	require.Error(t, err)
	assert.ErrorContains(t, err, `MaxLive bound declared at magusfile.buzz:3 allows 1`)
	assert.ErrorContains(t, err, "job exit")
}

func tieredStore(t *testing.T, set *types.ModelTierSet, matrix func(context.Context, types.ModelTierRequest) (*types.JobModelTier, error)) *Store {
	t.Helper()
	loc := tempLocation(t, t.TempDir())
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
	wantStored := *stored.ModelTier
	wantStored.Source, wantStored.Name = types.ModelTierSourceDefault, "standard"
	assert.Equal(t, wantStored, *stored.ModelTier)
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
	wantChanged := *changed.ModelTier
	wantChanged.Name, wantChanged.Source = "principal", types.ModelTierSourceRow
	assert.Equal(t, wantChanged, *changed.ModelTier)
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

	s := tempStore(t, t.TempDir())
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
	row.Model = ""
	row.WritePaths = []string{"pkg/a/run.go#func A", "pkg/a/*.go"}
	seed(t, s, row)

	assert.Equal(t, [][]string{{"pkg/a/run.go", "pkg/a/*.go"}}, asked, "a claim is asked about as its file")
	assert.Equal(t, []string{"pkg/a"}, matrix.seen.WriteProjects)

	named := lease("b")
	named.Model = "principal"
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
	row.Model = ""
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
	row.Model = ""

	_, err := s.Update(t.Context(), "a", func(cur *types.Job) { *cur = row })

	require.Error(t, err)
	assert.ErrorContains(t, err, "graph does not build")
	assert.Zero(t, matrix.calls, "the matrix is not fed a request with the projects missing")
}

func TestStoreRefusesAChildAboveItsParentsTier(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	parent := lease("p")
	parent.Model = "economy"
	seed(t, s, parent)

	child := lease("c")
	child.Parent, child.Model = "p", "principal"
	_, err := s.Update(t.Context(), "c", func(cur *types.Job) { *cur = child })
	require.Error(t, err)
	assert.ErrorContains(t, err, "c would run at model tier principal (rank 2)")
	assert.ErrorContains(t, err, "its parent p was forked at economy (rank 0)")
	assert.ErrorContains(t, err, "fork it at economy or lower (`--model economy`), or a person forks it at principal as a row of its own")
	verdict := types.ClassifyRetry(err)
	assert.False(t, verdict.Retryable)
	assert.Equal(t, types.RetryAfterPerson, verdict.Change)

	child.Model = ""
	_, err = s.Update(t.Context(), "c", func(cur *types.Job) { *cur = child })
	require.Error(t, err, "the declared default is held to the parent's tier too")
	assert.ErrorContains(t, err, "c would run at model tier standard (rank 1)")

	rows, err := s.List()
	require.NoError(t, err)
	require.Len(t, rows, 1, "a refused fork writes nothing")
}

func TestStoreForksAChildAtOrBelowItsParentsTier(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	parent := lease("p")
	parent.Model = "standard"
	seed(t, s, parent)

	equal := lease("p/equal")
	equal.Parent, equal.Model = "p", "standard"
	assert.Equal(t, "standard", seed(t, s, equal).ModelTier.Name)

	lower := lease("p/lower")
	lower.Parent, lower.Model = "p", "haiku"
	assert.Equal(t, "economy", seed(t, s, lower).ModelTier.Name, "a host's model resolves to its tier and is held by rank")

	orphan := lease("o")
	orphan.Model = "principal"
	assert.Equal(t, "principal", seed(t, s, orphan).ModelTier.Name, "a row with no parent has no ceiling")
}

func TestStoreRefusesReparentingUnderALowerTier(t *testing.T) {
	t.Parallel()

	s := tieredStore(t, tierSet(t), nil)
	low := lease("low")
	low.Model = "economy"
	seed(t, s, low)
	row := lease("a")
	row.Model = "standard"
	seed(t, s, row)

	_, err := s.Update(t.Context(), "a", func(cur *types.Job) { cur.Parent = "low" })
	require.Error(t, err)
	assert.ErrorContains(t, err, "its parent low was forked at economy (rank 0)")
}

func TestRefuseAboveParentJudgesOnlyAProvenPair(t *testing.T) {
	t.Parallel()

	set := tierSet(t)
	principal := &types.JobModelTier{Name: "principal", Rank: 2}
	tests := []struct {
		name string
		plan []types.Job
	}{
		{"parent missing from the plan", nil},
		{"parent records no tier", []types.Job{{ID: "p"}}},
		{"parent's tier is declared at another rank", []types.Job{{ID: "p", ModelTier: &types.JobModelTier{Name: "economy", Rank: 1}}}},
		{"parent's tier is no longer declared", []types.Job{{ID: "p", ModelTier: &types.JobModelTier{Name: "tiny", Rank: 0}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, refuseAboveParent(set, types.Job{ID: "c", Parent: "p", ModelTier: principal}, tt.plan))
		})
	}

	proven := []types.Job{{ID: "p", ModelTier: &types.JobModelTier{Name: "economy", Rank: 0}}}
	require.Error(t, refuseAboveParent(set, types.Job{ID: "c", Parent: "p", ModelTier: principal}, proven))
	assert.NoError(t, refuseAboveParent(nil, types.Job{ID: "c", Parent: "p", ModelTier: principal}, proven), "a workspace without tiers bounds nothing")
}
