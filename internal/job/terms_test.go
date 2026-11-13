package job

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/egladman/magus/types/release"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type termsDeliverySuite struct {
	suite.Suite
}

func TestTermsDelivery(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(termsDeliverySuite))
}

func (s *termsDeliverySuite) TestAStackedJobNamesTheBranchItIsBasedOn() {
	row := briefRow()
	row.Delivery, row.ParentHead = types.DeliveryStacked, "feat/parent"
	s.Contains(NewTerms(row, TermsFacts{}).String(), "delivery: stacked on feat/parent\n")
}

func (s *termsDeliverySuite) TestAFoldedJobNamesItsDelivery() {
	row := briefRow()
	row.Delivery = types.DeliveryFold
	s.Contains(NewTerms(row, TermsFacts{}).String(), "delivery: fold\n")
}

func (s *termsDeliverySuite) TestARowWithNoDeliveryPrintsNoLine() {
	s.NotContains(NewTerms(briefRow(), TermsFacts{}).String(), "delivery:")
}

func (s *termsDeliverySuite) TestARetryKeepsTheDeliveryAndTheParentHead() {
	row := briefRow()
	row.Delivery, row.ParentHead = types.DeliveryStacked, "feat/parent"
	want := RetryDeclaration(briefRow())
	want.Delivery, want.ParentHead = types.DeliveryStacked, "feat/parent"
	s.Equal(want, RetryDeclaration(row))
}

func briefRow() types.Job {
	return types.Job{
		ID:     "harness/ledger-per-repo",
		Parent: "harness",
		Agent: &agent.Spec{
			Instructions: "Move the lease ledger to the per-repository state dir.\n" +
				"Done when a row put from one checkout is listed from a second worktree of the same repo.",
			Model:  agent.Model{Tier: "design"},
			Worker: agent.Worker{Type: agent.WorkerTypeResumed},
		},
		Checkpoint: "cf5509d09",
		WritePaths: []string{"internal/ledger", "cmd/magus/ledger.go"},
		DenyPaths:  []string{"MAGUS.md", "docs/gen"},
		DependsOn:  []string{"harness/session-load"},
		Validation: "magus run test internal/ledger",
		State:      types.StateDeclared,
	}
}

// The brief is only worth building if two renders of one row agree byte for byte, so the
// golden IS the contract.
func TestTermsRendersTheGolden(t *testing.T) {
	t.Parallel()

	b := NewTerms(briefRow(), TermsFacts{
		Evidence: []TermsEvidence{{Path: "internal/ledger", Node: "dir:internal/ledger", BlastRadius: 4}},
	})
	want, err := os.ReadFile(filepath.Join("testdata", "brief.golden"))
	require.NoError(t, err)
	assert.Equal(t, string(want), b.String())
}

// A cold graph says so once. Silence would read as "nothing depends on any of this",
// which is the opposite of what magus knows.
func TestTermsNamesAColdGraphOnce(t *testing.T) {
	t.Parallel()

	b := NewTerms(briefRow(), TermsFacts{GraphCold: true})
	assert.Equal(t, 1, strings.Count(b.String(), "the knowledge graph is cold"))
}

// A workspace that would not load says so too, for the same reason: a worker in a tree
// whose magusfile is mid-edit still needs the row it was handed.
func TestTermsNamesAWorkspaceThatWouldNotLoad(t *testing.T) {
	t.Parallel()

	b := NewTerms(briefRow(), TermsFacts{WorkspaceCold: true})
	assert.Contains(t, b.String(), "this workspace would not load")
	assert.Contains(t, b.String(), briefRow().Instructions(), "the row is rendered whatever the workspace does")
}

// The unrun gates are a section of the terms, after the regenerated outputs, and a row with
// none renders as it did before the section existed.
func TestTermsRendersUnrunGatesAsASection(t *testing.T) {
	t.Parallel()

	bare := NewTerms(briefRow(), TermsFacts{}).String()
	assert.NotContains(t, bare, unrunHeading)

	gate := UnrunGate{Target: "lint", Project: ".", Source: "**/*.go", Goal: unrunGoal(".", "lint", map[string]bool{})}
	got := NewTerms(briefRow(), TermsFacts{UnrunGates: []UnrunGate{gate}}).String()
	assert.Contains(t, got, "\n"+unrunHeading+"\n  lint . (reads **/*.go)\n  goals to add: ")
	assert.Less(t, strings.Index(got, unrunHeading), strings.Index(got, "the only check you run"),
		"the section sits among the facts, ahead of the check")
}

