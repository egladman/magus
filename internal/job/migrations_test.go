package job

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// numberedHeaderSuite pins the reading of a header that counts its migrations.
//
// compat: see migrate.Store.Count.
type numberedHeaderSuite struct {
	suite.Suite
}

func TestNumberedHeaderSuite(t *testing.T) { suite.Run(t, new(numberedHeaderSuite)) }

func (s *numberedHeaderSuite) TestACurrentCountBecomesTheStoreVersion() {
	body := fmt.Sprintf("{\"api_version\":%q,\"kind\":\"JobList\",\"migration\":%d}\n%s",
		job.APIVersion, len(jobMigrations()), recordLine("a", "Running", "Running", "", ""))
	st := tempStore(s.T(), s.T().TempDir())
	notices := &bytes.Buffer{}
	st.notices = notices
	path := plantRaw(s.T(), st, body)

	f, err := st.Read(s.T().Context())
	s.Require().NoError(err)
	s.Require().Len(f.Jobs, 1)
	header := storedLines(s.T(), path)[0]
	s.Equal(string(currentStoreVersion()), header["api_version"])
	s.InDelta(len(jobMigrations()), header["migration"], 0, "the count agrees with the version")
	s.Contains(notices.String(), fmt.Sprintf("from %s to %s (1 entries); read the header's \"migration\" count as its \"api_version\"",
		currentStoreVersion(), currentStoreVersion()))
}

// A magus built before store versions reads a header by its count alone, so a store this
// build writes must read to it as through every migration, or it would replay them all.
func (s *numberedHeaderSuite) TestAStoreThisBuildWritesCountsEveryMigration() {
	st := tempStore(s.T(), s.T().TempDir())
	seed(s.T(), st, lease("a"))
	path, err := st.Path()
	s.Require().NoError(err)

	header := storedLines(s.T(), path)[0]

	s.Equal(string(currentStoreVersion()), header["api_version"])
	s.InDelta(len(jobMigrations()), header["migration"], 0, "an older magus finds no migration due")
	f, err := st.Read(s.T().Context())
	s.Require().NoError(err)
	s.Len(f.Jobs, 1, "and this build reads it by its version, migrating nothing")
}

type modelTierSourceJobSuite struct {
	suite.Suite
}

func TestModelTierSourceJobSuite(t *testing.T) { suite.Run(t, new(modelTierSourceJobSuite)) }

func (s *modelTierSourceJobSuite) entry(text string) migrate.Entry {
	var e migrate.Entry
	s.Require().NoError(codec.Unmarshal([]byte(text), &e))
	return e
}

func (s *modelTierSourceJobSuite) source(e migrate.Entry) string {
	// The entry is at the migration this one reads, before keys went snake_case.
	var status map[string]codec.RawMessage
	s.Require().NoError(codec.Unmarshal(e["status"], &status))
	raw, ok := status["modelTier"]
	if !ok {
		return ""
	}
	var tier struct {
		Source string `json:"source"`
	}
	s.Require().NoError(codec.Unmarshal(raw, &tier))
	return tier.Source
}

func (s *modelTierSourceJobSuite) TestAStoredRowBecomesTheJobSpelling() {
	got, err := modelTierSourceJob(s.entry(`{"spec":{},"status":{"modelTier":{"name":"bounded","source":"Row"}}}`))

	s.Require().NoError(err)
	s.Equal("Job", s.source(got))
}

func (s *modelTierSourceJobSuite) TestAnotherSourceIsKept() {
	got, err := modelTierSourceJob(s.entry(`{"spec":{},"status":{"modelTier":{"source":"Matrix"}}}`))

	s.Require().NoError(err)
	s.Equal("Matrix", s.source(got))
}

func (s *modelTierSourceJobSuite) TestAnEntryWithNoStatusIsKept() {
	got, err := modelTierSourceJob(s.entry(`{"spec":{}}`))

	s.Require().NoError(err)
	s.NotContains(got, "status")
}

func (s *modelTierSourceJobSuite) TestAnEntryWithNoModelTierIsKept() {
	got, err := modelTierSourceJob(s.entry(`{"spec":{},"status":{"created":"2026-11-13T00:00:00Z"}}`))

	s.Require().NoError(err)
	s.Empty(s.source(got))
}

type pathsNestedSuite struct {
	suite.Suite
}

func TestPathsNestedSuite(t *testing.T) { suite.Run(t, new(pathsNestedSuite)) }

// spec is the spec pathsNested makes of a stored entry's spec.
func (s *pathsNestedSuite) spec(spec string) map[string]codec.RawMessage {
	var e migrate.Entry
	s.Require().NoError(codec.Unmarshal(fmt.Appendf(nil, `{"metadata":{"name":"a"},"spec":%s}`, spec), &e))
	got, err := pathsNested(e)
	s.Require().NoError(err)
	var out map[string]codec.RawMessage
	s.Require().NoError(codec.Unmarshal(got["spec"], &out))
	return out
}

func (s *pathsNestedSuite) TestWritePathsMoveToPathsWrite() {
	got := s.spec(`{"writePaths":["a.go"],"dependsOn":["b"]}`)

	s.JSONEq(`{"write":["a.go"]}`, string(got["paths"]))
	s.NotContains(got, "writePaths")
	s.JSONEq(`["b"]`, string(got["dependsOn"]))
}

func (s *pathsNestedSuite) TestReadPathsMoveToPathsRead() {
	got := s.spec(`{"readPaths":["docs"]}`)

	s.JSONEq(`{"read":["docs"]}`, string(got["paths"]))
	s.NotContains(got, "readPaths")
}

func (s *pathsNestedSuite) TestDenyPathsMoveToPathsDeny() {
	got := s.spec(`{"denyPaths":["gen/x.go"]}`)

	s.JSONEq(`{"deny":["gen/x.go"]}`, string(got["paths"]))
	s.NotContains(got, "denyPaths")
}

