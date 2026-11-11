package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const configMagusfile = `import "magus";

magus\project({
    "name": "demo",
    "targets": {
        "test": {"timeout": "5m"},
    },
});

export fun build(ctx: magus\Context, args: [str]) > void {
}
`

// configFiles is a workspace of two projects under the buzz and yaml diff drivers, with a
// Buzz file no project loads and a magus.yaml.
var configFiles = map[string]string{
	".gitattributes":          "*.buzz diff=buzz\n*.yaml diff=yaml\n",
	"magus.yaml":              "jobs:\n  stale_after: 2h\n",
	"magusfile.buzz":          configMagusfile,
	"libs/lib/magusfile.buzz": "import \"magus\";\n\nmagus\\project({\n    \"name\": \"lib\",\n});\n",
	"notes.buzz":              "fun note() > void {\n}\n",
}

func TestRefuseWholeFileConfigClaims(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepository(t, configFiles).Root()
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"the root magusfile whole", []string{"magusfile.buzz"},
			"instead, such as `magusfile.buzz#project.name`, `magusfile.buzz#project.targets.test`, `magusfile.buzz#build`"},
		{"a project's magusfile whole", []string{"libs/lib/magusfile.buzz"}, "\"libs/lib/magusfile.buzz\" holds a project's configuration whole"},
		{"a key claim", []string{"magusfile.buzz#project.targets.test"}, ""},
		{"a key not written yet", []string{"magusfile.buzz#project.targets.lint"}, ""},
		{"a pattern", []string{"**/*.buzz"}, ""},
		{"magus.yaml whole", []string{"magus.yaml"}, "instead, such as `magus.yaml#jobs.stale_after`"},
		{"a magus.yaml key claim", []string{"magus.yaml#jobs.stale_after"}, ""},
		{"a Buzz file no project loads", []string{"notes.buzz"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fork := NewStore(tempLocation(t, root))
			_, forkErr := ForkMerge(t.Context(), fork, "wave/job", func(u *types.Job) {
				specify(u)
				u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, tc.paths
			}, config.Jobs{}, nil)
			apply := NewStore(tempLocation(t, root))
			_, applyErr := Apply(t.Context(), apply, []types.Declaration{{Criteria: testCriteria, Model: testModel, ID: "wave/job", WritePaths: tc.paths, Check: forkCheck()}}, config.Jobs{}, nil, nil, false)
			for door, err := range map[string]error{"fork": forkErr, "apply": applyErr} {
				if tc.want == "" {
					require.NoError(t, err, door)
					continue
				}
				require.ErrorIs(t, err, types.WritePathConfigWholeFile, door)
				assert.Contains(t, err.Error(), tc.want, door)
			}
			if tc.want != "" {
				for _, s := range []*Store{fork, apply} {
					rows, err := s.List()
					require.NoError(t, err)
					assert.Empty(t, rows, "a refused fork writes no row")
				}
			}
		})
	}

	t.Run("an apply adding it to a live job", func(t *testing.T) {
		t.Parallel()
		s := NewStore(tempLocation(t, root))
		seed(t, s, types.Job{ID: "wave/job", State: types.StateRunning, WritePaths: []string{"notes.buzz"}, Check: forkCheck()})
		_, err := Apply(t.Context(), s, []types.Declaration{{Criteria: testCriteria, Model: testModel, ID: "wave/job", WritePaths: []string{"notes.buzz", "magusfile.buzz"}, Check: forkCheck()}}, config.Jobs{}, nil, nil, false)
		require.ErrorIs(t, err, types.WritePathConfigWholeFile)
	})

	t.Run("a checkout that places no line of either", func(t *testing.T) {
		t.Parallel()
		bare := testkit.NewRepository(t, map[string]string{"magusfile.buzz": configMagusfile, "magus.yaml": "jobs:\n  stale_after: 2h\n"}).Root()
		assert.Empty(t, wholeFileConfigClaimProblems(t.Context(), NewStore(tempLocation(t, bare)), types.Job{ID: "wave/job", WritePaths: []string{"magusfile.buzz", "magus.yaml"}}))
	})

	t.Run("a magus.yaml that declares nothing yet", func(t *testing.T) {
		t.Parallel()
		empty := testkit.NewRepository(t, map[string]string{".gitattributes": "*.yaml diff=yaml\n", "magus.yaml": "{}\n"}).Root()
		assert.Empty(t, wholeFileConfigClaimProblems(t.Context(), NewStore(tempLocation(t, empty)), types.Job{ID: "wave/job", WritePaths: []string{"magus.yaml"}}))
	})
}

