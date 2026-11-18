package job

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

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
	var status struct {
		ModelTier struct {
			Source string `json:"source"`
		} `json:"modelTier"`
	}
	s.Require().NoError(codec.Unmarshal(e["status"], &status))
	return status.ModelTier.Source
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
	s.ErrorContains(err, fmt.Sprintf("job store %s has no header line, and line 1 is an enveloped row, which only a store past migration %d holds", path, envelopedAt()))
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
	s.Equal(string(job.APIVersion), stored[1]["apiVersion"])
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
	s.InDelta(1, storedLines(s.T(), path)[0]["migration"], 0)
	s.Contains(notices.String(), "so no base judged this magus")
}

func (s *rootlessMigrationSuite) TestAWorkingDirectoryOnABranchIsRefused() {
	repo := testkit.NewRepository(s.T(), nil)
	repo.Git("update-ref", "refs/remotes/origin/main", repo.Head())
	branch := repo.Commit("branch", map[string]string{"b.txt": "b\n"})
	s.T().Chdir(repo.Root())
	st, _, _ := behindStore(s.T(), "")

	_, err := st.Read(types.WithMagusBuild(s.T().Context(), types.MagusBuild{Version: "v1", Commit: branch}))

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
	s.Require().NoError(s.st.fileResult(s.T().Context(), "a", &job.Result{Job: "a", ChangedPaths: []string{"a.go"}}, 30))
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

// before writes the store back as the migration before groupsMoved left it.
func (s *regroupSuite) before() {
	group := job.APIVersion.Group() + "/"
	s.rewrite(s.jobs, group, batchMagusGroup+"/")
	s.rewrite(s.jobs, fmt.Sprintf(`"migration":%d`, len(jobMigrations())), fmt.Sprintf(`"migration":%d`, len(jobMigrations())-1))
	s.rewrite(s.attempts, group, batchMagusGroup+"/")
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
	s.rewrite(s.jobs, `"apiVersion":"`+string(job.APIVersion)+`","kind":"Job"`, `"apiVersion":"example.jobs/v1","kind":"Job"`)
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

// built is a context whose binary was built at version from commit.
func (s *baseFenceSuite) built(version, commit string) context.Context {
	return types.WithMagusBuild(s.T().Context(), types.MagusBuild{Version: version, Commit: commit})
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
	s.InDelta(1, header["migration"], 0)
	s.Equal(map[string]any{"version": "v0.4.3", "commit": s.base}, header["writer"])
}

func (s *baseFenceSuite) TestABranchBinaryIsRefused() {
	st, path, notices := behindStore(s.T(), s.repo.Root())

	off := s.refused(s.built("v0.4.3-1-gfeat", s.branch), st, path,
		fmt.Sprintf("origin/main at %s does not contain commit %.12s, which built it", s.repo.Git("rev-parse", "--short", s.base), s.branch))

	s.Equal(&offBaseMigrationError{
		Path: path, Found: 0, Known: 1, Build: types.MagusBuild{Version: "v0.4.3-1-gfeat", Commit: s.branch}, Base: "origin/main",
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

	s.refused(s.built("v0.4.3", "unknown"), st, path, "this binary carries no build commit, so nothing says origin/main contains it")
}

func (s *baseFenceSuite) TestABinaryBuiltFromAModifiedTreeIsRefused() {
	st, path, _ := behindStore(s.T(), s.repo.Root())

	s.refused(s.built("v0.4.3-dirty", s.base), st, path, "this binary was built from a modified tree")
}

func (s *baseFenceSuite) TestACommitTheRepositoryLacksIsRefused() {
	st, path, _ := behindStore(s.T(), s.repo.Root())

	s.refused(s.built("v0.4.3", strings.Repeat("e", 40)), st, path, "cannot say whether origin/main")
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
	s.InDelta(1, storedLines(s.T(), path)[0]["migration"], 0)
}

func (s *jobSuite) TestARootNoVersionControlHoldsMigratesAndSaysNothingJudgedIt() {
	root := s.T().TempDir()
	st, path, notices := behindStore(s.T(), root)

	_, err := st.Read(s.T().Context())

	s.Require().NoError(err)
	s.InDelta(1, storedLines(s.T(), path)[0]["migration"], 0)
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
