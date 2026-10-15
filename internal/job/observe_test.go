package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/gen/mocks"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
		Goals: []types.Goal{
			{ID: "migration", Kind: types.GoalKindPaths, Paths: []string{"db/migrations/**"}},
		},
	}
	// The holder says it wrote the migration. The diff says it wrote something else.
	rep := types.JobResult{Job: "unit", ChangedPaths: []string{"db/migrations/001.sql"}}
	seen := Observed{Changed: []string{"api/handler.go"}, ChangedKnown: true, ChangedFrom: "abc1234"}

	status := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, seen)
	assert.False(t, status.Verified)
	assert.Contains(t, strings.Join(status.Violations, "\n"), `nothing matching "db/migrations/**" changed since abc1234`)
}

func TestPathsGateVerifiesWhenTheDiffCoversEveryGlob(t *testing.T) {
	t.Parallel()

	row := types.Job{
		ID:         "unit",
		Created:    1,
		WritePaths: []string{"db", "api"},
		Goals: []types.Goal{
			{ID: "migration", Kind: types.GoalKindPaths, Paths: []string{"db/migrations/**", "api"}},
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
		Goals: []types.Goal{
			{ID: "both", Kind: types.GoalKindPaths, Paths: []string{"db/migrations/**", "db/schema.sql", "db/seed.sql"}},
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
		Goals: []types.Goal{
			{ID: "migration", Kind: types.GoalKindPaths, Paths: []string{"db/migrations/**"}},
		},
	}
	rep := types.JobResult{Job: "unit", ChangedPaths: []string{"db/migrations/001.sql"}}

	status := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, Observed{})
	assert.False(t, status.Verified)
	require.Len(t, status.Gates, 1)
	assert.Contains(t, strings.Join(status.Gates[0].Violations, "\n"), "could not read what this job changed")
}

// TestPathsGateSeparatesNothingChangedFromNobodyLooked pins why ChangedKnown exists at
// all: without it the zero value of Changed means both, and the failure mode is the
// silent one.
func TestPathsGateSeparatesNothingChangedFromNobodyLooked(t *testing.T) {
	t.Parallel()

	// Resolved, as every stored gate is: the verifier grades the fields and never defaults
	// them, so a raw gate here would exercise a shape production cannot produce.
	gate := types.Goal{ID: "g", Kind: types.GoalKindPaths, Paths: []string{"db/**"}}.Resolve()

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
	for _, kind := range types.GoalKinds() {
		if kind == types.GoalKindCheck {
			continue // graded against the output store, not against Observed
		}
		for _, expect := range types.GoalExpects() {
			// ONE subject, matching the kind. Carrying both Paths and Symbols made
			// Validate refuse every pair as two subjects, so this loop used to `continue`
			// on every iteration and grade nothing at all.
			gate := types.Goal{ID: "g", Kind: kind, Expect: expect}
			if kind == types.GoalKindPaths {
				gate.Paths = []string{"nope/**"}
			} else {
				gate.Symbols = []string{"Nope"}
			}
			if err := gate.Validate(); err != nil {
				continue // the matrix refuses this pair, so there is nothing to grade
			}
			graded++
			status := verifySubjectGate(gate, seen)
			if kind == types.GoalKindPaths && expect == types.ExpectAbsent {
				// A path the tree was read for and holds nothing of IS absent.
				assert.True(t, status.Verified, "%s/%s: nothing named is present, so it holds", kind, expect)
				continue
			}
			// A symbol nobody asked the graph about is no evidence of anything, so even
			// `absent` and `unreferenced` stay unmet.
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
// gates existed, so `describe job` reported "no completion gate" for a job that had one.
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
	assert.Equal(t, types.PrimaryGoalID, status.Gates[0].ID)
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
	require.NoError(t, err)
	assert.False(t, seen.ChangedKnown)
	assert.False(t, seen.RegionsKnown)
	assert.NotEmpty(t, seen.RegionsReason, "an unknown footprint says why")
}

func TestChangedSinceReadsRegionsForTheChangedPathsOnly(t *testing.T) {
	t.Parallel()

	regions := []types.RegionChange{
		{File: types.FileChange{Path: "a.go"}, Side: types.RegionNew, Lines: [2]int{4, 9}, Declaration: "func X() {", Driver: "golang"},
		{File: types.FileChange{Path: "b.md"}, Side: types.RegionOld, Lines: [2]int{2, 2}},
	}
	driver := mocks.NewMockVCSDriver(t)
	driver.EXPECT().ChangedFiles(mock.Anything, "/repo", "abc1234").Return([]string{"a.go", "b.md"}, nil)
	driver.EXPECT().Regions(mock.Anything, "/repo", "abc1234", []types.FileChange{{Path: "a.go"}, {Path: "b.md"}}).Return(regions, nil)

	seen := changedSince(t.Context(), driver, "", "/repo", "abc1234+deadbeef")

	assert.Equal(t, Observed{
		Changed: []string{"a.go", "b.md"}, ChangedKnown: true, ChangedFrom: "abc1234",
		Regions: regions, RegionsKnown: true,
	}, seen)
}

// A backend that declines RegionReporter leaves the footprint UNKNOWN, never empty: an
// empty footprint would read as a job that touched no declaration.
func TestChangedSinceLeavesRegionsUnknownWhenTheVCSDeclines(t *testing.T) {
	t.Parallel()

	driver := mocks.NewMockVCSDriver(t)
	driver.EXPECT().ChangedFiles(mock.Anything, "/repo", "abc1234").Return([]string{"a.go"}, nil)
	driver.EXPECT().Regions(mock.Anything, "/repo", "abc1234", []types.FileChange{{Path: "a.go"}}).
		Return(nil, &types.VCSUnsupportedError{VCS: "git", Capability: types.CapRegionReporter})

	seen := changedSince(t.Context(), driver, "", "/repo", "abc1234")

	assert.Equal(t, Observed{
		Changed: []string{"a.go"}, ChangedKnown: true, ChangedFrom: "abc1234",
		RegionsReason: "git does not report changed regions (RegionReporter)",
	}, seen)
}

func TestChangedSinceSaysWhyRegionsAreUnknown(t *testing.T) {
	t.Parallel()

	t.Run("no checkpoint", func(t *testing.T) {
		seen := changedSince(t.Context(), mocks.NewMockVCSDriver(t), "", "/repo", "")
		assert.Equal(t, Observed{RegionsReason: "the job was declared without a checkpoint, so there is no revision to diff against"}, seen)
	})
	t.Run("no vcs", func(t *testing.T) {
		seen := changedSince(t.Context(), nil, "version control is disabled here", "/repo", "abc1234")
		assert.Equal(t, Observed{ChangedFrom: "abc1234", RegionsReason: "version control is disabled here"}, seen)
	})
	t.Run("diff unreadable", func(t *testing.T) {
		driver := mocks.NewMockVCSDriver(t)
		driver.EXPECT().ChangedFiles(mock.Anything, "/repo", "abc1234").Return(nil, errors.New("bad revision"))
		seen := changedSince(t.Context(), driver, "", "/repo", "abc1234")
		assert.Equal(t, Observed{ChangedFrom: "abc1234", RegionsReason: "the diff since abc1234 could not be read: bad revision"}, seen)
	})
	t.Run("regions unreadable", func(t *testing.T) {
		driver := mocks.NewMockVCSDriver(t)
		driver.EXPECT().ChangedFiles(mock.Anything, "/repo", "abc1234").Return([]string{"a.go"}, nil)
		driver.EXPECT().Regions(mock.Anything, "/repo", "abc1234", []types.FileChange{{Path: "a.go"}}).Return(nil, errors.New("exit 128"))
		seen := changedSince(t.Context(), driver, "", "/repo", "abc1234")
		assert.Equal(t, Observed{
			Changed: []string{"a.go"}, ChangedKnown: true, ChangedFrom: "abc1234",
			RegionsReason: "the regions changed since abc1234 could not be read: exit 128",
		}, seen)
	})
}

// A job that changed nothing has an empty footprint without asking for regions, so a backend
// declining the capability cannot turn "touched nothing" into "not known".
func TestChangedSinceDoesNotAskForRegionsWhenNothingChanged(t *testing.T) {
	t.Parallel()

	driver := mocks.NewMockVCSDriver(t)
	driver.EXPECT().ChangedFiles(mock.Anything, "/repo", "abc1234").Return(nil, nil)

	seen := changedSince(t.Context(), driver, "", "/repo", "abc1234")

	assert.Equal(t, Observed{ChangedKnown: true, ChangedFrom: "abc1234", RegionsKnown: true}, seen)
}

// A holder whose branch never had its checkpoint is diffed from where the two meet, so the
// checkpoint's own commits are not read as the holder deleting them, and the result names
// the base it used beside the checkpoint. A holder on its checkpoint is diffed from it.
func TestCheckpointObserverDiffsFromTheHoldersBase(t *testing.T) {
	repo := testkit.NewRepo(t, map[string]string{"a.txt": "a\n"})
	root := repo.Root()
	base := repo.Head()
	commit := func(name string) string { return repo.Commit(name, map[string]string{name: name + "\n"}) }
	git := repo.Git
	git("checkout", "-q", "-b", "plan")
	checkpoint := commit("plan.txt")
	row := types.Job{ID: "w", Checkpoint: checkpoint, CheckoutRoot: root}
	observe := func() Observed {
		seen, err := CheckpointObserver(root, nil)(t.Context(), row)
		require.NoError(t, err)
		require.True(t, seen.ChangedKnown, "%+v", seen)
		return seen
	}

	git("checkout", "-q", "-b", "worker", base)
	head := commit("worker.txt")
	seen := observe()
	assert.Equal(t, []any{[]string{"worker.txt"}, base + " (the merge base of the holder's HEAD " + head + " and its checkpoint " + checkpoint + ")"},
		[]any{seen.Changed, seen.ChangedFrom}, "plan.txt is the checkpoint's, not the holder's")

	git("checkout", "-q", "plan")
	commit("on-plan.txt")
	seen = observe()
	assert.Equal(t, []any{[]string{"on-plan.txt"}, checkpoint}, []any{seen.Changed, seen.ChangedFrom}, "a holder on its checkpoint is diffed from it")
}

// With no merge base to read, the diff is taken from the base the holder reported, and
// with none of that either, from the checkpoint.
func TestDiffBaseFallsBackToTheReportedBase(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, reported     string
		head               error
		mergeBase          error
		wantRev, wantNamed string
	}{
		{
			"no head", "def5678+00ff", errors.New("no revision"), nil, "def5678",
			"def5678 (the base its holder reported, not its checkpoint abc1234; no merge base: the holder's HEAD could not be read: no revision)",
		},
		{
			"no merge base", "def5678", nil, &types.VCSUnsupportedError{VCS: "hg", Capability: types.CapTreeMerger}, "def5678",
			"def5678 (the base its holder reported, not its checkpoint abc1234; no merge base: " +
				(&types.VCSUnsupportedError{VCS: "hg", Capability: types.CapTreeMerger}).Error() + ")",
		},
		{"no reported base", "", errors.New("no revision"), nil, "abc1234", "abc1234 (its checkpoint; no merge base: the holder's HEAD could not be read: no revision)"},
		{"a reported base on the checkpoint", "abc1234+00ff", errors.New("no revision"), nil, "abc1234", "abc1234 (its checkpoint; no merge base: the holder's HEAD could not be read: no revision)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			driver := mocks.NewMockVCSDriver(t)
			driver.EXPECT().FindCommit(mock.Anything, "/repo", "").Return(types.Commit{ID: "head1"}, tc.head)
			if tc.head == nil {
				driver.EXPECT().MergeBase(mock.Anything, "/repo", "head1", "abc1234").Return("", false, tc.mergeBase)
			}

			got := diffBase(t.Context(), driver, "/repo", types.Job{Checkpoint: "abc1234+deadbeef", ReportedBase: tc.reported})

			assert.Equal(t, diffFrom{revision: tc.wantRev, named: tc.wantNamed}, got)
		})
	}
}

func TestVerifyGatesCarriesTheFootprintAndGradesNothingOnIt(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "unit", Created: 1, WritePaths: []string{"api"}, Check: &types.LeaseCheck{Target: "go-test", Project: "api"}}
	regions := []types.RegionChange{{File: types.FileChange{Path: "api/x.go"}, Side: types.RegionNew, Lines: [2]int{1, 3}, Declaration: "func Y() {"}}
	rep := types.JobResult{Job: "unit", ChangedPaths: []string{"api/x.go"}}
	base := Observed{Changed: []string{"api/x.go"}, ChangedKnown: true, ChangedFrom: "abc1234"}
	withRegions := base
	withRegions.Regions, withRegions.RegionsKnown = regions, true

	without := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, base)
	with := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, withRegions)

	assert.Equal(t, regions, with.Footprint)
	assert.True(t, with.FootprintKnown)
	assert.Equal(t, []int{3, 0}, []int{with.LinesAdded, with.LinesDeleted})
	with.Footprint, with.FootprintKnown, with.LinesAdded = nil, false, 0
	assert.Equal(t, without, with, "the footprint is reported and changes no verdict or violation")

	declined := base
	declined.RegionsReason = "git does not report changed regions (RegionReporter)"
	status := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, declined)
	assert.False(t, status.FootprintKnown)
	assert.Equal(t, "git does not report changed regions (RegionReporter)", status.FootprintReason)
}

