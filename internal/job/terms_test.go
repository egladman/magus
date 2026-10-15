package job

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func briefRow() types.Job {
	return types.Job{
		ID:     "harness/ledger-per-repo",
		Parent: "harness",
		Criteria: "Move the lease ledger to the per-repository state dir.\n" +
			"Done when a row put from one checkout is listed from a second worktree of the same repo.",
		Checkpoint: "cf5509d09",
		WritePaths: []string{"internal/ledger", "cmd/magus/ledger.go"},
		DenyPaths:  []string{"MAGUS.md", "docs/gen"},
		DependsOn:  []string{"harness/session-load"},
		Model:      "principal",
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
	assert.Contains(t, b.String(), briefRow().Criteria, "the row is rendered whatever the workspace does")
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
		Stage:              types.ReleaseStage{Tag: "v0.4.0"},
		DeletionCandidates: []types.UnreferencedEntry{{ID: "a", Label: "stale", Kind: "Function", Source: "internal/ledger/x.go:4"}},
	}

	got := NewTerms(row, facts).String()

	want := "job: harness/ledger-per-repo\n" +
		"\ninterface\n" +
		"  Open (signature): func Open(root string) error\n" +
		"    opens the ledger at root\n" +
		"  OpenLegacy (absent)\n" +
		"\ncriteria\n"
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

	assert.Equal(t, NewTerms(briefRow(), TermsFacts{}).String(), NewTerms(briefRow(), TermsFacts{Stage: types.ReleaseStage{}}).String())
	bare := NewTerms(briefRow(), TermsFacts{}).String()
	for _, heading := range []string{"interface", "may break", "release stage", deletionHeading} {
		assert.NotContains(t, bare, heading)
	}

	got := NewTerms(briefRow(), TermsFacts{Stage: types.ReleaseStage{Tag: "v1.2.0", Major: 1}}).String()
	assert.Contains(t, got, "\nrelease stage\n  newest release v1.2.0\n")
}

// Nothing outside the fixed order reaches the worker, which is how the gate stops leaking
// into a brief: `ci` is not rendered because nothing renders it.
func TestTermsRendersOnlyTheRow(t *testing.T) {
	t.Parallel()

	row := briefRow()
	row.Criteria, row.DependsOn, row.DenyPaths = "", nil, nil
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
		tier types.JobModelTier
		want string
	}{
		{
			name: "a matrix answer names its reason and site",
			tier: types.JobModelTier{Name: "economy", Rank: 0, Skills: types.SkillVariantFull, Source: types.ModelTierSourceMatrix, Reason: "small checked job", Site: "magusfile.buzz:20"},
			want: "job: harness/ledger-per-repo\nmodel tier: economy (rank 0), matrix: \"small checked job\" at magusfile.buzz:20\nskills: full\n",
		},
		{
			name: "a default has no site",
			tier: types.JobModelTier{Name: "standard", Rank: 1, Skills: types.SkillVariantShort, Source: types.ModelTierSourceDefault, Reason: "the declared default"},
			want: "job: harness/ledger-per-repo\nmodel tier: standard (rank 1), default: \"the declared default\"\nskills: short\n",
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

// retriedRow is a job on its third attempt of four: the second ended failing with
// everything an attempt can leave, the first ended by the sweep.
func retriedRow() types.Job {
	first := types.PriorAttempt{N: 1, State: types.StateNoReturn, EndReason: "its checkout is gone", ModelTier: "economy", DenialsKnown: true}
	first.Retry = types.RetryVerdict{Retryable: true}
	second := types.PriorAttempt{N: 2, State: types.StateFail, ModelTier: "standard", ReworkRounds: 2, Widenings: 1, DenialsKnown: true}
	second.Unmet = []types.GateStatus{{ID: "check"}, {ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	second.Violations = []string{`changed path "docs/x.md" is outside the job's write paths (internal/ledger)`}
	second.UnresolvedRisks = []string{"the second worktree was not tried"}
	second.Denials = []types.JobDenial{{Rule: "lease-write", Reason: "docs/x.md belongs to no lease you hold\nask the orchestrator"}}
	second.FailingRefs = []string{"out0123456789ab"}
	second.Retry = types.RetryVerdict{Change: types.RetryAfterWidening,
		Reason: "the guard denied its holder under lease-write: docs/x.md belongs to no lease you hold"}

	row := types.Job{ID: "harness/retry", Criteria: "Retry the store.", State: types.StateDeclared}
	row.WritePaths = []string{"internal/ledger"}
	row.Validation = "magus run test internal/ledger"
	row.Attempts, row.MaxAttempts = 3, 4
	row.Prior = []types.PriorAttempt{first, second}
	return row
}

// The previous attempt is what the next holder starts from, so it renders byte for byte
// the same twice: the newest in full, the older ones a line each.
func TestTermsRendersThePreviousAttemptGolden(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile(filepath.Join("testdata", "previous_attempt.golden"))
	require.NoError(t, err)
	first := NewTerms(retriedRow(), TermsFacts{}).String()
	assert.Equal(t, string(want), first)
	assert.Equal(t, first, NewTerms(retriedRow(), TermsFacts{}).String())
}

func TestTermsSaysWhenAnAttemptsDenialsAreUnknown(t *testing.T) {
	t.Parallel()

	row := retriedRow()
	row.Prior[1].Denials, row.Prior[1].DenialsKnown = nil, false
	got := NewTerms(row, TermsFacts{}).String()
	assert.Contains(t, got, "    (denials unknown: the trail was rotated)\n")
	assert.NotContains(t, got, "denied:")
}

func TestTermsNamesTheAttemptOnlyPastTheFirstOrUnderACap(t *testing.T) {
	t.Parallel()

	first := briefRow()
	first.Attempts = 1
	assert.NotContains(t, NewTerms(first, TermsFacts{}).String(), "attempt")

	capped := briefRow()
	capped.MaxAttempts = 2
	got := NewTerms(capped, TermsFacts{}).String()
	assert.Contains(t, got, "\nattempt 1 of 2\n")
	assert.NotContains(t, got, "previous attempts", "a section with nothing in it is dropped")

	uncapped := retriedRow()
	uncapped.MaxAttempts = 0
	assert.Contains(t, NewTerms(uncapped, TermsFacts{}).String(), "\nattempt 3\n")
}