// The files outside the write paths that use them follow the deny paths, so the holder
// reads the paths to add beside the paths it has; a row with none renders no heading.
func TestTermsRendersReferencedOutsideAfterThePaths(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, NewTerms(briefRow(), TermsFacts{}).String(), referencedHeading)

	refs := []ReferencedFile{{File: "internal/ledger/x_test.go", Count: 2, Uses: []string{"internal/ledger/x.go"}}}
	got := NewTerms(briefRow(), TermsFacts{ReferencedOutside: refs}).String()
	assert.Contains(t, got, "\n"+referencedHeading+"\n  internal/ledger/x_test.go (tests internal/ledger/x.go, 2 reference(s))\n")
	assert.Less(t, strings.Index(got, "write paths"), strings.Index(got, referencedHeading))
	assert.Less(t, strings.Index(got, referencedHeading), strings.Index(got, "the only check you run"))

	unknown := NewTerms(briefRow(), TermsFacts{ReferencedOutsideGap: NoSymbolIndex()}).String()
	assert.Contains(t, unknown, fmt.Sprintf(`
%s
  unknown, %s
`, referencedHeading, NoSymbolIndex()),
		"an unindexed graph says the list is unknown rather than dropping the heading")
}

// The grouping history renders only when it was read, and a lens with nothing for the
// lease's projects says so rather than dropping the section.
func TestTermsRendersTheGroupingHistoryWhenItWasRead(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, NewTerms(briefRow(), TermsFacts{}).String(), "grouping history")

	empty := GroupingRecords(nil, []string{"internal/a"})
	got := NewTerms(briefRow(), TermsFacts{Grouping: &empty}).String()
	assert.Contains(t, got, "\ngrouping history\n  none recorded for these projects\n")
	assert.Less(t, strings.Index(got, "grouping history"), strings.Index(got, "the only check you run"))
}

// The interface is what the holder implements, so it leads; what the job may break, the
// release stage and the deletion candidates follow the criteria in that order, and the
// check stays the only check named.
func TestTermsRendersTheInterfaceFirstThenWhatMayBreak(t *testing.T) {
	t.Parallel()

	row := briefRow()
	row.Goals = []types.Goal{
		{ID: "api", Kind: types.GoalKindSymbol, Expect: types.ExpectSignature, Symbols: []string{"Open"},
			Signature: "func Open(root string) error", Description: "opens the ledger at root"},
		{ID: "gone", Kind: types.GoalKindSymbol, Expect: types.ExpectAbsent, Symbols: []string{"OpenLegacy"}},
		{ID: "unit", Kind: types.GoalKindCheck, Expect: types.ExpectPassed, Check: types.LeaseCheck{Target: "test", Project: "."}},
	}
	row.Breaks = []string{"--old-flag", "ledger.Open"}
	facts := TermsFacts{
		Stage:              release.Stage{Tag: "v0.4.0"},
		DeletionCandidates: []types.UnreferencedEntry{{ID: "a", Label: "stale", Kind: "Function", Source: "internal/ledger/x.go:4"}},
	}

	got := NewTerms(row, facts).String()

	want := "job: harness/ledger-per-repo\n" +
		"\ninterface\n" +
		"  Open (signature): func Open(root string) error\n" +
		"    opens the ledger at root\n" +
		"  OpenLegacy (absent)\n" +
		"\ninstructions\n"
	assert.True(t, strings.HasPrefix(got, want), "got:\n%s", got)
	for _, section := range []string{
		"\nmay break\n  --old-flag\n  ledger.Open\n",
		"\nrelease stage\n  newest release v0.4.0: below 1.0 a major floor ships as a minor release\n",
		"\n" + deletionHeading + "\n  stale (function) at internal/ledger/x.go:4\n",
		"\nwrite paths\n",
	} {
		assert.Contains(t, got, section)
	}
	assert.Less(t, strings.Index(got, "may break"), strings.Index(got, "release stage"))
	assert.Less(t, strings.Index(got, "release stage"), strings.Index(got, deletionHeading))
	assert.Less(t, strings.Index(got, deletionHeading), strings.Index(got, "write paths"))
	assert.NotContains(t, got, "unit", "a check goal is not an interface; the check stays the only one named")
	assert.Equal(t, got, NewTerms(row, facts).String(), "two renders are byte-identical")
}