func (s *pathsNestedSuite) TestSoftPathsMoveToPathsSoft() {
	got := s.spec(`{"writePaths":["a_test.go"],"softPaths":["a_test.go"]}`)

	s.JSONEq(`{"write":["a_test.go"],"soft":["a_test.go"]}`, string(got["paths"]))
	s.NotContains(got, "softPaths")
}

func (s *pathsNestedSuite) TestARecordWithNoPathsIsKept() {
	got := s.spec(`{"readOnly":true}`)

	s.NotContains(got, "paths")
	s.JSONEq(`true`, string(got["readOnly"]))
}

func (s *pathsNestedSuite) TestTheHeaderLineIsKept() {
	var e migrate.Entry
	s.Require().NoError(codec.Unmarshal([]byte(`{"apiVersion":"batch.magus/v1alpha1","kind":"JobList"}`), &e))

	got, err := pathsNested(e)

	s.Require().NoError(err)
	s.NotContains(got, "spec")
}

// headerlessStoreSuite pins a store with no header line, which would replay every
// migration from 0.
type headerlessStoreSuite struct {
	suite.Suite
}

func TestHeaderlessStoreSuite(t *testing.T) { suite.Run(t, new(headerlessStoreSuite)) }

func (s *headerlessStoreSuite) TestAnEnvelopedRowIsRefusedAsDamagedAndNothingWritten() {
	st := tempStore(s.T(), s.T().TempDir())
	row := job.Job{ID: "a", State: job.StateRunning, Created: 1, Updated: 1, Paths: job.Paths{Write: []string{"a.go"}}}
	line, err := job.MarshalRecord(row, nil)
	s.Require().NoError(err)
	path := plantRaw(s.T(), st, fmt.Sprintf("%s\n%s", line, line))

	_, err = st.List(s.T().Context())

	headerless, ok := errors.AsType[*headerlessStoreError](err)
	s.Require().True(ok, "%v", err)
	s.Equal(1, headerless.Line)
	s.ErrorContains(err, fmt.Sprintf("job store %s has no header line, and line 1 is an enveloped row, which only a store at %s or later holds", path, envelopedAt()))
	s.ErrorContains(err, fmt.Sprintf("move it aside with `mv %s %s.damaged` and fork the jobs again", path, path))
	s.Equal(fmt.Sprintf("%s\n%s\n", line, line), string(s.read(path)), "nothing was written")
}

func (s *headerlessStoreSuite) TestAFlatRowMigratesFromZero() {
	st := tempStore(s.T(), s.T().TempDir())
	st.notices = &bytes.Buffer{}
	path := plantRaw(s.T(), st, `{"id":"a","state":"running","created":1,"updated":1}`)

	rows, err := st.List(s.T().Context())

	s.Require().NoError(err)
	s.Require().Len(rows, 1)
	stored := storedLines(s.T(), path)
	s.Require().Len(stored, 2, "the header the migration wrote, then the row")
	s.Equal(string(job.APIVersion), stored[1]["api_version"])
}

func (s *headerlessStoreSuite) read(path string) []byte {
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	return raw
}

// rootlessMigrationSuite pins the migration of a store opened with no root, which
// keys on the working directory. It changes directory, so it runs alone.
type rootlessMigrationSuite struct {
	suite.Suite
}

func TestRootlessMigrationSuite(t *testing.T) { suite.Run(t, new(rootlessMigrationSuite)) }

func (s *rootlessMigrationSuite) TestAWorkingDirectoryNoVersionControlHoldsMigrates() {
	wd := s.T().TempDir()
	s.T().Chdir(wd)
	st, path, notices := behindStore(s.T(), "")

	_, err := st.Read(s.T().Context())

	s.Require().NoError(err)
	s.Equal(string(storeVersion(1)), storedLines(s.T(), path)[0]["api_version"])
	s.Contains(notices.String(), "so no base judged this magus")
}

func (s *rootlessMigrationSuite) TestAWorkingDirectoryOnABranchIsRefused() {
	repo := testkit.NewRepository(s.T(), nil)
	repo.Git("update-ref", "refs/remotes/origin/main", repo.Head())
	branch := repo.Commit("branch", map[string]string{"b.txt": "b\n"})
	s.T().Chdir(repo.Root())
	st, _, _ := behindStore(s.T(), "")

	_, err := st.Read(types.WithMagusBuild(s.T().Context(), types.StampedBuild("v1", branch, "")))

	_, ok := errors.AsType[*offBaseMigrationError](err)
	s.True(ok, "%v", err)
}

// regroupSuite pins the move of a store's lines from the batch.magus group to the
// safetyscissors groups.
type regroupSuite struct {
	suite.Suite
	st             *Store
	jobs, attempts string
}

func TestRegroupSuite(t *testing.T) { suite.Run(t, new(regroupSuite)) }

// SetupTest writes a store at this build's migration, with one row and one attempt.
func (s *regroupSuite) SetupTest() {
	s.st = tempStore(s.T(), s.T().TempDir())
	seed(s.T(), s.st, running("a"))
	s.Require().NoError(s.st.fileResult(s.T().Context(), "a", &job.Result{Job: "a", ChangedPaths: []string{"a.go"}}, nil, 30))
	var err error
	s.jobs, err = s.st.Path()
	s.Require().NoError(err)
	s.attempts, err = s.st.AttemptsPath()
	s.Require().NoError(err)
}

// rewrite replaces old with new in the file at path.
func (s *regroupSuite) rewrite(path, old, new string) {
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Require().Contains(string(raw), old)
	s.Require().NoError(os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), old, new)), 0o600))
}

func (s *regroupSuite) read(path string) string {
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	return string(raw)
}