// The symbol delta and the net lines are reports: they land in the status sorted, an
// export a goal of the job or an ancestor names is declared, and the verdict is the one the
// same diff reaches without them.
func TestVerifyGatesReportsTheSymbolDeltaAndGradesNothingOnIt(t *testing.T) {
	t.Parallel()

	row := types.Job{ID: "unit", Created: 1, WritePaths: []string{"api"}, Check: &types.LeaseCheck{Target: "go-test", Project: "api"},
		Goals: []types.Goal{
			{ID: "new", Kind: types.GoalKindSymbol, Expect: types.ExpectPresent, Symbols: []string{"Declared"}},
			{ID: "root/field", Kind: types.GoalKindSymbol, Expect: types.ExpectPresent, Symbols: []string{"Goal#Signature"}},
		}}
	rep := types.JobResult{Job: "unit", ChangedPaths: []string{"api/x.go"}}
	base := Observed{Changed: []string{"api/x.go"}, ChangedKnown: true, ChangedFrom: "abc1234", RegionsKnown: true,
		Regions: []types.RegionChange{
			{File: types.FileChange{Path: "api/x.go"}, Side: types.RegionNew, Lines: [2]int{4, 9}},
			{File: types.FileChange{Path: "api/x.go"}, Side: types.RegionOld, Lines: [2]int{2, 3}},
		}}
	delta := base
	delta.AddedExports = []string{"Zeta", "Goal#Signature", "Declared", "Alpha"}
	delta.LeftUnreferenced = []string{"old (api/y.go:3)", "helper (api/x.go:9)"}
	delta.SymbolDeltaKnown = true

	without := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, base)
	with := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, delta)

	assert.Equal(t, []int{6, 2}, []int{with.LinesAdded, with.LinesDeleted})
	wantWith := without
	wantWith.UndeclaredExports = []string{"Alpha", "Zeta"}
	wantWith.LeftUnreferenced = []string{"helper (api/x.go:9)", "old (api/y.go:3)"}
	wantWith.SymbolDeltaKnown = true
	assert.Equal(t, wantWith, with, "the delta changes no verdict or violation")
}