// An empty section is dropped, a major release names no pre-1.0 rule, and an unknown stage
// renders nothing rather than a guess.
func TestTermsDropsEmptyInterfaceStageAndCandidates(t *testing.T) {
	t.Parallel()

	assert.Equal(t, NewTerms(briefRow(), TermsFacts{}).String(), NewTerms(briefRow(), TermsFacts{Stage: release.Stage{}}).String())
	bare := NewTerms(briefRow(), TermsFacts{}).String()
	for _, heading := range []string{"interface", "may break", "release stage", deletionHeading} {
		assert.NotContains(t, bare, heading)
	}

	got := NewTerms(briefRow(), TermsFacts{Stage: release.Stage{Tag: "v1.2.0", Major: 1}}).String()
	assert.Contains(t, got, "\nrelease stage\n  newest release v1.2.0\n")
}

// Nothing outside the fixed order reaches the worker, which is how the gate stops leaking
// into a brief: `ci` is not rendered because nothing renders it.
func TestTermsRendersOnlyTheRow(t *testing.T) {
	t.Parallel()

	row := briefRow()
	row.Agent, row.DependsOn, row.DenyPaths = nil, nil, nil
	got := NewTerms(row, TermsFacts{}).String()

	assert.NotContains(t, got, "goal")
	assert.NotContains(t, got, "depends on")
	assert.NotContains(t, got, "deny paths")
	assert.Contains(t, got, "the only check you run")
	assert.Contains(t, got, row.Validation)
}

// The tier a fork assigned leads the terms, with the reason and site that chose it, so a
// holder reads which skill variant it was handed without asking.
func TestTermsNamesTheModelTierAndItsSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tier agent.JobModelTier
		want string
	}{
		{
			name: "a matrix answer names its reason and site",
			tier: agent.JobModelTier{Name: "routine", Rank: 0, Skills: agent.SkillVariantFull, Source: agent.ModelTierSourceMatrix, Reason: "small checked job", Site: "magusfile.buzz:20"},
			want: "job: harness/ledger-per-repo\nmodel tier: routine (rank 0), matrix: \"small checked job\" at magusfile.buzz:20\nskills: full\n",
		},
		{
			name: "a default has no site",
			tier: agent.JobModelTier{Name: "bounded", Rank: 1, Skills: agent.SkillVariantShort, Source: agent.ModelTierSourceDefault, Reason: "the declared default"},
			want: "job: harness/ledger-per-repo\nmodel tier: bounded (rank 1), default: \"the declared default\"\nskills: short\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			row := briefRow()
			row.ModelTier = &tt.tier
			assert.True(t, strings.HasPrefix(NewTerms(row, TermsFacts{}).String(), tt.want))
		})
	}
}

func TestTermsPrintsNoModelTierForARowWithout(t *testing.T) {
	t.Parallel()

	assert.NotContains(t, NewTerms(briefRow(), TermsFacts{}).String(), "model tier")
}

// retriedRow is a job on its third attempt of four.
func retriedRow() types.Job {
	row := types.Job{ID: "harness/retry", Agent: agent.Instructed("Retry the store."), State: types.StateDeclared}
	row.WritePaths = []string{"internal/ledger"}
	row.Validation = "magus run test internal/ledger"
	row.MaxAttempts = 4
	return row
}

