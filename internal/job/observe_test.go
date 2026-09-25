package job

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/gen/mocks"
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
		CompletionGates: []types.CompletionGate{
			{ID: "migration", Kind: types.GateKindPaths, Paths: []string{"db/migrations/**"}},
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
	with.Footprint, with.FootprintKnown = nil, false
	assert.Equal(t, without, with, "the footprint is reported and changes no verdict or violation")

	declined := base
	declined.RegionsReason = "git does not report changed regions (RegionReporter)"
	status := VerifyGates(row, rep, types.JobAttempt{}, nil, []types.Job{row}, declined)
	assert.False(t, status.FootprintKnown)
	assert.Equal(t, "git does not report changed regions (RegionReporter)", status.FootprintReason)
}

// overlapFixture is two checkouts of one repository, root binding job a and other binding
// job b, each marker in its checkout's own .magus.
type overlapFixture struct {
	root, other string
	driver      *mocks.MockVCSDriver
	rows        []types.Job
}

func newOverlapFixture(t *testing.T) overlapFixture {
	t.Helper()
	f := overlapFixture{root: t.TempDir(), other: t.TempDir(), driver: mocks.NewMockVCSDriver(t)}
	bind := func(dir, id string) {
		cacheDir := filepath.Join(dir, ".magus")
		require.NoError(t, os.MkdirAll(cacheDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(cacheDir, LeaseMarkerName), []byte(id+"\n"), 0o644))
	}
	bind(f.root, "a")
	bind(f.other, "b")
	f.rows = []types.Job{
		{ID: "a", Checkpoint: "reva", WritePaths: []string{"api"}, State: types.StateRunning},
		{ID: "b", Checkpoint: "revb+digest", WritePaths: []string{"api/x.go"}, State: types.StateRunning},
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
		unbindB    bool
		listErr    error
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
			name:    "checkout missing",
			a:       []types.RegionChange{region("api/x.go", "func X() {", types.RegionNew)},
			unbindB: true,
			askA:    true,
			want:    types.JobOverlapFootprint{Verdict: types.FootprintUnknown, Reason: "b: no checkout of this repository is bound to b"},
		},
		{
			name:       "capability declined",
			regionsErr: declined,
			askA:       true, askB: true,
			want: types.JobOverlapFootprint{Verdict: types.FootprintUnknown,
				Reason: "a: git does not report changed regions (RegionReporter); b: git does not report changed regions (RegionReporter)"},
		},
		{
			name:    "checkouts unlistable",
			listErr: errors.New("not a repository"),
			want: types.JobOverlapFootprint{Verdict: types.FootprintUnknown,
				Reason: "a: the checkouts of this repository could not be listed: not a repository;" +
					" b: the checkouts of this repository could not be listed: not a repository"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newOverlapFixture(t)
			if tc.unbindB {
				require.NoError(t, os.Remove(filepath.Join(f.other, ".magus", LeaseMarkerName)))
			}
			f.driver.EXPECT().OtherCheckouts(f.root).Return([]string{f.other}, tc.listErr)
			if tc.askA {
				f.driver.EXPECT().ChangedFiles(mock.Anything, f.root, "reva").Return(changed, nil)
				f.driver.EXPECT().Regions(mock.Anything, f.root, "reva", files).Return(tc.a, tc.regionsErr)
			}
			if tc.askB {
				f.driver.EXPECT().ChangedFiles(mock.Anything, f.other, "revb").Return(changed, nil)
				f.driver.EXPECT().Regions(mock.Anything, f.other, "revb", files).Return(tc.b, tc.regionsErr)
			}
			overlaps := []types.JobOverlap{{JobA: "a", JobB: "b", PathsA: []string{"api"}, PathsB: []string{"api/x.go"}}}
			cacheDirOf := func(dir string) (string, error) { return filepath.Join(dir, ".magus"), nil }

			got := OverlapFootprints(t.Context(), f.driver, f.root, cacheDirOf, f.rows, overlaps)

			assert.Equal(t, []types.JobOverlap{{
				JobA: "a", JobB: "b", PathsA: []string{"api"}, PathsB: []string{"api/x.go"}, Footprint: &tc.want,
			}}, got)
		})
	}
}

// Checkouts sharing one cache dir bind every lease in both, and diffing whichever came first
// would compare the wrong tree.
func TestOverlapFootprintsRefusesALeaseTwoCheckoutsClaim(t *testing.T) {
	t.Parallel()

	f := newOverlapFixture(t)
	f.driver.EXPECT().OtherCheckouts(f.root).Return([]string{f.other}, nil)
	shared := filepath.Join(f.root, ".magus")
	overlaps := []types.JobOverlap{{JobA: "a", JobB: "b"}}

	got := OverlapFootprints(t.Context(), f.driver, f.root, func(string) (string, error) { return shared, nil }, f.rows, overlaps)

	assert.Equal(t, &types.JobOverlapFootprint{
		Verdict: types.FootprintUnknown,
		Reason:  "a: more than one checkout is bound to a; b: no checkout of this repository is bound to b",
	}, got[0].Footprint)
	assert.Nil(t, overlaps[0].Footprint, "the caller's overlaps are not written through")
}

func TestOverlapFootprintsWithNoVCS(t *testing.T) {
	t.Parallel()

	rows := []types.Job{{ID: "a", Checkpoint: "reva"}, {ID: "b"}}
	got := OverlapFootprints(t.Context(), nil, "/repo", nil, rows, []types.JobOverlap{{JobA: "a", JobB: "b"}})

	assert.Equal(t, &types.JobOverlapFootprint{
		Verdict: types.FootprintUnknown,
		Reason:  "a: no version control answered here; b: b was declared without a checkpoint",
	}, got[0].Footprint)
}