// narrowHoldSuite pins NarrowHold over a workspace where cfg holds every Buzz file by a
// pattern and lit holds notes.buzz by name.
type narrowHoldSuite struct {
	suite.Suite
	root string
	loc  Location
	s    *Store
}

func TestNarrowHold(t *testing.T) { suite.Run(t, new(narrowHoldSuite)) }

func (s *narrowHoldSuite) SetupTest() {
	s.root = testkit.NewRepository(s.T(), configFiles).Root()
	s.loc = tempLocation(s.T(), s.root)
	s.s = NewStore(s.loc)
	seed(s.T(), s.s, types.Job{ID: "cfg", State: types.StateRunning, WritePaths: []string{"**/*.buzz"}, Check: forkCheck()})
	seed(s.T(), s.s, types.Job{ID: "lit", State: types.StateRunning, WritePaths: []string{"notes.buzz"}, Check: forkCheck()})
}

func (s *narrowHoldSuite) narrow(id, file string, touched ...string) (types.Job, []string, error) {
	return NarrowHold(s.T().Context(), s.s, id, file, touched, "")
}

func (s *narrowHoldSuite) stored(id string) types.Job {
	row, err := s.s.stored(id)
	s.Require().NoError(err)
	return row
}

func (s *narrowHoldSuite) TestAPatternHoldNarrowsToTheTouchedDeclarations() {
	peer := types.Job{ID: "peer", State: types.StateDeclared, WritePaths: []string{"magusfile.buzz#project.name"}}
	rows, err := s.s.List()
	s.Require().NoError(err)
	s.Equal(types.WriteProofOverlapping, s.s.WriteProof(rows, peer), "the pattern covers the magusfile whole")

	// The holder narrows its own row: the claims sit inside what its pattern covered.
	got, added, err := NarrowHold(s.T().Context(), boundStore(s.loc, "cfg"), "cfg", "magusfile.buzz", []string{"project.targets.test", types.Preamble}, "")
	s.Require().NoError(err)
	body, err := os.ReadFile(filepath.Join(s.root, "magusfile.buzz"))
	s.Require().NoError(err)
	sum := sha256.Sum256(body)
	s.Equal([]string{"magusfile.buzz#project.targets.test"}, added)
	s.Equal([]string{"**/*.buzz", "magusfile.buzz#project.targets.test"}, got.WritePaths)
	s.Require().Len(got.Releases, 1)
	s.NotZero(got.Releases[0].ReleasedAt)
	s.Equal(types.JobRelease{Path: "magusfile.buzz", Digest: "sha256:" + hex.EncodeToString(sum[:]), ReleasedAt: got.Releases[0].ReleasedAt, Narrowed: true},
		got.Releases[0])
	s.Equal([]string{"project.targets.test"}, ClaimedDeclarations(got, "magusfile.buzz"))
	s.Nil(ClaimedDeclarations(got, "libs/lib/magusfile.buzz"), "the pattern still covers every other file whole")
	s.Zero(got.Widenings, "a narrowing widens nothing")

	rows, err = s.s.List()
	s.Require().NoError(err)
	s.Equal(types.WriteProofDisjoint, s.s.WriteProof(rows, peer), "a narrowed file is out of the pattern's reach")
}