// overlapFixture is two checkouts of one repository, job a taken in root and job b in
// other.
type overlapFixture struct {
	root, other string
	driver      *mocks.MockVCSDriver
	rows        []types.Job
}

func newOverlapFixture(t *testing.T) overlapFixture {
	t.Helper()
	f := overlapFixture{root: t.TempDir(), other: t.TempDir(), driver: mocks.NewMockVCSDriver(t)}
	f.rows = []types.Job{
		{ID: "a", Checkpoint: "reva", WritePaths: []string{"api"}, State: types.StateRunning, CheckoutRoot: f.root},
		{ID: "b", Checkpoint: "revb+digest", WritePaths: []string{"api/x.go"}, State: types.StateRunning, CheckoutRoot: f.other},
	}
	return f
}

// region is a region git placed in decl, or, with no decl, one no driver could place.
func region(path, decl string, side types.RegionSide) types.RegionChange {
	r := types.RegionChange{File: types.FileChange{Path: path}, Side: side, Lines: [2]int{1, 2}, Declaration: decl}
	if decl != "" {
		r.Driver = "golang"
	}
	return r
}

func TestOverlapFootprints(t *testing.T) {
	t.Parallel()

	// Each job's diff since its checkpoint, as ChangedFiles reports it and Regions is asked
	// to refine.
	changed := []string{"api/x.go", "api/y.go", "api/notes.txt"}
	files := []types.FileChange{{Path: "api/x.go"}, {Path: "api/y.go"}, {Path: "api/notes.txt"}}
	declined := &types.VCSUnsupportedError{VCS: "git", Capability: types.CapRegionReporter}
	for _, tc := range []struct {
		name       string
		a, b       []types.RegionChange
		regionsErr error
		untakenB   bool
		askA, askB bool
		want       types.JobOverlapFootprint
	}{
		{
			name: "disjoint",
			a:    []types.RegionChange{region("api/x.go", "func X() {", types.RegionNew)},
			b:    []types.RegionChange{region("api/x.go", "func Y() {", types.RegionNew)},
			askA: true, askB: true,
			want: types.JobOverlapFootprint{Verdict: types.FootprintDisjoint},
		},
		{
			name: "shared",
			a: []types.RegionChange{
				region("api/x.go", "func X() {", types.RegionNew),
				region("api/notes.txt", "", types.RegionNew),
				region("api/y.go", "func Z() {", types.RegionNew),
			},
			b: []types.RegionChange{
				region("api/y.go", "func Z() {", types.RegionNew),
				region("api/x.go", "func X() {", types.RegionNew),
				region("api/x.go", "func X() {", types.RegionNew),
				region("api/notes.txt", "", types.RegionNew),
			},
			askA: true, askB: true,
			want: types.JobOverlapFootprint{Verdict: types.FootprintShared, Shared: []string{"api/notes.txt", "api/x.go#func X() {", "api/y.go#func Z() {"}},
		},
		{
			name: "deletions from one declaration share it",
			a:    []types.RegionChange{region("api/x.go", "func X() {", types.RegionOld)},
			b:    []types.RegionChange{region("api/x.go", "func X() {", types.RegionOld)},
			askA: true, askB: true,
			want: types.JobOverlapFootprint{Verdict: types.FootprintShared, Shared: []string{"api/x.go#func X() {"}},
		},
		{
			name: "a deletion and an edit of one declaration share it",
			a:    []types.RegionChange{region("api/x.go", "func X() {", types.RegionOld)},
			b:    []types.RegionChange{region("api/x.go", "func X() {", types.RegionNew)},
			askA: true, askB: true,
			want: types.JobOverlapFootprint{Verdict: types.FootprintShared, Shared: []string{"api/x.go#func X() {"}},
		},
		{
			name: "a region no driver placed covers its whole file",
			a:    []types.RegionChange{region("api/x.go", "", types.RegionNew)},
			b: []types.RegionChange{
				region("api/x.go", "func Y() {", types.RegionNew),
				region("api/y.go", "func Z() {", types.RegionNew),
			},
			askA: true, askB: true,
			want: types.JobOverlapFootprint{Verdict: types.FootprintShared, Shared: []string{"api/x.go#func Y() {"}},
		},
		{
			name:     "checkout missing",
			a:        []types.RegionChange{region("api/x.go", "func X() {", types.RegionNew)},
			untakenB: true,
			askA:     true,
			want:     types.JobOverlapFootprint{Verdict: types.FootprintUnknown, Reason: "b: no checkout of this repository has taken b"},
		},
		{
			name:       "capability declined",
			regionsErr: declined,
			askA:       true, askB: true,
			want: types.JobOverlapFootprint{Verdict: types.FootprintUnknown,
				Reason: "a: git does not report changed regions (RegionReporter); b: git does not report changed regions (RegionReporter)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newOverlapFixture(t)
			if tc.untakenB {
				f.rows[1].CheckoutRoot = ""
			}
			// Each holder's branch has its checkpoint, so the merge base is the checkpoint.
			if tc.askA {
				f.driver.EXPECT().FindCommit(mock.Anything, f.root, "").Return(types.Commit{ID: "heada"}, nil)
				f.driver.EXPECT().MergeBase(mock.Anything, f.root, "heada", "reva").Return("reva", true, nil)
				f.driver.EXPECT().ChangedFiles(mock.Anything, f.root, "reva").Return(changed, nil)
				f.driver.EXPECT().Regions(mock.Anything, f.root, "reva", files).Return(tc.a, tc.regionsErr)
			}
			if tc.askB {
				f.driver.EXPECT().FindCommit(mock.Anything, f.other, "").Return(types.Commit{ID: "headb"}, nil)
				f.driver.EXPECT().MergeBase(mock.Anything, f.other, "headb", "revb").Return("revb", true, nil)
				f.driver.EXPECT().ChangedFiles(mock.Anything, f.other, "revb").Return(changed, nil)
				f.driver.EXPECT().Regions(mock.Anything, f.other, "revb", files).Return(tc.b, tc.regionsErr)
			}
			overlaps := []types.JobOverlap{{JobA: "a", JobB: "b", PathsA: []string{"api"}, PathsB: []string{"api/x.go"}}}

			got := OverlapFootprints(t.Context(), f.driver, f.rows, overlaps)

			assert.Equal(t, []types.JobOverlap{{
				JobA: "a", JobB: "b", PathsA: []string{"api"}, PathsB: []string{"api/x.go"}, Footprint: &tc.want,
			}}, got)
		})
	}
}

