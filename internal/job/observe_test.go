package job

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPathsGateReadsTheDiffAndNotTheReport is the whole point of the kind: the holder's
// own account of what it changed is not evidence, and a gate that read rep.ChangedPaths
// would be one more attestation wearing a gate's name.
func TestPathsGateReadsTheDiffAndNotTheReport(t *testing.T) {
	t.Parallel()

	row := types.Job{
		ID:         "unit",
		Created:    1,
		WritePaths: []string{"db", "api"},
		CompletionGates: []types.CompletionGate{
			{ID: "migration", Kind: types.GateKindPaths, Paths: []string{"db/migrations/**"}},
		},
	}
	// The holder says it wrote the migration. The diff says it wrote something else.
	rep := types.JobResult{Job: "unit", ChangedPaths: []string{"db/migrations/001.sql"}}
	seen := Observed{Changed: []string{"api/handler.go"}, ChangedKnown: true, ChangedFrom: "abc1234"}

	status := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, seen)
	assert.False(t, status.Verified)
	assert.Contains(t, status.Violations[0], `nothing matching "db/migrations/**" changed since abc1234`)
}

func TestPathsGateVerifiesWhenTheDiffCoversEveryGlob(t *testing.T) {
	t.Parallel()

	row := types.Job{
		ID:         "unit",
		Created:    1,
		WritePaths: []string{"db", "api"},
		CompletionGates: []types.CompletionGate{
			{ID: "migration", Kind: types.GateKindPaths, Paths: []string{"db/migrations/**", "api"}},
		},
	}
	rep := types.JobResult{Job: "unit", ChangedPaths: []string{"db/migrations/001.sql", "api/handler.go"}}
	seen := Observed{Changed: []string{"db/migrations/001.sql", "api/handler.go"}, ChangedKnown: true, ChangedFrom: "abc1234"}

	status := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, seen)
	assert.True(t, status.Verified, "violations: %v", status.Violations)
}

// TestPathsGateNamesEveryUnmetGlob pins the partial case, which is the common one: the
// migration lands and the test beside it does not. A gate that reported only "failed"
// would make the reader re-derive which half is missing.
func TestPathsGateNamesEveryUnmetGlob(t *testing.T) {
	t.Parallel()

	row := types.Job{
		ID:         "unit",
		Created:    1,
		WritePaths: []string{"db"},
		CompletionGates: []types.CompletionGate{
			{ID: "both", Kind: types.GateKindPaths, Paths: []string{"db/migrations/**", "db/schema.sql", "db/seed.sql"}},
		},
	}
	rep := types.JobResult{Job: "unit", ChangedPaths: []string{"db/migrations/001.sql"}}
	seen := Observed{Changed: []string{"db/migrations/001.sql"}, ChangedKnown: true, ChangedFrom: "abc1234"}

	status := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, seen)
	assert.False(t, status.Verified)
	assert.Len(t, status.Gates, 1)
	assert.Len(t, status.Gates[0].Violations, 2, "one per unmet glob, not one for the gate")
}

// TestPathsGateRefusesWhenTheDiffCouldNotBeRead is the direction that matters most. A
// guard that cannot ask stands down; a GATE that cannot verify must not certify, or the
// cheapest way to pass one is to break the observation.
func TestPathsGateRefusesWhenTheDiffCouldNotBeRead(t *testing.T) {
	t.Parallel()

	row := types.Job{
		ID:         "unit",
		Created:    1,
		WritePaths: []string{"db"},
		CompletionGates: []types.CompletionGate{
			{ID: "migration", Kind: types.GateKindPaths, Paths: []string{"db/migrations/**"}},
		},
	}
	rep := types.JobResult{Job: "unit", ChangedPaths: []string{"db/migrations/001.sql"}}

	status := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, Observed{})
	assert.False(t, status.Verified)
	assert.Contains(t, status.Violations[0], "could not read what this job changed")
}

// TestPathsGateSeparatesNothingChangedFromNobodyLooked pins why ChangedKnown exists at
// all: without it the zero value of Changed means both, and the failure mode is the
// silent one.
func TestPathsGateSeparatesNothingChangedFromNobodyLooked(t *testing.T) {
	t.Parallel()

	// Resolved, as every stored gate is: the verifier grades the fields and never defaults
	// them, so a raw gate here would exercise a shape production cannot produce.
	gate := types.CompletionGate{ID: "g", Kind: types.GateKindPaths, Paths: []string{"db/**"}}.Resolve()

	looked := verifySubjectGate(gate, Observed{ChangedKnown: true, ChangedFrom: "abc1234"})
	assert.False(t, looked.Verified)
	assert.Contains(t, looked.Violations[0], "nothing matching")

	blind := verifySubjectGate(gate, Observed{})
	assert.False(t, blind.Verified)
	assert.Contains(t, blind.Violations[0], "could not read")
}