// before writes the store back as the migration before groupsMoved left it: in the
// batch.magus group, its api version under the camelCase key it had then, and its
// version counted as headers of that time counted it.
//
// compat: see migrate.Store.Count.
func (s *regroupSuite) before() {
	group := job.APIVersion.Group() + "/"
	at := slices.IndexFunc(jobMigrations(), func(m migrate.Migration) bool { return m.Name == groupsMoved })
	s.rewrite(s.jobs, group, batchMagusGroup+"/")
	s.rewrite(s.jobs, `"api_version":`, `"apiVersion":`)
	s.rewrite(s.jobs, fmt.Sprintf(`"migration":%d`, len(jobMigrations())), fmt.Sprintf(`"migration":%d`, at))
	s.rewrite(s.attempts, group, batchMagusGroup+"/")
	s.rewrite(s.attempts, `"api_version":`, `"apiVersion":`)
}

func (s *regroupSuite) TestARowTakesTheJobGroupAndItsAnnotations() {
	e, err := decodeEntry([]byte(`{"apiVersion":"batch.magus/v1alpha1","kind":"Job","metadata":{"name":"a","annotations":{"batch.magus/requires":"claim-declarations","other/key":"v"}},"spec":{}}`))
	s.Require().NoError(err)

	got, err := regroup(e)

	s.Require().NoError(err)
	text, err := codec.Marshal(got)
	s.Require().NoError(err)
	s.JSONEq(fmt.Sprintf(`{"apiVersion":%q,"kind":"Job","metadata":{"name":"a","annotations":{%q:"claim-declarations","other/key":"v"}},"spec":{}}`,
		job.APIVersion, job.APIVersion.Group()+"/requires"), string(text))
}

// regrouped is a stored line as [regroup] rewrites it.
func (s *regroupSuite) regrouped(text []byte) ([]byte, error) {
	e, err := decodeEntry(text)
	if err != nil {
		return nil, err
	}
	if e, err = regroup(e); err != nil {
		return nil, err
	}
	return codec.Marshal(e)
}

func (s *regroupSuite) TestAMigratedLineIsKeptByteForByte() {
	line := []byte(fmt.Sprintf(`{"apiVersion":%q,"kind":"Job","metadata":{"annotations":{%q:"x"},"name":"a"},"spec":{}}`,
		job.APIVersion, job.APIVersion.Group()+"/requires"))
	once, err := s.regrouped(line)
	s.Require().NoError(err)

	twice, err := s.regrouped(once)

	s.Require().NoError(err)
	s.JSONEq(string(line), string(once))
	s.Equal(string(once), string(twice))
}

func (s *regroupSuite) TestABatchMagusStoreReadsAfterItsLinesMove() {
	s.before()

	rows, err := s.st.Read(s.T().Context())

	s.Require().NoError(err)
	s.Len(rows.Jobs, 1)
	for _, path := range []string{s.jobs, s.attempts} {
		s.NotContains(s.read(path), batchMagusGroup+"/", path)
		s.Contains(s.read(path), string(job.APIVersion), path)
	}
	tries, err := s.st.Attempts(s.T().Context())
	s.Require().NoError(err)
	s.Len(tries, 1)
}

func (s *regroupSuite) TestAMigratedStoreIsNotRewrittenAgain() {
	s.before()
	_, err := s.st.Read(s.T().Context())
	s.Require().NoError(err)
	jobs, attempts := s.read(s.jobs), s.read(s.attempts)

	s.Require().NoError(s.st.regroupAttempts(s.T().Context()))
	_, err = s.st.Read(s.T().Context())

	s.Require().NoError(err)
	s.Equal(jobs, s.read(s.jobs))
	s.Equal(attempts, s.read(s.attempts))
}

func (s *regroupSuite) TestAForeignGroupIsRefusedAndNothingIsWritten() {
	s.rewrite(s.jobs, `"api_version":"`+string(job.APIVersion)+`","kind":"Job"`, `"api_version":"example.jobs/v1","kind":"Job"`)
	before := s.read(s.jobs)

	_, err := s.st.Read(s.T().Context())

	coded, ok := errors.AsType[*types.DiagnosticError](err)
	s.Require().True(ok, "%v", err)
	s.Equal(types.JobStoreRecordForeign, coded.Code)
	s.Contains(err.Error(), "fork the job again, or start a fresh session")
	s.Equal(before, s.read(s.jobs))
}

type jobRecordReadSuite struct {
	suite.Suite
}

func TestJobRecordRead(t *testing.T) { suite.Run(t, new(jobRecordReadSuite)) }

func (s *jobRecordReadSuite) TestAFlatRowSourceMigratesToJob() {
	st := tempStore(s.T(), s.T().TempDir())
	st.notices = &bytes.Buffer{}
	plantRaw(s.T(), st, `{"api_version":"safetyscissors.job/v1alpha1"}
{"id":"a","state":"running","created":1,"updated":1,"model_tier":{"name":"bounded","rank":1,"skills":"short","source":"row"}}`)

	f, err := st.Read(s.T().Context())

	s.Require().NoError(err)
	s.Require().Len(f.Jobs, 1)
	s.Equal(&tier.Assignment{Name: "bounded", Rank: 1, Skills: tier.SkillVariantShort, Source: tier.SourceJob}, f.Jobs[0].ModelTier)
}

// baseFenceSuite pins who may migrate the job store every checkout and branch of a
// repository shares: only a binary built on a commit the base contains.
type baseFenceSuite struct {
	suite.Suite
	repo *testkit.Repo
	// base is the commit origin/main names, and branch one past it on a branch.
	base, branch string
}

func TestBaseFenceSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(baseFenceSuite))
}