func (s *narrowHoldSuite) TestALaterCallGrowsTheClaims() {
	_, _, err := s.narrow("cfg", "magusfile.buzz", "project.targets.test")
	s.Require().NoError(err)

	again, added, err := s.narrow("cfg", "magusfile.buzz", "project.targets.test", "export fun build(ctx: magus\\Context, args: [str]) > void {")
	s.Require().NoError(err)
	s.Equal([]string{"magusfile.buzz#build"}, added)
	s.Equal([]string{"**/*.buzz", "magusfile.buzz#project.targets.test", "magusfile.buzz#build"}, again.WritePaths)
	s.Len(again.Releases, 1)
}

func (s *narrowHoldSuite) TestARepeatWritesNothing() {
	first, _, err := s.narrow("cfg", "magusfile.buzz", "project.targets.test")
	s.Require().NoError(err)

	again, added, err := s.narrow("cfg", "magusfile.buzz", "project.targets.test")
	s.Require().NoError(err)
	s.Empty(added)
	s.Equal(first, again)
	s.Equal(first, s.stored("cfg"))
}

func (s *narrowHoldSuite) TestALiteralEntryNarrowsAndStays() {
	got, added, err := s.narrow("lit", "notes.buzz", "fun note() > void {")
	s.Require().NoError(err)

	s.Equal([]string{"notes.buzz#note"}, added)
	s.Equal([]string{"notes.buzz", "notes.buzz#note"}, got.WritePaths, "the root's entry stays")
	s.True(Narrowed(got, "notes.buzz"))
	s.Equal([]string{"note"}, ClaimedDeclarations(got, "notes.buzz"))
	s.True(releasedReach(got, "notes.buzz", "notes.buzz"))
}

func (s *narrowHoldSuite) TestANarrowedHoldWithNoClaimHoldsNothingWhole() {
	got, added, err := s.narrow("lit", "notes.buzz", types.Preamble)
	s.Require().NoError(err)

	s.Empty(added)
	s.NotNil(ClaimedDeclarations(got, "notes.buzz"), "an empty claim set, not a whole-file hold")
	s.Empty(ClaimedDeclarations(got, "notes.buzz"))
}

func (s *narrowHoldSuite) TestAGrantRecordsItsFork() {
	got, _, err := NarrowHold(s.T().Context(), s.s, "lit", "notes.buzz", []string{"fun note() > void {"}, "fork")
	s.Require().NoError(err)
	s.Equal("fork", got.Releases[0].TakenBy)

	grown, _, err := s.narrow("lit", "notes.buzz", "fun note() > void {")
	s.Require().NoError(err)
	s.Equal("fork", grown.Releases[0].TakenBy, "the holder's own growth keeps the grant on record")
}

func (s *narrowHoldSuite) TestADeclarationAnotherRowClaimsIsRefused() {
	seed(s.T(), s.s, types.Job{ID: "keys", State: types.StateRunning, WritePaths: []string{"magusfile.buzz#project.name"}, Check: forkCheck()})

	_, _, err := s.narrow("cfg", "magusfile.buzz", "project.targets.test", "project.name")
	s.Require().Error(err)
	s.Contains(err.Error(), "magusfile.buzz#project.name is lease keys's claim")
	s.Equal([]string{"**/*.buzz"}, s.stored("cfg").WritePaths, "a refused narrowing adds nothing")
	s.Empty(s.stored("cfg").Releases)
}

func (s *narrowHoldSuite) TestAFileNoDriverPlacesIsRefused() {
	seed(s.T(), s.s, types.Job{ID: "txt", State: types.StateRunning, WritePaths: []string{"notes.txt"}, Check: forkCheck()})

	_, _, err := s.narrow("txt", "notes.txt", "anything")
	s.Require().Error(err)
	s.Contains(err.Error(), "no diff driver places the declarations of notes.txt, so its hold stays whole")
	s.Empty(s.stored("txt").Releases)
}