func TestOverlapFootprintsWithNoVCS(t *testing.T) {
	t.Parallel()

	rows := []types.Job{{ID: "a", Checkpoint: "reva", CheckoutRoot: "/repo"}, {ID: "b"}}
	overlaps := []types.JobOverlap{{JobA: "a", JobB: "b"}}
	got := OverlapFootprints(t.Context(), nil, rows, overlaps)

	assert.Equal(t, &types.JobOverlapFootprint{
		Verdict: types.FootprintUnknown,
		Reason:  "a: no version control answered here; b: b was declared without a checkpoint",
	}, got[0].Footprint)
	assert.Nil(t, overlaps[0].Footprint, "the caller's overlaps are not written through")
}

// MeasureOverlaps is the measurement every door reporting overlaps calls, so it is pinned
// against a real checkout: two jobs taken in one tree whose edit lands in one declaration
// share it, and a list with no overlaps reads no VCS at all.
func TestMeasureOverlaps(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n", "api/x.go": "package api\n\nfunc X() {\n\treturn\n}\n"})
	root := repo.Root()
	rev := repo.Head()
	require.NoError(t, os.WriteFile(filepath.Join(root, "api", "x.go"), []byte("package api\n\nfunc X() {\n\tpanic(1)\n}\n"), 0o644))
	rows := []types.Job{
		{ID: "a", State: types.StateRunning, Checkpoint: rev, CheckoutRoot: root, WritePaths: []string{"api"}},
		{ID: "b", State: types.StateRunning, Checkpoint: rev, CheckoutRoot: root, WritePaths: []string{"api/x.go"}},
	}
	overlaps := types.NewJobList(rows).Overlaps
	require.Len(t, overlaps, 1)

	got := MeasureOverlaps(t.Context(), root, rows, overlaps)

	require.NotNil(t, got[0].Footprint)
	assert.Equal(t, &types.JobOverlapFootprint{
		Verdict: types.FootprintShared,
		Shared:  []string{"api/x.go#func X() {"},
	}, got[0].Footprint, got[0].Footprint.Reason)
	assert.Empty(t, MeasureOverlaps(t.Context(), "/nonexistent", rows, nil))
}