func (s *baseFenceSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n"})
	s.base = s.repo.Head()
	s.repo.Git("update-ref", "refs/remotes/origin/main", s.base)
	s.repo.Git("checkout", "-q", "-b", "feat")
	s.branch = s.repo.Commit("branch work", map[string]string{"b.txt": "b\n"})
}

// built is a context whose binary go-build stamped with version and commit.
func (s *baseFenceSuite) built(version, commit string) context.Context {
	return types.WithMagusBuild(s.T().Context(), types.StampedBuild(version, commit, ""))
}

// unstamped is a context whose binary carries no magus stamp, only Go's VCS stamp of
// commit, modified or not; commit "" carries none either.
func (s *baseFenceSuite) unstamped(commit string, modified bool) context.Context {
	b := types.MagusBuild{Version: "unknown", Commit: "unknown"}
	if commit != "" {
		b.Commit, b.Source, b.Dirty = commit, types.BuildSourceVCS, modified
	}
	return types.WithMagusBuild(s.T().Context(), b)
}

// refused asserts a read and a write of the store at path under ctx are refused as off
// the base for why, and that nothing was written.
func (s *baseFenceSuite) refused(ctx context.Context, st *Store, path, why string) *offBaseMigrationError {
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)

	_, err = st.Read(ctx)

	off, ok := errors.AsType[*offBaseMigrationError](err)
	s.Require().True(ok, "%v", err)
	s.Contains(err.Error(), why)
	s.Contains(err.Error(), "rebuild magus from origin/main, or run this binary under its own XDG_STATE_HOME")
	_, err = st.Update(ctx, "b", func(cur *job.Job) { *cur = lease("b") })
	s.Require().ErrorAs(err, &off, "a write refuses it too")
	after, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Equal(string(raw), string(after), "nothing was written")
	return off
}

func (s *baseFenceSuite) TestABinaryBuiltOnTheBaseMigratesAndStampsItself() {
	st, path, _ := behindStore(s.T(), s.repo.Root())

	f, err := st.Read(s.built("v0.4.3", s.base))

	s.Require().NoError(err)
	s.Equal("ship it", f.Jobs[0].Parent)
	header := storedLines(s.T(), path)[0]
	s.Equal(string(storeVersion(1)), header["api_version"])
	s.Equal(map[string]any{"version": "v0.4.3", "commit": s.base}, header["writer"])
}

func (s *baseFenceSuite) TestABranchBinaryIsRefused() {
	st, path, notices := behindStore(s.T(), s.repo.Root())

	off := s.refused(s.built("v0.4.3-1-gfeat", s.branch), st, path,
		fmt.Sprintf("origin/main at %s does not contain commit %.12s, which built it", s.repo.Git("rev-parse", "--short", s.base), s.branch))

	s.Equal(&offBaseMigrationError{
		Path: path, Found: "no api_version", Known: storeVersion(1), Build: types.StampedBuild("v0.4.3-1-gfeat", s.branch, ""), Base: "origin/main",
		Why: fmt.Sprintf("origin/main at %s does not contain commit %.12s, which built it", s.repo.Git("rev-parse", "--short", s.base), s.branch),
	}, off)
	s.Contains(off.Error(), "(its header names no writer")
	s.Empty(notices.String())
}

func (s *baseFenceSuite) TestTheRefusalNamesTheLastWriter() {
	st, path, _ := behindStore(s.T(), s.repo.Root())
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)
	stamped := strings.Replace(string(raw), `"kind":"JobList"`, `"kind":"JobList","writer":{"version":"v0.4.2","commit":"0123456789abcdef"}`, 1)
	s.Require().NoError(os.WriteFile(path, []byte(stamped), 0o644))

	s.refused(s.built("v0.4.3-1-gfeat", s.branch), st, path, "magus v0.4.2 (commit 0123456789ab) wrote it last")
}

func (s *baseFenceSuite) TestABinaryWithNoBuildCommitIsRefused() {
	st, path, _ := behindStore(s.T(), s.repo.Root())

	s.refused(s.built("v0.4.3", "unknown"), st, path, "this binary's build identity could not be read")
}

func (s *baseFenceSuite) TestAStampedBinaryBuiltFromAModifiedTreeIsRefused() {
	st, path, _ := behindStore(s.T(), s.repo.Root())

	s.refused(s.built("v0.4.3-dirty", s.base), st, path, "this binary was built from a modified tree (its version v0.4.3-dirty says so)")
}

// Go's VCS stamp of a build in a linked worktree can say modified for a clean tree, so it
// never speaks for a binary go-build stamped.
func (s *baseFenceSuite) TestAStampedCleanBinaryIgnoresTheVCSStamp() {
	st, path, notices := behindStore(s.T(), s.repo.Root())

	_, err := st.Read(s.built("v0.5.0-rc.3-205-g"+s.base[:9], s.base))

	s.Require().NoError(err)
	s.Equal(string(storeVersion(1)), storedLines(s.T(), path)[0]["api_version"])
	s.NotContains(notices.String(), "VCS stamp")
}

func (s *baseFenceSuite) TestAnUnstampedBinaryOnTheBaseMigratesAndSaysItFellBack() {
	st, path, notices := behindStore(s.T(), s.repo.Root())

	_, err := st.Read(s.unstamped(s.base, false))

	s.Require().NoError(err)
	s.Equal(string(storeVersion(1)), storedLines(s.T(), path)[0]["api_version"])
	s.Contains(notices.String(), fmt.Sprintf("job: this magus carries no magus version stamp, so its build commit %.12s was read from Go's VCS stamp before it migrated job store %s\n", s.base, path))
}