func (s *narrowHoldSuite) TestARowHoldingNoWholeEntryIsRefused() {
	seed(s.T(), s.s, types.Job{ID: "keys", State: types.StateRunning, WritePaths: []string{"magusfile.buzz#project.name"}, Check: forkCheck()})

	_, _, err := s.narrow("keys", "magusfile.buzz", "project.targets.test")
	s.Require().Error(err)
	s.Contains(err.Error(), "no whole-file write path of keys covers magusfile.buzz")
}

func (s *narrowHoldSuite) TestAnUnknownRowIsRefused() {
	_, _, err := s.narrow("gone", "magusfile.buzz", "project.name")
	s.Require().Error(err)
	s.Contains(err.Error(), "unknown lease")
}

func (s *narrowHoldSuite) TestARedeclarationKeepsTheNarrowing() {
	_, _, err := s.narrow("lit", "notes.buzz", "fun note() > void {")
	s.Require().NoError(err)

	applied, err := Apply(s.T().Context(), s.s, []types.Declaration{{Criteria: testCriteria, Model: testModel, ID: "lit", WritePaths: []string{"notes.buzz", "notes.buzz#note"}, Check: forkCheck()}}, config.Jobs{}, nil, nil, false)
	s.Require().NoError(err)
	s.True(Narrowed(applied[0].Next, "notes.buzz"))
}

func (s *narrowHoldSuite) TestAReforkEndsTheNarrowing() {
	narrowed := types.Job{ID: "lit", State: types.StatePass, WritePaths: []string{"notes.buzz", "notes.buzz#note"},
		Releases: []types.JobRelease{{Path: "notes.buzz", Narrowed: true, ReleasedAt: 1}}}
	next := narrowed.Clone()
	next.State = types.StateDeclared

	s.Empty(s.s.releases(s.T().Context(), narrowed, next, 2, false))
}

func (s *narrowHoldSuite) TestNamingAPatternedFileAgainEndsTheNarrowing() {
	narrowed := types.Job{ID: "cfg", State: types.StateRunning, WritePaths: []string{"**/*.buzz", "magusfile.buzz#build"},
		Releases: []types.JobRelease{{Path: "magusfile.buzz", Narrowed: true, ReleasedAt: 1}}}
	next := narrowed.Clone()
	next.WritePaths = append(next.WritePaths, "magusfile.buzz")

	s.Empty(s.s.releases(s.T().Context(), narrowed, next, 2, false))
}

// releaseUnderPatternSuite pins a record naming again the glob a narrowing took
// a file out of.
type releaseUnderPatternSuite struct {
	suite.Suite
}

func TestReleaseUnderPattern(t *testing.T) { suite.Run(t, new(releaseUnderPatternSuite)) }

func (s *releaseUnderPatternSuite) applied(write ...string) types.Job {
	loc := tempLocation(s.T(), testkit.NewRepository(s.T(), configFiles).Root())
	store := NewStore(loc)
	seed(s.T(), store, types.Job{ID: "cfg", Criteria: "goal", State: types.StateRunning, WritePaths: []string{"**/*.buzz"}, Check: forkCheck()})
	_, _, err := NarrowHold(s.T().Context(), boundStore(loc, "cfg"), "cfg", "magusfile.buzz", []string{"project.targets.test"}, "")
	s.Require().NoError(err)
	applied, err := Apply(s.T().Context(), store, []types.Declaration{{ID: "cfg", Criteria: "goal", WritePaths: write, Check: forkCheck()}}, config.Jobs{}, nil, nil, false)
	s.Require().NoError(err)
	return applied[0].Next
}

func (s *releaseUnderPatternSuite) TestAPatternAloneLeavesTheFileHeldByNothing() {
	row := s.applied("**/*.buzz")

	s.True(releasedFile(row, "magusfile.buzz"), "a record naming the glob does not take the file back")
	s.Empty(claimsOnFile(row, "magusfile.buzz"))
	s.Equal([]string{"magusfile.buzz stays released, so the pattern naming it does not hold it and neither does a claim: this job cannot write it until write_paths name `magusfile.buzz#<declaration>`"},
		ReleasedUnderPattern(row))
}