// Report measures the footprint of an overlap between two rows still being worked and
// leaves one with a stale row unmeasured, saying why, in the order the overlaps were
// derived: each footprint reads its holders' diffs, and on a store of abandoned rows
// measuring them all held `magus ls jobs` for minutes.
func TestReportSkipsFootprintsOfStaleRows(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n", "api/x.go": "package api\n\nfunc X() {\n\treturn\n}\n"})
	root := repo.Root()
	rev := repo.Head()
	require.NoError(t, os.WriteFile(filepath.Join(root, "api", "x.go"), []byte("package api\n\nfunc X() {\n\tpanic(1)\n}\n"), 0o644))
	const now, hour = int64(100_000), int64(3600)
	s, _ := sweepStore(t, now, 2*time.Hour)
	s.root = root
	held := func(id, path string, updated int64) types.Job {
		return types.Job{ID: id, State: types.StateRunning, Checkpoint: rev, CheckoutRoot: root, WritePaths: []string{path},
			Registered: updated, Created: updated, Updated: updated}
	}
	plant(t, s, held("old", "api", now-3*hour), held("a", "api", now), held("b", "api/x.go", now))

	list, err := s.Report(t.Context(), now, 2*time.Hour)
	require.NoError(t, err)

	type got struct {
		A, B    string
		Verdict string
		Reason  string
	}
	var overlaps []got
	for _, o := range list.Overlaps {
		require.NotNil(t, o.Footprint, "%s and %s", o.JobA, o.JobB)
		overlaps = append(overlaps, got{o.JobA, o.JobB, o.Footprint.Verdict, o.Footprint.Reason})
	}
	assert.Equal(t, []got{
		{"old", "a", types.FootprintUnknown, "not measured: old or a is stale (jobs.stale_after is 2h0m0s)"},
		{"old", "b", types.FootprintUnknown, "not measured: old or b is stale (jobs.stale_after is 2h0m0s)"},
		{"a", "b", types.FootprintShared, ""},
	}, overlaps)
}

// countingDriver counts the calls a footprint read makes of the driver it wraps.
type countingDriver struct {
	types.VCSDriver
	mu    sync.Mutex
	calls map[string]int
}

func (d *countingDriver) count(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.calls == nil {
		d.calls = map[string]int{}
	}
	d.calls[name]++
}

// take returns the calls counted since the last take.
func (d *countingDriver) take() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	calls := d.calls
	d.calls = nil
	return calls
}

func (d *countingDriver) FindCommit(ctx context.Context, dir, rev string) (types.Commit, error) {
	d.count("FindCommit")
	return d.VCSDriver.FindCommit(ctx, dir, rev)
}

func (d *countingDriver) MergeBase(ctx context.Context, root, a, b string) (string, bool, error) {
	d.count("MergeBase")
	return d.VCSDriver.MergeBase(ctx, root, a, b)
}

func (d *countingDriver) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	d.count("ChangedFiles")
	return d.VCSDriver.ChangedFiles(ctx, dir, base)
}

func (d *countingDriver) Regions(ctx context.Context, root, base string, files []types.FileChange) ([]types.RegionChange, error) {
	d.count("Regions")
	return d.VCSDriver.Regions(ctx, root, base, files)
}

func (d *countingDriver) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	d.count("DirtyFiles")
	return d.VCSDriver.DirtyFiles(ctx, dir, paths)
}

func (d *countingDriver) DirtyDiff(ctx context.Context, dir string, paths []string) (string, error) {
	d.count("DirtyDiff")
	return d.VCSDriver.DirtyDiff(ctx, dir, paths)
}

func (d *countingDriver) RegionsConfig(ctx context.Context, root string) (string, error) {
	d.count("RegionsConfig")
	return d.VCSDriver.(regionsConfigReader).RegionsConfig(ctx, root)
}

// gitDriver is a counting driver over root's real git.
func gitDriver(t testing.TB, root string) *countingDriver {
	t.Helper()
	res, err := vcs.Resolve(context.Background(), root, "", types.VCSOptions{})
	require.NoError(t, err)
	return &countingDriver{VCSDriver: res.VCS}
}

const twoFuncs = "package api\n\nfunc X() {\n\treturn\n}\n\nfunc Y() {\n\treturn\n}\n"

// overlapCheckouts is two checkouts of one repository at its first commit, job a taken in
// the first and b in the second, both writing api/x.go.
func overlapCheckouts(t testing.TB) (a, b *testkit.Repo, rows []types.Job, overlaps []types.JobOverlap) {
	t.Helper()
	a = testkit.NewRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n", "api/x.go": twoFuncs})
	b = a.AddWorktree("b")
	rev := a.Head()
	rows = []types.Job{
		{ID: "a", State: types.StateRunning, Checkpoint: rev, CheckoutRoot: a.Root(), WritePaths: []string{"api/x.go"}, Updated: 1},
		{ID: "b", State: types.StateRunning, Checkpoint: rev, CheckoutRoot: b.Root(), WritePaths: []string{"api/x.go"}, Updated: 1},
	}
	overlaps = types.NewJobList(rows).Overlaps
	require.Len(t, overlaps, 1)
	return a, b, rows, overlaps
}