func (s *baseFenceSuite) TestAnUnstampedModifiedBinaryIsRefusedAndSaysItFellBack() {
	st, path, _ := behindStore(s.T(), s.repo.Root())

	off := s.refused(s.unstamped(s.base, true), st, path, "this binary carries no magus version stamp, and Go's VCS stamp says it was built from a modified tree")

	s.Contains(off.Error(), "read from Go's VCS stamp because it carries no magus version stamp")
	s.Contains(off.Error(), "./magus run go-build .")
}

func (s *baseFenceSuite) TestABinaryWithNoStampAtAllIsRefusedNamingTheBuild() {
	st, path, _ := behindStore(s.T(), s.repo.Root())

	off := s.refused(s.unstamped("", false), st, path, "this binary's build identity could not be read: it carries no magus version stamp and no Go VCS stamp")

	s.Contains(off.Error(), "build magus with `./magus run go-build .` or `./magus buzz hack/dev/bootstrap-checkout.buzz -- --here`")
	s.NotContains(off.Error(), "modified tree")
}

func (s *baseFenceSuite) TestABaseThatDoesNotResolveIsRefused() {
	s.repo.Git("update-ref", "-d", "refs/remotes/origin/main")
	st, path, _ := behindStore(s.T(), s.repo.Root())

	s.refused(s.built("v0.4.3", s.base), st, path, "origin/main does not resolve in "+s.repo.Root())
}

// A job's own checkout migrates like any other once its binary is on the base: the
// refusal is the binary's, not the checkout's.
func (s *baseFenceSuite) TestALinkedCheckoutOnTheBaseMigrates() {
	wt := s.repo.AddWorktree("job")
	st, path, _ := behindStore(s.T(), wt.Root())

	_, err := st.Read(s.built("v0.4.3", s.base))

	s.Require().NoError(err)
	s.Equal(string(storeVersion(1)), storedLines(s.T(), path)[0]["api_version"])
}

// consumerFenceSuite pins who may migrate the job store of a workspace that is not
// magus's source: its history holds no commit that built magus, so only the binary's
// stamp judges it.
type consumerFenceSuite struct {
	suite.Suite
	repo *testkit.Repo
	// magus is the commit that built the binary, which the workspace never held.
	magus string
}

func TestConsumerFenceSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(consumerFenceSuite))
}

func (s *consumerFenceSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"app.txt": "app\n"})
	s.repo.Git("update-ref", "refs/remotes/origin/main", s.repo.Head())
	s.magus = strings.Repeat("e", 40)
}

// refused asserts a read of the store at path under b is refused for why, naming the
// consumer remedy, and that nothing was written.
func (s *consumerFenceSuite) refused(b types.MagusBuild, why string) {
	st, path, _ := behindStore(s.T(), s.repo.Root())
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)

	_, err = st.Read(types.WithMagusBuild(s.T().Context(), b))

	off, ok := errors.AsType[*offBaseMigrationError](err)
	s.Require().True(ok, "%v", err)
	s.True(off.Foreign)
	s.Contains(err.Error(), fmt.Sprintf("%s holds no commit %.12s, so it is not magus's source", s.repo.Root(), s.magus))
	s.Contains(err.Error(), why)
	s.Contains(err.Error(), "install a magus release, build magus with `./magus run go-build .` in a clean magus checkout, or run this binary under its own XDG_STATE_HOME")
	s.NotContains(err.Error(), "rebuild magus from origin/main")
	after, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Equal(string(raw), string(after), "nothing was written")
}

func (s *consumerFenceSuite) TestAReleaseMigratesAndSaysNoBaseJudgedIt() {
	st, path, notices := behindStore(s.T(), s.repo.Root())

	f, err := st.Read(types.WithMagusBuild(s.T().Context(), types.StampedBuild("v0.4.3", s.magus, "")))

	s.Require().NoError(err)
	s.Equal("ship it", f.Jobs[0].Parent)
	header := storedLines(s.T(), path)[0]
	s.Equal(string(storeVersion(1)), header["api_version"])
	s.Equal(map[string]any{"version": "v0.4.3", "commit": s.magus}, header["writer"])
	s.Contains(notices.String(), fmt.Sprintf("job: %s holds no commit %.12s, so no base judged this magus; its clean stamp v0.4.3 let it migrate job store %s\n", s.repo.Root(), s.magus, path))
}

func (s *consumerFenceSuite) TestACleanStampedDevBuildMigrates() {
	st, path, _ := behindStore(s.T(), s.repo.Root())

	_, err := st.Read(types.WithMagusBuild(s.T().Context(), types.StampedBuild("v0.5.0-rc.3-219-geeeeeeeee", s.magus, "")))

	s.Require().NoError(err)
	s.Equal(string(storeVersion(1)), storedLines(s.T(), path)[0]["api_version"])
}

func (s *consumerFenceSuite) TestAWorkspaceWithNoBaseMigratesAStampedBuild() {
	s.repo.Git("update-ref", "-d", "refs/remotes/origin/main")
	st, path, _ := behindStore(s.T(), s.repo.Root())

	_, err := st.Read(types.WithMagusBuild(s.T().Context(), types.StampedBuild("v0.4.3", s.magus, "")))

	s.Require().NoError(err)
	s.Equal(string(storeVersion(1)), storedLines(s.T(), path)[0]["api_version"])
}

func (s *consumerFenceSuite) TestADirtyStampedBuildIsRefused() {
	s.refused(types.StampedBuild("v0.4.3-dirty", s.magus, ""), "and it was built from a modified tree")
}

func (s *consumerFenceSuite) TestABuildWithOnlyGoVCSStampIsRefused() {
	s.refused(types.MagusBuild{Version: "unknown", Commit: s.magus, Source: types.BuildSourceVCS},
		"and it carries no magus version stamp: Go's VCS stamp can name another revision")
}