func (s *releaseUnderPatternSuite) TestAKeptClaimIsNamedAsTheHold() {
	row := s.applied("**/*.buzz", "magusfile.buzz#project.targets.test")

	s.Equal([]string{"magusfile.buzz stays released, so the pattern naming it does not hold it: only its claims do (project.targets.test)"},
		ReleasedUnderPattern(row))
}

func (s *releaseUnderPatternSuite) TestAReleasedFileNoPatternNamesIsNotNoted() {
	s.Empty(ReleasedUnderPattern(types.Job{WritePaths: []string{"a.go"}, Releases: []types.JobRelease{{Path: "b.go"}}}))
}

// A fork refused over a magusfile another live job holds files one release request per
// holder and file, however often it is refused; a dry run files none.
func TestConfigRefusalRequestsOneRelease(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepository(t, configFiles).Root()
	elsewhere := testkit.NewRepository(t, configFiles)
	s := NewStore(tempLocation(t, root))
	seed(t, s, types.Job{ID: "cfg", State: types.StateRunning, WritePaths: []string{"**/*.buzz"}, Checkpoint: elsewhere.Head(), CheckoutRoot: elsewhere.Root()})
	fork := func(id string, paths ...string) error {
		_, err := ForkMerge(t.Context(), s, id, func(u *types.Job) {
			specify(u)
			u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, paths
		}, config.Jobs{}, nil)
		return err
	}

	_, err := Apply(t.Context(), s, []types.Declaration{{Criteria: testCriteria, Model: testModel, ID: "slots", WritePaths: []string{"magusfile.buzz"}, Check: forkCheck()}}, config.Jobs{}, nil, nil, true)
	require.ErrorIs(t, err, types.WritePathConfigWholeFile)
	assert.Empty(t, releaseRequests(t, root), "a dry run is refused the same way and files nothing")

	for range 2 {
		require.ErrorIs(t, fork("slots", "magusfile.buzz"), types.WritePathConfigWholeFile)
	}
	requests := releaseRequests(t, root)
	require.Len(t, requests, 1)
	assert.Equal(t, string(types.OutcomeWaiting), requests[0].Outcome)
	assert.Equal(t, "magusfile.buzz", requests[0].Where)
	assert.Equal(t, []types.FileRef{{Value: "magusfile.buzz"}}, requests[0].Files)
	assert.Contains(t, requests[0].Message, "job cfg holds magusfile.buzz")
	assert.Contains(t, requests[0].Message, "cfg has changed nothing under **/*.buzz", "the request lists the holder's unused paths")
	assert.Contains(t, requests[0].Message, "magus job apply -f <record>")
	assert.Contains(t, requests[0].Message, "magus session dispose")

	// MGS3032: a holder in another checkout already claims the key the fork wants.
	seed(t, s, types.Job{ID: "keys", State: types.StateRunning, WritePaths: []string{"magusfile.buzz#project.name"}, Checkpoint: elsewhere.Head(), CheckoutRoot: elsewhere.Root()})
	require.ErrorIs(t, fork("name", "magusfile.buzz#project.name"), types.WritePathFileShared)
	requests = releaseRequests(t, root)
	require.Len(t, requests, 2)
	i := slices.IndexFunc(requests, func(r sessions.AttentionRequest) bool { return r.Files[0].Value != "magusfile.buzz" })
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, []types.FileRef{{Value: "magusfile.buzz#project.name"}}, requests[i].Files)
	assert.Contains(t, requests[i].Message, "job keys holds magusfile.buzz")
	assert.Contains(t, requests[i].Message, "keys has changed nothing under magusfile.buzz#project.name")
	assert.Len(t, storedRows(t, s)["keys"].ReleaseRequests, 1, "the row carries what the guard reads")
	require.ErrorIs(t, fork("name", "magusfile.buzz#project.name"), types.WritePathFileShared)
	assert.Len(t, storedRows(t, s)["keys"].ReleaseRequests, 1, "a repeat files nothing")
}