// An overlap read again over unchanged checkouts reuses each footprint: it probes each
// checkout's HEAD and uncommitted state and diffs nothing. Any change to a checkout's files
// is a miss, and every read, cold or warm, says what a read with no cache says.
func TestOverlapFootprintsReuseAnUnchangedCheckout(t *testing.T) {
	t.Parallel()

	a, b, rows, overlaps := overlapCheckouts(t)
	a.Write("api/x.go", strings.Replace(twoFuncs, "func X() {\n\treturn", "func X() {\n\tpanic(1)", 1))
	b.Write("api/x.go", strings.Replace(twoFuncs, "func Y() {\n\treturn", "func Y() {\n\tpanic(2)", 1))
	driver := gitDriver(t, a.Root())
	ctx := withFootprintCache(t.Context(), footprintCache{dir: t.TempDir(), build: testBuild})
	uncached := func() []types.JobOverlap {
		got := OverlapFootprints(t.Context(), driver, rows, overlaps)
		driver.take()
		return got
	}

	want := uncached()
	assert.Equal(t, types.FootprintDisjoint, want[0].Footprint.Verdict, want[0].Footprint.Reason)
	assert.Equal(t, want, OverlapFootprints(ctx, driver, rows, overlaps), "cold")
	driver.take()
	assert.Equal(t, want, OverlapFootprints(ctx, driver, rows, overlaps), "warm")
	assert.Equal(t, map[string]int{"FindCommit": 2, "DirtyFiles": 2, "DirtyDiff": 2, "RegionsConfig": 2}, driver.take(),
		"a warm read probes each checkout and diffs nothing")

	b.Write("api/x.go", strings.Replace(twoFuncs, "return", "panic(3)", 2))
	want = uncached()
	assert.Equal(t, types.FootprintShared, want[0].Footprint.Verdict)
	assert.Equal(t, want, OverlapFootprints(ctx, driver, rows, overlaps), "an edit is a miss")
	assert.Positive(t, driver.take()["ChangedFiles"])

	b.Git("commit", "-q", "-am", "b's work")
	want = uncached()
	assert.Equal(t, want, OverlapFootprints(ctx, driver, rows, overlaps), "a commit is a miss")
	assert.Positive(t, driver.take()["ChangedFiles"])

	rows = slices.Clone(rows)
	rows[1].Updated++
	want = uncached()
	assert.Equal(t, want, OverlapFootprints(ctx, driver, rows, overlaps), "a row written again is a miss")
	assert.Positive(t, driver.take()["ChangedFiles"])
}

// Placement reads the repository's funcname patterns, so an edit to one is a miss for a
// row's whole footprint and for the regions of unchanged files alike.
func TestFootprintCacheMissesOnAnEditedFuncname(t *testing.T) {
	t.Parallel()

	a, _, rows, overlaps := overlapCheckouts(t)
	root, rev := a.Root(), a.Head()
	a.Write("api/x.go", strings.Replace(twoFuncs, "func X() {\n\treturn", "func X() {\n\tpanic(1)", 1))
	driver := gitDriver(t, root)
	dir := t.TempDir()
	regionsCtx := withFootprintCache(t.Context(), footprintCache{dir: dir, build: testBuild}.forRow(types.Job{Checkpoint: rev}))
	footprintCtx := withFootprintCache(t.Context(), footprintCache{dir: dir, build: testBuild})
	regions := func(ctx context.Context) Observed { return changedSince(ctx, driver, "", root, rev) }

	before := regions(t.Context())
	require.True(t, before.RegionsKnown, before.RegionsReason)
	beforeOverlap := OverlapFootprints(t.Context(), driver, rows, overlaps)
	assert.Equal(t, before, regions(regionsCtx), "cold")
	assert.Equal(t, beforeOverlap, OverlapFootprints(footprintCtx, driver, rows, overlaps), "cold")
	driver.take()
	assert.Equal(t, before, regions(regionsCtx), "warm")
	OverlapFootprints(footprintCtx, driver, rows, overlaps)
	require.Zero(t, driver.take()["Regions"], "both reads are warm before the edit")

	a.Git("config", "diff.golang.xfuncname", "^func Y.*$")
	after := regions(t.Context())
	require.NotEqual(t, before.Regions, after.Regions, "the pattern places the edit elsewhere")
	afterOverlap := OverlapFootprints(t.Context(), driver, rows, overlaps)
	driver.take()
	assert.Equal(t, after, regions(regionsCtx), "the regions of an unchanged file")
	assert.Equal(t, 1, driver.take()["Regions"])
	assert.Equal(t, afterOverlap, OverlapFootprints(footprintCtx, driver, rows, overlaps), "a row's footprint")
	assert.Positive(t, driver.take()["ChangedFiles"])
}

// Grading reads a dry-run store's footprint cache and writes none, as its report does.
func TestGradeGatesOfADryRunStoreCachesNothing(t *testing.T) {
	t.Parallel()

	for _, dryRun := range []bool{true, false} {
		a, _, rows, _ := overlapCheckouts(t)
		a.Write("api/x.go", strings.Replace(twoFuncs, "return", "panic(1)", 1))
		const now = int64(100_000)
		s, _ := sweepStore(t, now, 2*time.Hour)
		s.root = a.Root()
		for i := range rows {
			rows[i].Registered, rows[i].Created, rows[i].Updated = now, now, now
		}
		plant(t, s, rows...)
		s.dryRun, s.build = dryRun, testBuild

		status, err := GradeGates(t.Context(), s, "a", CheckpointObserver(a.Root(), nil))
		require.NoError(t, err)
		assert.True(t, status.FootprintKnown, status.FootprintReason)
		_, err = os.Stat(footprintsDir(s.path))
		assert.Equal(t, dryRun, errors.Is(err, os.ErrNotExist), "dry run %v", dryRun)
	}
}

const testBuild = "build-a"

// BenchmarkFootprintKeyHash is the cost of starting one cache key, which every lookup pays:
// the build is folded in, never re-read from the executable.
func BenchmarkFootprintKeyHash(b *testing.B) {
	c := footprintCache{dir: "footprints", revision: "abc123", build: testBuild}
	for b.Loop() {
		if _, ok := c.keyHash("footprint", "/checkout", "abc123"); !ok {
			b.Fatal("keyed nothing")
		}
	}
}