func (s *jobSuite) TestARootNoVersionControlHoldsMigratesAndSaysNothingJudgedIt() {
	root := s.T().TempDir()
	st, path, notices := behindStore(s.T(), root)

	_, err := st.Read(s.T().Context())

	s.Require().NoError(err)
	s.Equal(string(storeVersion(1)), storedLines(s.T(), path)[0]["api_version"])
	s.Contains(notices.String(), fmt.Sprintf("job: no version control holds %s, so no base judged this magus before it migrated job store %s\n", root, path))
}

// writerStampSuite pins the writer every write of the job store records in its header.
type writerStampSuite struct {
	suite.Suite
}

func TestWriterStampSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(writerStampSuite))
}

func (s *writerStampSuite) TestEveryWriteStampsItsWriter() {
	st := tempStore(s.T(), s.T().TempDir())
	ctx := types.WithMagusBuild(s.T().Context(), types.MagusBuild{Version: "v1", Commit: "c1"})
	_, err := st.Update(ctx, "a", func(cur *job.Job) { *cur = lease("a") })
	s.Require().NoError(err)
	path, err := st.Path()
	s.Require().NoError(err)
	s.Equal(map[string]any{"version": "v1", "commit": "c1"}, storedLines(s.T(), path)[0]["writer"])

	ctx = types.WithMagusBuild(s.T().Context(), types.MagusBuild{Version: "v2", Commit: "c2"})
	_, err = st.Update(ctx, "b", func(cur *job.Job) { *cur = lease("b") })

	s.Require().NoError(err)
	lines := storedLines(s.T(), path)
	s.Equal(map[string]any{"version": "v2", "commit": "c2"}, lines[0]["writer"], "the header names the last writer")
	s.Len(lines, 3, "a new writer rewrites the file, so one header leads it")
}

func (s *writerStampSuite) TestAWriteWithNoBuildOnItsContextStampsTheBinary() {
	st := tempStore(s.T(), s.T().TempDir())

	_, err := st.Update(s.T().Context(), "a", func(cur *job.Job) { *cur = lease("a") })

	s.Require().NoError(err)
	path, err := st.Path()
	s.Require().NoError(err)
	commit, _ := buildinfo.Commit()
	s.Equal(map[string]any{"version": buildinfo.UnknownVersion, "commit": commit}, storedLines(s.T(), path)[0]["writer"])
}

// snakeRecordSuite pins the migration that respells the store's records in snake_case.
type snakeRecordSuite struct {
	suite.Suite
}

func TestSnakeRecordSuite(t *testing.T) { suite.Run(t, new(snakeRecordSuite)) }

// The lines below are what a store at migration 9 held: camelCase keys, CamelCase enum
// values and a check spelling noDefaultCharms.
const (
	camelHeader = `{"apiVersion":"safetyscissors.job/v1alpha1","kind":"JobList","migration":9,"retained":0}`
	camelRow    = `{"apiVersion":"safetyscissors.job/v1alpha1","kind":"Job",` +
		`"metadata":{"name":"a","generation":2,"annotations":{"x.example/dependsOn":"keepMe"}},` +
		`"spec":{"agent":{"instructions":"x","worker":{"type":"Resumed"}},"paths":{"write":["a.go"]},"dependsOn":["b"],` +
		`"briefFormat":"Markdown","maxAttempts":2,"check":{"target":"generate","project":".","noDefaultCharms":true},` +
		`"goals":[{"id":"g","kind":"Check","expect":"Passed","check":{"target":"test","project":"."},"accept":["Witnessed"]},` +
		`{"id":"s","kind":"Symbol","expect":"Unreferenced","symbols":["X"]}]},` +
		`"status":{"conditions":[{"type":"Running","status":"True","observedGeneration":2,"lastTransitionTime":"1970-01-01T00:00:02Z","reason":"Running","message":""}],` +
		`"modelTier":{"name":"bounded","rank":1,"skills":"Short","source":"UserMatrix"},"writeProof":"Disjoint","reportedBase":"abc",` +
		`"baseVerdict":"RevisionMatch","registeredBy":{"user":"u","entryPoint":"Cli","credential":{"class":"Operator","id":"i"}},` +
		`"registered":1,"checkoutRoot":"/tmp/x","created":1,"updated":2,` +
		`"gateAttempts":[{"gateId":"g","attempt":{"found":true,"ref":"out1","timestampMs":5}}]}}`
	camelAttempt = `{"apiVersion":"safetyscissors.job/v1alpha1","kind":"Attempt","spec":{"job":"a","n":1},` +
		`"status":{"started":1,"ended":2,"end":"Handback","jobState":"NoReturn","observedGeneration":2,"retry":{"retryable":false,"change":"Upgrade"}}}`
)

// snakeRowWant is camelRow as this build writes the row it holds, annotation included.
func (s *snakeRecordSuite) snakeRowWant() string {
	row := job.Job{ID: "a", Generation: 2, State: job.StateRunning, Created: 1, Updated: 2,
		Agent: &job.Agent{Instructions: "x", Worker: job.Worker{Type: job.WorkerTypeResumed}},
		Paths: job.Paths{Write: []string{"a.go"}}, DependsOn: []string{"b"}, BriefFormat: job.BriefFormatMarkdown, MaxAttempts: 2,
		Check: fixtureCheck(types.LeaseCheck{Target: "generate", Project: ".", NoDefaultCharms: true}),
		Postconditions: []job.Postcondition{
			{ID: "g", Kind: job.PostconditionKindCheck, Expect: job.ExpectPassed, Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "."}), Accept: []job.Basis{job.BasisWitnessed}},
			{ID: "s", Kind: job.PostconditionKindSymbol, Expect: job.ExpectUnreferenced, Symbols: []string{"X"}},
		},
		ModelTier:  &tier.Assignment{Name: "bounded", Rank: 1, Skills: tier.SkillVariantShort, Source: tier.SourceUserMatrix},
		WriteProof: job.WriteProofDisjoint, ReportedBase: "abc", BaseVerdict: job.BaseRevisionMatch,
		RegisteredBy: job.Origin{User: "u", EntryPoint: job.EntryPointCLI, Credential: job.Credential{Kind: "operator", ID: "i"}},
		Registered:   1, CheckoutRoot: "/tmp/x",
		PostconditionAttempts: []job.PostconditionAttempt{{PostconditionID: "g", Attempt: job.CapturedRun{Found: true, Ref: "out1", TimestampMs: 5}}},
	}
	line, err := job.MarshalRecord(row, nil)
	s.Require().NoError(err)
	var e map[string]any
	s.Require().NoError(codec.Unmarshal(line, &e))
	e["metadata"].(map[string]any)["annotations"] = map[string]any{"x.example/dependsOn": "keepMe"}
	want, err := codec.Marshal(e)
	s.Require().NoError(err)
	return string(want)
}