// ReleaseRequestRowSuite pins what keeps and clears a request on a row.
type ReleaseRequestRowSuite struct {
	suite.Suite
	store *Store
}

func TestReleaseRequestRowSuite(t *testing.T) {
	suite.Run(t, new(ReleaseRequestRowSuite))
}

func (s *ReleaseRequestRowSuite) SetupTest() {
	s.store = NewStore(declared(s.T(), types.Job{ID: "h", State: types.StateRunning, WritePaths: []string{"a.go", "b.go"}}))
	filed, err := s.store.FileReleaseRequest(s.T().Context(), "h", types.JobReleaseRequest{ID: "att-1", File: "a.go", Wanted: []string{"a.go"}, Message: "wanted"})
	s.Require().NoError(err)
	s.Require().True(filed)
}

func (s *ReleaseRequestRowSuite) requests() []types.JobReleaseRequest {
	return storedRows(s.T(), s.store)["h"].ReleaseRequests
}

// Filing appends without reading the store: a second open of an id shows the
// fold was not asked, and the queue still holds one row.
func (s *ReleaseRequestRowSuite) TestAttentionIsFiledWithoutReadingTheStore() {
	ctx := s.T().Context()
	s.store.requestRelease(ctx, "h", "b.go", []string{"b.go"}, nil)
	queue := releaseRequests(s.T(), s.store.root)
	s.Require().Len(queue, 1)
	s.Require().NoError(s.store.CloseReleaseRequest(ctx, queue[0].ID))
	s.store.requestRelease(ctx, "h", "b.go", []string{"b.go"}, nil)

	dir, err := sessions.Dir(s.store.root)
	s.Require().NoError(err)
	fold, err := sessions.ReadAll(dir)
	s.Require().NoError(err)
	opens := slices.DeleteFunc(slices.Clone(fold.Records), func(r sessions.Record) bool { return r.Kind != sessions.KindAttentionOpen })
	s.Len(opens, 2)
	s.Len(releaseRequests(s.T(), s.store.root), 1)
}

func (s *ReleaseRequestRowSuite) TestTheStoreStampsTheTime() {
	s.Positive(s.requests()[0].At)
}

func (s *ReleaseRequestRowSuite) TestTheSameIDIsNotFiledTwice() {
	filed, err := s.store.FileReleaseRequest(s.T().Context(), "h", types.JobReleaseRequest{ID: "att-1", File: "a.go"})
	s.Require().NoError(err)
	s.False(filed)
	s.Len(s.requests(), 1)
}

func (s *ReleaseRequestRowSuite) TestAnUnknownHolderIsAnError() {
	_, err := s.store.FileReleaseRequest(s.T().Context(), "nobody", types.JobReleaseRequest{ID: "att-2", File: "a.go"})
	s.ErrorIs(err, ErrUnknownJob)
}

func (s *ReleaseRequestRowSuite) TestAWriteToTheRowKeepsIt() {
	_, err := s.store.Update(s.T().Context(), "h", func(cur *types.Job) { cur.Criteria = "changed" })
	s.Require().NoError(err)
	s.Len(s.requests(), 1)
}

func (s *ReleaseRequestRowSuite) TestNarrowingPastTheFileClearsIt() {
	_, err := s.store.Update(s.T().Context(), "h", func(cur *types.Job) { cur.WritePaths = []string{"b.go"} })
	s.Require().NoError(err)
	s.Empty(s.requests())
}

func (s *ReleaseRequestRowSuite) TestClosingDropsIt() {
	s.Require().NoError(s.store.CloseReleaseRequest(s.T().Context(), "att-1"))
	s.Empty(s.requests())
}

func (s *ReleaseRequestRowSuite) TestDisposingAnotherSourceLeavesIt() {
	req := sessions.AttentionRequest{ID: "att-1", Source: "another/source"}
	s.Require().NoError(ReleaseRequestDisposed(s.T().Context(), s.T().TempDir(), req))
	s.Len(s.requests(), 1)
}