// A key carries the build that wrote it, so a rebuilt binary under a long-lived daemon
// misses; a Store that cannot name its build keys nothing.
func TestFootprintCacheKeysOnTheBuild(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, map[string]string{"a.go": twoFuncs})
	driver := gitDriver(t, repo.Root())
	row := types.Job{ID: "a", Checkpoint: repo.Head(), CheckoutRoot: repo.Root()}
	files := []types.FileChange{{Path: "a.go"}}
	keys := func(build string) ([2]string, bool) {
		cache := footprintCache{dir: t.TempDir(), build: build}.forRow(row)
		regions, regionsOK := cache.regionsKey(t.Context(), driver, repo.Root(), repo.Head(), files)
		footprint, footprintOK := cache.footprintKey(t.Context(), driver, row)
		require.Equal(t, regionsOK, footprintOK)
		return [2]string{regions, footprint}, regionsOK
	}

	a, ok := keys(testBuild)
	require.True(t, ok)
	again, _ := keys(testBuild)
	assert.Equal(t, a, again, "the same build keys the same")
	b, ok := keys("build-b")
	require.True(t, ok)
	assert.NotEqual(t, a[0], b[0], "regions")
	assert.NotEqual(t, a[1], b[1], "footprint")
	_, ok = keys("")
	assert.False(t, ok, "no build, no key")
}

// A backend that cannot say what configuration its placement reads is never cached: an
// edit to that configuration would answer from an entry placed under the old one.
func TestFootprintCacheKeysNothingForABackendThatHidesItsConfig(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, map[string]string{"a.go": twoFuncs})
	hidden := struct{ types.VCSDriver }{gitDriver(t, repo.Root()).VCSDriver}
	cache := footprintCache{dir: t.TempDir(), build: testBuild}.forRow(types.Job{Checkpoint: repo.Head()})
	_, keyed := cache.regionsKey(t.Context(), hidden, repo.Root(), repo.Head(), []types.FileChange{{Path: "a.go"}})
	assert.False(t, keyed)
	_, keyed = cache.footprintKey(t.Context(), hidden, types.Job{ID: "a", Checkpoint: repo.Head(), CheckoutRoot: repo.Root()})
	assert.False(t, keyed)
}

// The attribute files outside the tree pick each file's diff driver, so an edit to either
// one is a miss for the regions and the footprint alike.
func TestFootprintCacheMissesOnAnEditedAttributeFileOutsideTheTree(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, map[string]string{"a.go": twoFuncs})
	global := filepath.Join(t.TempDir(), "attributes")
	repo.Git("config", "core.attributesFile", global)
	driver := gitDriver(t, repo.Root())
	cache := footprintCache{dir: t.TempDir(), build: testBuild}.forRow(types.Job{Checkpoint: repo.Head()})
	row := types.Job{ID: "a", Checkpoint: repo.Head(), CheckoutRoot: repo.Root()}
	keys := func() [2]string {
		regions, keyed := cache.regionsKey(t.Context(), driver, repo.Root(), repo.Head(), []types.FileChange{{Path: "a.go"}})
		require.True(t, keyed)
		footprint, keyed := cache.footprintKey(t.Context(), driver, row)
		require.True(t, keyed)
		return [2]string{regions, footprint}
	}

	before := keys()
	assert.Equal(t, before, keys(), "unchanged")
	require.NoError(t, os.MkdirAll(filepath.Join(repo.Root(), ".git", "info"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo.Root(), ".git", "info", "attributes"), []byte("*.go -diff\n"), 0o644))
	local := keys()
	assert.NotEqual(t, before[0], local[0], "info/attributes, regions")
	assert.NotEqual(t, before[1], local[1], "info/attributes, footprint")
	require.NoError(t, os.WriteFile(global, []byte("*.go -diff\n"), 0o644))
	written := keys()
	assert.NotEqual(t, local[0], written[0], "core.attributesFile, regions")
	assert.NotEqual(t, local[1], written[1], "core.attributesFile, footprint")
}

// The regions a diff placed are reused while every changed file holds the content they were
// read from, and read again once one does not.
func TestRegionsSinceReuseUnchangedFiles(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n", "api/x.go": twoFuncs})
	root, rev := repo.Root(), repo.Head()
	repo.Write("api/x.go", strings.Replace(twoFuncs, "func X() {\n\treturn", "func X() {\n\tpanic(1)", 1))
	driver := gitDriver(t, root)
	ctx := withFootprintCache(t.Context(), footprintCache{dir: t.TempDir(), build: testBuild}.forRow(types.Job{Checkpoint: rev}))
	read := func(ctx context.Context) Observed { return changedSince(ctx, driver, "", root, rev) }

	for _, edit := range []string{"", "func Y() {\n\tpanic(2)"} {
		if edit != "" {
			repo.Write("api/x.go", strings.Replace(twoFuncs, "func Y() {\n\treturn", edit, 1))
		}
		want := read(t.Context())
		require.True(t, want.RegionsKnown, want.RegionsReason)
		driver.take()
		assert.Equal(t, want, read(ctx), "cold")
		assert.Equal(t, 1, driver.take()["Regions"])
		assert.Equal(t, want, read(ctx), "warm")
		assert.Zero(t, driver.take()["Regions"], "a warm read places no line")
	}
}

// A cache entry that will not decode, or that answers to another key, is a miss.
func TestFootprintCacheMissesOnABadEntry(t *testing.T) {
	t.Parallel()

	cache := footprintCache{dir: t.TempDir(), revision: "abc123"}
	regions := []types.RegionChange{region("a.go", "func X() {", types.RegionNew)}
	cache.write("k1", regions)
	got, ok := cache.read("k1")
	require.True(t, ok)
	assert.Equal(t, regions, got)

	require.NoError(t, os.WriteFile(cache.entryPath("k1"), []byte("{not json"), 0o644))
	_, ok = cache.read("k1")
	assert.False(t, ok, "unreadable")

	cache.write("k2", regions)
	require.NoError(t, os.Rename(cache.entryPath("k2"), cache.entryPath("k1")))
	_, ok = cache.read("k1")
	assert.False(t, ok, "another key's entry")

	readOnly := cache
	readOnly.readOnly = true
	readOnly.write("k3", regions)
	_, ok = cache.read("k3")
	assert.False(t, ok, "a read-only cache writes nothing")
}

