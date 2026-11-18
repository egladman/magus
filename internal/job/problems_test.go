package job

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// A cap on attempts is declared like the rest of the spec, so applying a record that omits
// it clears it, and a negative cap is refused at the door.
func TestDeclarationCarriesMaxAttempts(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateDeclaration(job.Declaration{ID: "worker", MaxAttempts: 3}))
	assert.ErrorContains(t, ValidateDeclaration(job.Declaration{ID: "worker", MaxAttempts: -1}), "max_attempts is -1")

	var row job.Job
	job.Declaration{ID: "worker", MaxAttempts: 3}.Apply(&row)
	assert.Equal(t, 3, row.MaxAttempts)
	job.Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Zero(t, row.MaxAttempts)
}

// A weight outside (0,1] is refused, never clamped, and an explicit 0 is out of range rather
// than read as omitted.
func TestDeclarationRefusesAWeightOutOfRange(t *testing.T) {
	t.Parallel()

	for _, w := range []float64{0, -0.5, 1.5, math.NaN()} {
		assert.ErrorContains(t, ValidateDeclaration(job.Declaration{ID: "worker", Weight: &w}), "outside (0,1]", "weight %v", w)
	}
	for _, w := range []float64{0.1, 0.5, 1} {
		require.NoError(t, ValidateDeclaration(job.Declaration{ID: "worker", Weight: &w}), "weight %v", w)
	}

	var row job.Job
	w := 0.9
	job.Declaration{ID: "worker", Weight: &w}.Apply(&row)
	assert.InDelta(t, 0.9, row.Weight, 0)
	row.Paused = 42
	job.Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Zero(t, row.Weight, "an omitted weight clears the row's, which reads as DefaultJobWeight")
	assert.Equal(t, int64(42), row.Paused, "apply keeps a pause")
}

func TestDeclarationRejectsCyclicGoals(t *testing.T) {
	t.Parallel()

	err := ValidateDeclaration((job.Declaration{
		ID: "gated",
		Goals: []job.Goal{
			{ID: "unit", Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "."}), DependsOn: []string{"publish"}},
			{ID: "publish", Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "."}), DependsOn: []string{"unit"}},
		},
	}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dependencies contain a cycle")
}

// A path the guard can never match against a workspace-relative one used to be accepted
// and bound nothing, silently.
func TestDeclarationRefusesAPathOutsideTheWorkspace(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		decl job.Declaration
		want string
	}{
		{job.Declaration{ID: "j", Paths: job.Paths{Write: []string{"~/.claude/plans/**"}}}, `paths.write[0] "~/.claude/plans/**" begins with ~`},
		{job.Declaration{ID: "j", Paths: job.Paths{Write: []string{"api/**", "/abs/**"}}}, `paths.write[1] "/abs/**" is absolute`},
		{job.Declaration{ID: "j", Paths: job.Paths{Deny: []string{"../wt/**"}}}, `paths.deny[0] "../wt/**" climbs out of the workspace`},
		{job.Declaration{ID: "j", Paths: job.Paths{Read: []string{"docs/../../x"}}}, `paths.read[0] "docs/../../x" climbs out`},
	} {
		err := ValidateDeclaration(tt.decl)
		require.Error(t, err, tt.want)
		assert.Contains(t, err.Error(), tt.want)
	}
	assert.NoError(t, ValidateDeclaration(job.Declaration{ID: "j", ReadOnly: true, Paths: job.Paths{Read: []string{"docs/../api/**"}}}), "a path that cleans to one inside the workspace stands")
}