// releaseRequests are the open release requests filed for the repository at root.
func releaseRequests(t *testing.T, root string) []sessions.AttentionRequest {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	return slices.DeleteFunc(sessions.AttentionQueue(fold), func(r sessions.AttentionRequest) bool { return r.Source != releaseRequestSource })
}

func (s *exitEvidenceSuite) TestOwedForAnyReasonButNoSeatIsRefused() {
	row := s.row()
	result := passingResult()
	result.Validation = types.JobResultValidation{Command: row.Validation, OutputRef: "out1"}
	result.VerificationEvidence = []types.VerificationEvidence{{GateID: "lint", Owed: "generated drift"}}

	_, err := s.exit(row, result, types.JobAttempt{Target: "go-test"})

	s.ErrorContains(err, `goal "lint" is filed as owed because "generated drift", and the reasons a run is owed are "no seat" and "remote CI runs it"`)
	s.ErrorContains(err, "the integrator regenerates")
}

func (s *exitEvidenceSuite) TestOwedCheckForAnyReasonButNoSeatIsRefused() {
	row := s.row()
	result := passingResult()
	result.Validation = types.JobResultValidation{Command: row.Validation, Owed: "not this row's target"}
	result.VerificationEvidence = []types.VerificationEvidence{{GateID: "lint", Owed: types.OwedNoSeat}}

	_, err := s.exit(row, result, types.JobAttempt{Target: "go-test"})

	s.ErrorContains(err, `goal "check" is filed as owed because "not this row's target"`)
}

func (s *exitEvidenceSuite) TestNarrowedLintOverTheClaimedFilesIsFiled() {
	row := s.row()
	loc := declared(s.T(), row)
	for _, f := range []string{"cmd/magus/ledger.go", "internal/ledger/report.go"} {
		full := filepath.Join(loc.Root, filepath.FromSlash(f))
		s.Require().NoError(os.MkdirAll(filepath.Dir(full), 0o755))
		s.Require().NoError(os.WriteFile(full, []byte("package x\n"), 0o644))
	}
	result := passingResult()
	result.Validation = types.JobResultValidation{Command: row.Validation, Owed: types.OwedNoSeat}
	result.VerificationEvidence = []types.VerificationEvidence{{GateID: "lint", OutputRef: "out2"}}
	exit := func(args ...string) (types.Job, error) {
		resolve := func(_ context.Context, ref string) (types.JobAttempt, error) {
			return types.JobAttempt{Found: true, Ref: ref, Project: ".", Target: "lint", ExtraArgs: args, TimestampMs: 9_999_999_999_999}, nil
		}
		return Exit(s.T().Context(), boundStore(loc, row.ID).InWorkspace(goCheckWorkspace(".")), row.ID, &result, resolve)
	}

	_, err := exit("cmd/magus/ledger.go")
	s.ErrorContains(err, "internal/ledger/report.go", "exit names the changed file the run left out")

	exited, err := exit("cmd/magus/ledger.go", "internal/ledger/report.go")
	s.Require().NoError(err)
	s.Equal(types.StateExited, exited.State)
}

func TestLintGoalLeavesOutTheTestsAForkAddedByItself(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	merge := func(u *types.Job) {
		u.WritePaths, u.SoftPaths, u.Check = []string{"cli/registry.go", "cli/registry_test.go"}, []string{"cli/registry_test.go"}, forkCheck()
	}
	merged, err := resolveMerge(t.Context(), s, nil, "w", merge, nil)
	require.NoError(t, err)
	var row types.Job
	merged(&row)

	i := slices.IndexFunc(row.Goals, func(g types.Goal) bool { return g.ID == "lint" })
	require.GreaterOrEqual(t, i, 0, "goals: %v", row.Goals)
	assert.Equal(t, []string{"cli/registry.go"}, row.Goals[i].Check.Args, "lint reads a soft path once the row changes it")
	assert.Contains(t, row.WritePaths, "cli/registry_test.go", "the worker may still write it")
}