// A dry-run store's report measures as any other and caches nothing.
func TestReportOfADryRunStoreCachesNothing(t *testing.T) {
	t.Parallel()

	for _, dryRun := range []bool{true, false} {
		a, _, rows, _ := overlapCheckouts(t)
		a.Write("api/x.go", strings.Replace(twoFuncs, "return", "panic(1)", 1))
		const now = int64(100_000)
		s, _ := sweepStore(t, now, 2*time.Hour)
		s.root = a.Root()
		for i := range rows {
			rows[i].Registered, rows[i].Created, rows[i].Updated = now, now, now
		}
		plant(t, s, rows...)
		s.dryRun, s.build = dryRun, testBuild

		list, err := s.Report(t.Context(), now, 2*time.Hour)
		require.NoError(t, err)

		require.Len(t, list.Overlaps, 1)
		assert.NotEqual(t, types.FootprintUnknown, list.Overlaps[0].Footprint.Verdict, list.Overlaps[0].Footprint.Reason)
		_, err = os.Stat(footprintsDir(s.path))
		assert.Equal(t, dryRun, errors.Is(err, os.ErrNotExist), "dry run %v", dryRun)
	}
}

// The retention pass drops the cache of every checkpoint no live row declares.
func TestRetentionPrunesFootprintsNoLiveRowNames(t *testing.T) {
	t.Parallel()

	s := tmpStore(t, t.TempDir())
	keep := time.Duration(0)
	s.retain = &keep
	dir := footprintsDir(s.path)
	for _, rev := range []string{"live", "over", "gone"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, revisionDir(rev), "k"), 0o755))
	}
	rows := []types.Job{
		{ID: "a", State: types.StateRunning, Checkpoint: "live+0123", Updated: 1},
		{ID: "b", State: types.StatePass, Checkpoint: "over", Updated: 1},
	}
	// Retained long ago, so this write runs a pass.
	require.NoError(t, s.update(t.Context(), func(f *jobsFile) error { f.Jobs, f.Retained = rows, 1; return nil }))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	assert.Equal(t, []string{"live"}, left)
}

// benchFiles is how many Go files each benchmark checkout has edited, each in two of its
// declarations: a worker's diff, not a one-line fix.
const benchFiles = 12

// editedCheckouts is overlapCheckouts with benchFiles files edited in each checkout, and the
// changed files of the first.
func editedCheckouts(b *testing.B) (rows []types.Job, overlaps []types.JobOverlap, root, rev string, files []types.FileChange) {
	b.Helper()
	a, other, rows, _ := overlapCheckouts(b)
	tree := map[string]string{}
	for i := range benchFiles {
		tree[fmt.Sprintf("api/f%d.go", i)] = twoFuncs
	}
	rev = a.Commit("files", tree)
	other.Git("merge", "-q", "--ff-only", rev)
	for i := range benchFiles {
		p := fmt.Sprintf("api/f%d.go", i)
		a.Write(p, strings.Replace(twoFuncs, "return", "panic(1)", 2))
		other.Write(p, strings.Replace(twoFuncs, "func Y() {\n\treturn", "func Y() {\n\tpanic(2)", 1))
		files = append(files, types.FileChange{Path: p})
	}
	for i := range rows {
		rows[i].Checkpoint = rev
		rows[i].WritePaths = []string{"api/*.go"}
	}
	return rows, types.NewJobList(rows).Overlaps, a.Root(), rev, files
}

// BenchmarkRegionsSinceWarm places the changed lines of benchFiles edited files: cold reads
// them as every read did before the cache, warm answers from the cache. calls/op is the
// driver calls one read makes.
func BenchmarkRegionsSinceWarm(b *testing.B) {
	rows, _, root, rev, files := editedCheckouts(b)
	driver := gitDriver(b, root)
	cache := withFootprintCache(b.Context(), footprintCache{dir: b.TempDir(), build: testBuild}.forRow(rows[0]))
	if _, ok, why := regionsSince(cache, driver, root, rev, files); !ok {
		b.Fatal(why)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{{"cold", b.Context()}, {"warm", cache}} {
		b.Run(tc.name, func(b *testing.B) {
			driver.take()
			for b.Loop() {
				if _, ok, why := regionsSince(tc.ctx, driver, root, rev, files); !ok {
					b.Fatal(why)
				}
			}
			reportCalls(b, driver.take())
		})
	}
}

// BenchmarkOverlapFootprintsWarm measures one overlap between two checkouts that each edited
// benchFiles files, as `magus ls jobs` does: cold diffs both as every read did before the
// cache, warm probes each checkout and reuses its footprint.
func BenchmarkOverlapFootprintsWarm(b *testing.B) {
	rows, overlaps, root, _, _ := editedCheckouts(b)
	require.Len(b, overlaps, 1)
	driver := gitDriver(b, root)
	cache := withFootprintCache(b.Context(), footprintCache{dir: b.TempDir(), build: testBuild})
	if got := OverlapFootprints(cache, driver, rows, overlaps); got[0].Footprint.Verdict == types.FootprintUnknown {
		b.Fatal(got[0].Footprint.Reason)
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{{"cold", b.Context()}, {"warm", cache}} {
		b.Run(tc.name, func(b *testing.B) {
			driver.take()
			for b.Loop() {
				OverlapFootprints(tc.ctx, driver, rows, overlaps)
			}
			reportCalls(b, driver.take())
		})
	}
}

func reportCalls(b *testing.B, calls map[string]int) {
	total := 0
	for _, n := range calls {
		total += n
	}
	b.ReportMetric(float64(total)/float64(b.N), "calls/op")
}