// snaked is a stored line as [snakeRecord] rewrites it.
func (s *snakeRecordSuite) snaked(text string) (string, error) {
	e, err := decodeEntry([]byte(text))
	s.Require().NoError(err)
	if e, err = snakeRecord(e); err != nil {
		return "", err
	}
	out, err := codec.Marshal(e)
	s.Require().NoError(err)
	return string(out), nil
}

func (s *snakeRecordSuite) TestARowRespellsItsKeysItsEnumsAndItsChecks() {
	got, err := s.snaked(camelRow)

	s.Require().NoError(err)
	s.JSONEq(s.snakeRowWant(), got)
}

func (s *snakeRecordSuite) TestAnAttemptRespellsItsKeysAndItsEnums() {
	want, err := job.MarshalAttempt(job.Attempt{Job: "a", N: 1, Status: job.AttemptStatus{Started: 1, Ended: 2, End: job.AttemptEndHandback,
		State: job.StateNoReturn, ObservedGeneration: 2, Retry: job.RetryVerdict{Change: job.RetryAfterUpgrade}}}, false)
	s.Require().NoError(err)

	got, err := s.snaked(camelAttempt)

	s.Require().NoError(err)
	s.JSONEq(string(want), got)
}

func (s *snakeRecordSuite) TestTheHeaderRespellsItsAPIVersion() {
	got, err := s.snaked(camelHeader)

	s.Require().NoError(err)
	s.JSONEq(`{"api_version":"safetyscissors.job/v1alpha1","kind":"JobList","migration":9,"retained":0}`, got)
}

func (s *snakeRecordSuite) TestARespelledLineIsKeptAsItIs() {
	once, err := s.snaked(camelRow)
	s.Require().NoError(err)

	twice, err := s.snaked(once)

	s.Require().NoError(err)
	s.Equal(once, twice)
}

func (s *snakeRecordSuite) TestAValueNoListSpellsIsRefused() {
	_, err := s.snaked(strings.Replace(camelRow, `"writeProof":"Disjoint"`, `"writeProof":"Sideways"`, 1))

	var unplaced *unplacedEnumError
	s.Require().ErrorAs(err, &unplaced)
	s.Equal(&unplacedEnumError{Path: "status.write_proof", Value: "Sideways", Values: unplaced.Values}, unplaced)
	s.Contains(unplaced.Values, "disjoint")
}

func (s *snakeRecordSuite) TestACredentialKindTheCallerDoesNotAdmitIsRefused() {
	_, err := s.snaked(strings.Replace(camelRow, `"class":"Operator"`, `"class":"Smartcard"`, 1))

	var unplaced *unplacedEnumError
	s.Require().ErrorAs(err, &unplaced)
	s.Equal("status.registered_by.credential.class", unplaced.Path)
}

func (s *snakeRecordSuite) TestAKeyUnderBothSpellingsIsRefused() {
	_, err := s.snaked(strings.Replace(camelRow, `"maxAttempts":2`, `"maxAttempts":2,"max_attempts":3`, 1))

	var collision *snakeKeyCollisionError
	s.Require().ErrorAs(err, &collision)
	s.Equal("max_attempts", collision.Snake)
}

func (s *snakeRecordSuite) TestAnnotationsAndUnknownMembersAreKeptVerbatim() {
	got, err := s.snaked(strings.Replace(camelRow, `"checkoutRoot":"/tmp/x"`, `"checkoutRoot":"/tmp/x","laterMember":{"innerKey":1}`, 1))

	s.Require().NoError(err)
	s.Contains(got, `"x.example/dependsOn":"keepMe"`)
	s.Contains(got, `"laterMember":{"innerKey":1}`)
}

func (s *snakeRecordSuite) TestAVerificationEvidenceKeepsItsGateID() {
	got, err := s.snaked(strings.Replace(camelRow, `"checkoutRoot":"/tmp/x"`,
		`"checkoutRoot":"/tmp/x","result":{"verificationEvidence":[{"gateId":"g","outputRef":"out1"}]}`, 1))

	s.Require().NoError(err)
	s.Contains(got, `"verification_evidence":[{"gate_id":"g","output_ref":"out1"}]`)
	s.NotContains(got, `"goals"`)
	s.NotContains(got, `"gate_attempts"`)
}

func TestRespellFeaturesSuite(t *testing.T) { suite.Run(t, new(respellFeaturesSuite)) }

// respellFeaturesSuite pins [featuresRespelled] on a row and on the header.
type respellFeaturesSuite struct {
	suite.Suite
}

func (s *respellFeaturesSuite) respelled(text string) string {
	e, err := decodeEntry([]byte(text))
	s.Require().NoError(err)
	e, err = respellFeatures(e)
	s.Require().NoError(err)
	out, err := codec.Marshal(e)
	s.Require().NoError(err)
	return string(out)
}