func TestDeclarationValidatesSignatureGoals(t *testing.T) {
	t.Parallel()

	shape := func(id string, symbols ...string) job.Goal {
		return job.Goal{ID: id, Kind: job.GoalKindSymbol, Expect: job.ExpectSignature, Symbols: symbols}
	}
	for _, tc := range []struct {
		name  string
		goals []job.Goal
		want  string
	}{
		{"one symbol, signature left for the fork to record", []job.Goal{shape("a", "Api")}, ""},
		{"one symbol with its signature", []job.Goal{{ID: "a", Kind: job.GoalKindSymbol, Expect: job.ExpectSignature, Symbols: []string{"Api"}, Signature: "func Api()"}}, ""},
		{"two symbols", []job.Goal{shape("a", "Api", "Old")}, "expects the signature of 2 symbols"},
		{"no symbol", []job.Goal{shape("a", " ")}, "names nothing to examine"},
		{"a signature on another expectation", []job.Goal{{ID: "a", Kind: job.GoalKindSymbol, Expect: job.ExpectPresent, Symbols: []string{"Api"}, Signature: "func Api()"}},
			`expects "present" and carries a signature`},
		{"two signatures for one symbol", []job.Goal{shape("a", "Api"), shape("b", "Api")}, `goals "a" and "b" both expect a signature of "Api"`},
		{"signatures for two symbols", []job.Goal{shape("a", "Api"), shape("b", "Old")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateDeclaration(job.Declaration{ID: "shaped", Goals: tc.goals})
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestDeclarationRefusesABlankBreaksEntry(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateDeclaration(job.Declaration{ID: "worker", Breaks: []string{"--old-flag", "api.Open"}}))
	err := ValidateDeclaration(job.Declaration{ID: "worker", Breaks: []string{"--old-flag", "  "}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "breaks[1] is blank")
}

func TestDeclarationIntegratesNamesMergeableBranchesOnce(t *testing.T) {
	t.Parallel()

	ok := job.Declaration{ID: "integrate", Integrates: []string{"done-a", "feature/done-b"}}
	require.NoError(t, ValidateDeclaration(ok))
	var row job.Job
	ok.Apply(&row)
	assert.True(t, row.Integrating())
	assert.Equal(t, []string{"done-a", "feature/done-b"}, row.Clone().Integrates)

	for branches, want := range map[string]string{
		"--force":       `integrates[0] "--force" is not a branch name a merge can take`,
		"a..b":          `integrates[0] "a..b" is not a branch name a merge can take`,
		"done done":     `integrates[0] "done done" is not a branch name a merge can take`,
		"done-a,done-a": `integrates[1] "done-a" is named twice`,
		"main.lock":     `integrates[0] "main.lock" is not a branch name a merge can take`,
	} {
		err := ValidateDeclaration(job.Declaration{ID: "integrate", Integrates: strings.Split(branches, ",")})
		require.Error(t, err, branches)
		assert.Contains(t, err.Error(), want, branches)
	}
	err := ValidateDeclaration(job.Declaration{ID: "integrate", Integrates: []string{"done-a"}, ReadOnly: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an integration job merges branches, so it writes")
	assert.False(t, job.Job{ID: "worker"}.Integrating())
}

func TestDeclarationCarryingEnterDeclaresNothingElse(t *testing.T) {
	t.Parallel()

	schema := job.Schema{Version: job.APIVersion}
	require.NoError(t, ValidateDeclaration(job.Declaration{Schema: schema, ID: "worker", Enter: "a.go"}))
	err := ValidateDeclaration(job.Declaration{Schema: schema, ID: "worker", Enter: "a.go", Paths: job.Paths{Write: []string{"**"}}})
	assert.ErrorContains(t, err, "carries only api_version, id and enter")
}

func TestScriptCheckNamesOneSubject(t *testing.T) {
	t.Parallel()

	script := types.LeaseCheck{Script: "probes/key.buzz"}
	gate := func(c types.LeaseCheck) job.Goal {
		return job.Goal{ID: "probe", Kind: job.GoalKindCheck, Check: fixtureCheck(c), Accept: []job.Basis{job.BasisWitnessed}}.Resolve()
	}
	require.NoError(t, validateGoal(gate(script)))

	both := script
	both.Target = "test"
	require.ErrorContains(t, validateGoal(gate(both)), "a check names one")

	scoped := script
	scoped.Project = "cmd/magus"
	require.ErrorContains(t, validateGoal(gate(scoped)), "belongs to the workspace")

	tested := script
	tested.Test = true
	require.NoError(t, validateGoal(gate(tested)))
}

func TestTestCheckIsAScriptChecksAlone(t *testing.T) {
	t.Parallel()

	target := job.Goal{ID: "unit", Kind: job.GoalKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: ".", Test: true})}.Resolve()
	require.ErrorContains(t, validateGoal(target), `check target "test" sets test`)

	rec := job.Declaration{Schema: job.Schema{Version: job.APIVersion}, ID: "fix-parser", Check: fixtureCheck(types.LeaseCheck{Target: "test", Test: true})}
	ps := declarationProblems(rec)
	require.Len(t, ps, 1)
	assert.Equal(t, "check.test", ps[0].Field)

	rec.Check = fixtureCheck(types.LeaseCheck{Script: "probes/key.buzz", Test: true})
	require.Empty(t, declarationProblems(rec))
	var row job.Job
	rec.Apply(&row)
	require.True(t, job.HasCheck(row.Check))
	assert.True(t, checkIn(t, row.Check).Test, "the record's test reaches the row's check")
}

func TestDeclarationProblemsCollectsEveryProblem(t *testing.T) {
	t.Parallel()
	d := job.Declaration{
		ID:          "bad id",
		Parent:      "bad parent",
		DependsOn:   []string{"ok", "bad dep"},
		State:       "nope",
		Timeout:     "soon",
		MaxAttempts: -1,
		Checkout:    "rel/dir",
		Paths:       job.Paths{Write: []string{"a.go", "../b.go"}},
		Breaks:      []string{" "},
		Check:       fixtureCheck(types.LeaseCheck{Target: "test", Project: "."}),
		Deviations: []job.Deviation{
			{Code: job.Code(types.JobRecordInvalid), Subject: "x", Reason: "r"},
			{Code: job.Code(types.GateNotRun)},
		},
		Goals: []job.Goal{
			{ID: "a", Kind: job.GoalKindPaths},
			{ID: "a", Kind: job.GoalKindPaths, Paths: []string{"x"}, DependsOn: []string{"missing"}},
		},
	}

	ps := declarationProblems(d)

	fields := make([]string, len(ps))
	for i, p := range ps {
		fields[i] = fmt.Sprintf("%s %s", string(p.Code), p.Field)
		assert.NotEmptyf(t, p.Message, "%s has a message", fields[i])
		assert.NotEmptyf(t, p.Fix, "%s has a fix", fields[i])
	}
	assert.Equal(t, []string{
		"MGS3037 id",
		"MGS3037 parent",
		"MGS3037 depends_on[1]",
		"MGS3037 state",
		"MGS3037 timeout",
		"MGS3037 max_attempts",
		"MGS3037 checkout",
		"MGS3037 paths.write[1]",
		"MGS3037 breaks[0]",
		"MGS3037 deviations[0].code",
		"MGS3037 deviations[1].subject",
		"MGS3037 deviations[1].reason",
		"MGS3038 goals[0].paths",
		"MGS3038 goals[1].id",
		"MGS3038 goals[1].depends_on[0]",
	}, fields)
	err := ValidateDeclaration(d)
	require.ErrorIs(t, err, types.JobRecordInvalid)
	require.ErrorIs(t, err, types.JobGoalInvalid)
}

func TestDeclarationProblemsOfAValidRecordIsEmpty(t *testing.T) {
	t.Parallel()
	d := job.Declaration{
		ID:         "wave/a",
		Paths:      job.Paths{Write: []string{"types/job.go"}},
		Check:      fixtureCheck(types.LeaseCheck{Target: "go-test", Project: "."}),
		Deviations: []job.Deviation{{Code: job.Code(types.GateNotRun), Subject: "lint .", Reason: "the root runs it"}},
		Goals:      []job.Goal{{ID: "names", Kind: job.GoalKindSymbol, Symbols: []string{"X"}, DependsOn: []string{job.PrimaryGoalID}}},
	}

	assert.Empty(t, declarationProblems(d))
	require.NoError(t, ValidateDeclaration(d))
}

func TestDeclarationProblemsReportsEachGoalCycleOnce(t *testing.T) {
	t.Parallel()
	d := job.Declaration{ID: "a", Goals: []job.Goal{
		{ID: "x", Kind: job.GoalKindPaths, Paths: []string{"p"}, DependsOn: []string{"y"}},
		{ID: "y", Kind: job.GoalKindPaths, Paths: []string{"p"}, DependsOn: []string{"x"}},
		{ID: "z", Kind: job.GoalKindPaths, Paths: []string{"p"}, DependsOn: []string{"z"}},
	}}

	ps := declarationProblems(d)

	require.Len(t, ps, 2)
	assert.Equal(t, "goals[0].depends_on", ps[1].Field)
	assert.Contains(t, ps[1].Message, `cycle at "x"`)
	assert.Equal(t, "goals[2].depends_on[0]", ps[0].Field)
	assert.Contains(t, ps[0].Message, "cannot depend on itself")
}

func TestDeclarationApplyCopiesDeviations(t *testing.T) {
	t.Parallel()
	deviations := []job.Deviation{{Code: job.Code(types.GateNotRun), Subject: "lint .", Reason: "the root runs it"}}
	var row job.Job

	job.Declaration{ID: "a", Deviations: deviations}.Apply(&row)
	deviations[0].Reason = "changed"

	assert.Equal(t, "the root runs it", row.Deviations[0].Reason)
}

func TestGoalProblemsNameTheFieldUnderTheGoal(t *testing.T) {
	t.Parallel()
	g := job.Goal{ID: "g", Kind: job.GoalKindSymbol, Expect: job.ExpectSignature, Symbols: []string{"A", "B"}, Paths: []string{"x"}}

	ps := types.FaultProblems(g.Faults())

	require.Len(t, ps, 2)
	assert.Equal(t, "paths", ps[0].Field)
	assert.Equal(t, "symbols", ps[1].Field)
	for _, p := range ps {
		assert.Equal(t, types.JobGoalInvalid, p.Code)
	}
	require.ErrorIs(t, validateGoal(g), types.JobGoalInvalid)
}

func TestAClaimRecordDeclaresNothingElse(t *testing.T) {
	t.Parallel()

	rec := job.Declaration{Schema: job.Schema{Version: job.APIVersion}, ID: "cli-ls-files", Claim: []job.ClaimRequest{{Path: "internal/guard/shell.go", Because: "refs:hint.VCSAdd"}}}
	assert.Empty(t, declarationProblems(rec))

	rec.Agent = job.Instructed("more")
	rec.Claim = append(rec.Claim, job.ClaimRequest{Path: "../x.go", Because: "released"}, job.ClaimRequest{Path: "internal/**", Because: "maybe"})
	var fields []string
	for _, p := range declarationProblems(rec) {
		fields = append(fields, p.Field)
	}
	assert.Equal(t, []string{"claim", "claim[1].path", "claim[2].path", "claim[2].because"}, fields)
}

func TestDeclarationCarriesMaxClaims(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateDeclaration(job.Declaration{ID: "worker", MaxClaims: 4}))
	assert.ErrorContains(t, ValidateDeclaration(job.Declaration{ID: "worker", MaxClaims: -1}), "max_claims is -1")

	var row job.Job
	job.Declaration{ID: "worker", MaxClaims: 4}.Apply(&row)
	assert.Equal(t, 4, row.MaxClaims)
	job.Declaration{ID: "worker"}.ApplySpec(&row)
	assert.Zero(t, row.MaxClaims)
}

type deliverySuite struct {
	suite.Suite
}

func TestDelivery(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(deliverySuite))
}

func (s *deliverySuite) TestEachDeliveryIsAccepted() {
	for _, d := range job.Deliveries() {
		s.Require().NoError(ValidateDeclaration(job.Declaration{ID: "worker", Delivery: d}), d)
	}
}

func (s *deliverySuite) TestAnUnknownDeliveryIsRefusedNamingTheSet() {
	err := ValidateDeclaration(job.Declaration{ID: "worker", Delivery: "merge"})
	s.Require().ErrorContains(err, `delivery "merge" is not a delivery`)
	s.Require().ErrorContains(err, "fold, separate, stacked")
}

func (s *deliverySuite) TestAParentHeadOffAStackedJobIsRefused() {
	err := ValidateDeclaration(job.Declaration{ID: "worker", Delivery: job.DeliveryFold, ParentHead: "feat/a"})
	s.Require().ErrorContains(err, "this job is not stacked")
}

func (s *deliverySuite) TestApplyCarriesDeliveryAndTheParentHead() {
	var row job.Job
	job.Declaration{ID: "worker", Delivery: job.DeliveryStacked, ParentHead: " feat/a "}.Apply(&row)
	var want job.Job
	job.Declaration{ID: "worker"}.Apply(&want)
	want.Delivery, want.ParentHead = job.DeliveryStacked, "feat/a"
	s.Equal(want, row)

	job.Declaration{ID: "worker"}.ApplySpec(&row)
	s.Empty(row.Delivery, "a spec naming no delivery clears it")
	s.Empty(row.ParentHead)
}