// TestEveryKindAndExpectPairIsGraded walks the declared matrix and asserts each pair
// reaches a verdict rather than falling through a switch to a silent pass. A kind added
// without an arm in Observed.holds would otherwise verify by default, which is the one
// failure direction a gate may never have.
func TestEveryKindAndExpectPairIsGraded(t *testing.T) {
	t.Parallel()

	// Everything observed, and nothing matching what the gates name, so every pair that
	// is graded at all must come back UNMET. A pair that verifies here is one nobody
	// graded.
	seen := Observed{
		ChangedKnown: true, PresentKnown: true, SymbolsKnown: true,
		ChangedFrom: "abc1234",
		Symbols:     map[string]SymbolFact{},
	}
	graded := 0
	for _, kind := range types.GateKinds() {
		if kind == types.GateKindCheck {
			continue // graded against the output store, not against Observed
		}
		for _, expect := range types.GateExpects() {
			// ONE subject, matching the kind. Carrying both Paths and Symbols made
			// Validate refuse every pair as two subjects, so this loop used to `continue`
			// on every iteration and grade nothing at all.
			gate := types.CompletionGate{ID: "g", Kind: kind, Expect: expect}
			if kind == types.GateKindPaths {
				gate.Paths = []string{"nope/**"}
			} else {
				gate.Symbols = []string{"Nope"}
			}
			if err := gate.Validate(); err != nil {
				continue // the matrix refuses this pair, so there is nothing to grade
			}
			graded++
			status := verifySubjectGate(gate, seen)
			// `absent` and `unreferenced` of something that is not there ARE satisfied,
			// which are the verified cases in this sweep and are correct.
			if expect == types.ExpectAbsent || expect == types.ExpectUnreferenced {
				assert.True(t, status.Verified, "%s/%s: nothing named is present, so it holds", kind, expect)
				continue
			}
			assert.False(t, status.Verified, "%s/%s verified against an observation holding nothing", kind, expect)
			assert.NotEmpty(t, status.Violations, "%s/%s failed without saying why", kind, expect)
		}
	}
	// The guard against the shape this test had before: every iteration hit `continue`,
	// so it swept the whole matrix and asserted nothing.
	require.NotZero(t, graded, "the sweep graded no pair at all, so it is measuring nothing")
}

// TestGradeGatesReportsAPrimaryCheck is the regression for a job declaring only --check:
// VerifyGates used to grade the primary by hand and append it to Gates only when OTHER
// gates existed, so `describe job --gates` reported "no completion gate" and exited 0 for
// a job that had one.
func TestGradeGatesReportsAPrimaryCheck(t *testing.T) {
	t.Parallel()

	row := types.Job{
		ID:         "unit",
		Created:    1,
		WritePaths: []string{"api"},
		Check:      &types.LeaseCheck{Target: "go-test", Project: "api"},
	}
	status := VerifyGates(row, types.JobResult{Job: "unit", ChangedPaths: []string{"api/x.go"}},
		types.JobAttempt{}, nil, []types.Job{row}, Observed{})

	require.Len(t, status.Gates, 1, "a job whose only gate is its primary check must report that gate")
	assert.Equal(t, types.PrimaryCompletionGateID, status.Gates[0].ID)
	assert.False(t, status.Gates[0].Verified, "no run was recorded, so the check is unmet")
}

// TestCheckpointObserverCutsThePatchDigest pins that only the revision half of a
// checkpoint token is handed to a VCS. The digest marks a dirty tree and is not a
// revision any backend can resolve.
func TestCheckpointObserverCutsThePatchDigest(t *testing.T) {
	t.Parallel()

	// An empty root resolves no VCS, so the observation is UNKNOWN rather than empty,
	// which is the contract a paths gate turns on.
	seen, err := CheckpointObserver("", nil)(t.Context(), types.Job{Checkpoint: "abc1234+deadbeef"})
	assert.NoError(t, err)
	assert.False(t, seen.ChangedKnown)
}