func (s *respellFeaturesSuite) TestARowsSignatureGoalsBecomesSignaturePostconditions() {
	got := s.respelled(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a",` +
		`"annotations":{"safetyscissors.job/requires":"claim-declarations,signature-goals","x.example/note":"signature-goals"}}}`)

	s.JSONEq(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a",`+
		`"annotations":{"safetyscissors.job/requires":"claim-declarations,signature-postconditions","x.example/note":"signature-goals"}}}`, got)
	var stored struct {
		Metadata apiversion.Metadata `json:"metadata"`
	}
	s.Require().NoError(codec.Unmarshal([]byte(got), &stored))
	var row job.Job
	for _, f := range stored.Metadata.Requires("safetyscissors.job") {
		row.Requires = append(row.Requires, string(f))
	}
	s.Equal([]string{"claim-declarations", "signature-postconditions"}, row.Requires)
	s.Empty(row.Unmet(), "this build implements every feature the row requires")
}

func (s *respellFeaturesSuite) TestTheHeaderIsRespelledToo() {
	header, err := job.MarshalHeader([]apiversion.Feature{"signature-goals"})
	s.Require().NoError(err)

	want, err := job.MarshalHeader([]apiversion.Feature{"signature-postconditions"})
	s.Require().NoError(err)
	s.JSONEq(string(want), s.respelled(string(header)))
}

func (s *respellFeaturesSuite) TestALineNamingNoRenamedFeatureIsKeptAsItIs() {
	line := `{"kind":"Job","metadata":{"name":"a","annotations":{"safetyscissors.job/requires":"job-generations"}}}`

	s.JSONEq(line, s.respelled(line))
	s.JSONEq(`{"kind":"JobList"}`, s.respelled(`{"kind":"JobList"}`))
}

// snakeStoreSuite migrates a store left at the migration before [keysSnaked], as a binary
// built on the base does: only such a binary migrates the store every checkout shares.
type snakeStoreSuite struct {
	suite.Suite
	repo *testkit.Repo
	base string
}

func TestSnakeStoreSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(snakeStoreSuite))
}

func (s *snakeStoreSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n"})
	s.base = s.repo.Head()
	s.repo.Git("update-ref", "refs/remotes/origin/main", s.base)
}

// planted is a store at root holding the camel lines, at the migration before keysSnaked,
// counted as headers of that time counted it.
//
// compat: see migrate.Store.Count.
func (s *snakeStoreSuite) planted() (st *Store, jobs, attempts string) {
	st = tempStore(s.T(), s.repo.Root())
	st.notices = &bytes.Buffer{}
	at := slices.IndexFunc(jobMigrations(), func(m migrate.Migration) bool { return m.Name == keysSnaked })
	s.Require().Positive(at)
	header := strings.Replace(camelHeader, `"migration":9`, fmt.Sprintf(`"migration":%d`, at), 1)
	jobs = plantRaw(s.T(), st, strings.Join([]string{header, camelRow}, "\n"))
	attempts, err := st.AttemptsPath()
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(attempts, []byte(camelAttempt+"\n"), 0o600))
	return st, jobs, attempts
}

func (s *snakeStoreSuite) TestTheStoreReadsAfterItsLinesAreRespelled() {
	st, jobs, attempts := s.planted()
	ctx := types.WithMagusBuild(s.T().Context(), types.StampedBuild("v0.4.3", s.base, ""))

	f, err := st.Read(ctx)

	s.Require().NoError(err)
	s.Require().Len(f.Jobs, 1)
	row := f.Jobs[0]
	s.Equal(job.StateRunning, row.State)
	s.Equal(types.LeaseCheck{Target: "generate", Project: ".", NoDefaultCharms: true}, checkIn(s.T(), row.Check))
	s.Equal([]job.Basis{job.BasisWitnessed}, row.Postconditions[0].Accept)
	s.Equal(job.WriteProofDisjoint, row.WriteProof)
	s.Equal(job.EntryPointCLI, row.RegisteredBy.EntryPoint)
	lines := storedLines(s.T(), jobs)
	s.Require().Len(lines, 2)
	s.Equal(string(currentStoreVersion()), lines[0]["api_version"])
	s.NotContains(lines[0], "apiVersion")
	s.InDelta(len(jobMigrations()), lines[0]["migration"], 0, "the count an older magus reads rides beside the version")
	s.Equal(map[string]any{"x.example/dependsOn": "keepMe"}, lines[1]["metadata"].(map[string]any)["annotations"])

	tries, err := st.Attempts(ctx)
	s.Require().NoError(err)
	s.Require().Len(tries, 1)
	s.Equal(job.AttemptStatus{Started: 1, Ended: 2, End: job.AttemptEndHandback, State: job.StateNoReturn, ObservedGeneration: 2,
		Retry: job.RetryVerdict{Change: job.RetryAfterUpgrade}}, tries[0].Status)
	raw, err := os.ReadFile(attempts)
	s.Require().NoError(err)
	s.NotContains(string(raw), "apiVersion")
}

func (s *snakeStoreSuite) TestAValueNoListSpellsLeavesTheStoreAsItWas() {
	st, jobs, _ := s.planted()
	body, err := os.ReadFile(jobs)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(jobs, []byte(strings.Replace(string(body), `"baseVerdict":"RevisionMatch"`, `"baseVerdict":"Elsewhere"`, 1)), 0o600))
	before, err := os.ReadFile(jobs)
	s.Require().NoError(err)

	_, err = st.Read(types.WithMagusBuild(s.T().Context(), types.StampedBuild("v0.4.3", s.base, "")))

	var unplaced *unplacedEnumError
	s.Require().ErrorAs(err, &unplaced)
	after, err := os.ReadFile(jobs)
	s.Require().NoError(err)
	s.Equal(string(before), string(after), "nothing was written")
}