// retriedTries are retriedRow's two ended tries: the second failing with
// everything a try can leave, the first ended by the sweep.
func retriedTries() []types.Attempt {
	first := types.AttemptStatus{State: types.StateNoReturn, EndReason: "its checkout is gone", ModelTier: "routine", DenialsKnown: true}
	first.Retry = types.RetryVerdict{Retryable: true}
	second := types.AttemptStatus{State: types.StateFail, ModelTier: "bounded", ReworkRounds: 2, Widenings: 1, DenialsKnown: true}
	second.Unmet = []types.VerificationStatus{{ID: "check"}, {ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	second.Violations = []string{`changed path "docs/x.md" is outside the job's write paths (internal/ledger)`}
	second.Result = &types.JobResult{UnresolvedRisks: []string{"the second worktree was not tried"}}
	second.Denials = []types.JobDenial{{Rule: "lease-write", Reason: "docs/x.md belongs to no lease you hold\nask the orchestrator"}}
	second.FailingRefs = []string{"out0123456789ab"}
	second.Retry = types.RetryVerdict{Change: types.RetryAfterWidening,
		Reason: "the guard denied its holder under lease-write: docs/x.md belongs to no lease you hold"}
	return []types.Attempt{{Job: "harness/retry", N: 1, Status: first}, {Job: "harness/retry", N: 2, Status: second}}
}

// The previous attempt is what the next holder starts from, so it renders byte for byte
// the same twice: the newest in full, the older ones a line each.
func TestTermsRendersThePreviousAttemptGolden(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile(filepath.Join("testdata", "previous_attempt.golden"))
	require.NoError(t, err)
	first := NewTerms(retriedRow(), TermsFacts{Attempts: retriedTries()}).String()
	assert.Equal(t, string(want), first)
	assert.Equal(t, first, NewTerms(retriedRow(), TermsFacts{Attempts: retriedTries()}).String())
}

func TestTermsSaysWhenAnAttemptsDenialsAreUnknown(t *testing.T) {
	t.Parallel()

	tries := retriedTries()
	tries[1].Status.Denials, tries[1].Status.DenialsKnown = nil, false
	got := NewTerms(retriedRow(), TermsFacts{Attempts: tries}).String()
	assert.Contains(t, got, "    (denials unknown: the trail was rotated)\n")
	assert.NotContains(t, got, "denied:")
}

func TestTermsNamesTheAttemptOnlyPastTheFirstOrUnderACap(t *testing.T) {
	t.Parallel()

	first := briefRow()
	assert.NotContains(t, NewTerms(first, TermsFacts{}).String(), "attempt")

	capped := briefRow()
	capped.MaxAttempts = 2
	got := NewTerms(capped, TermsFacts{}).String()
	assert.Contains(t, got, "\nattempt 1 of 2\n")
	assert.NotContains(t, got, "previous attempts", "a section with nothing in it is dropped")

	uncapped := retriedRow()
	uncapped.MaxAttempts = 0
	assert.Contains(t, NewTerms(uncapped, TermsFacts{Attempts: retriedTries()}).String(), "\nattempt 3\n")
}

type termsTestScopeSuite struct{ suite.Suite }

func TestTermsTestScope(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(termsTestScopeSuite))
}

func (s *termsTestScopeSuite) facts() TermsFacts {
	return TermsFacts{TestScope: []TestScope{
		{Project: ".", Spell: "go", Op: "go::go-test", Files: []string{"pkg/a/x.go"}, Operands: []string{"./pkg/a"}},
		{Project: "docs", Spell: "typescript", Files: []string{"docs/a.ts"}, Whole: "spell typescript declares no testScope, so its whole project runs"},
	}}
}

func (s *termsTestScopeSuite) TestRendersOperandsAndTheReasonForAWholeRun() {
	got := NewTerms(briefRow(), s.facts()).String()
	s.Contains(got, "\nnarrow test scope\n"+
		"  . go::go-test ./pkg/a (pkg/a/x.go)\n"+
		"  docs: not narrowed: spell typescript declares no testScope, so its whole project runs (docs/a.ts)\n")
	s.Less(strings.Index(got, testScopeHeading), strings.Index(got, "the only check you run"))
}

func (s *termsTestScopeSuite) TestNoScopeDropsTheSection() {
	s.NotContains(NewTerms(briefRow(), TermsFacts{}).String(), testScopeHeading)
}

func (s *termsTestScopeSuite) TestJSONCarriesTestScope() {
	b, err := json.Marshal(NewTerms(briefRow(), s.facts()))
	s.Require().NoError(err)
	s.Contains(string(b), `"test_scope":[{"project":".","spell":"go","op":"go::go-test","files":["pkg/a/x.go"],"operands":["./pkg/a"]}`)
}
