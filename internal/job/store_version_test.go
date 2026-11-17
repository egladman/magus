package job

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
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

// rootlessMigrationSuite pins the migration refusal for a store opened with
// no root, which keys on the working directory. It changes directory, so it
// runs alone.
type rootlessMigrationSuite struct {
	suite.Suite
}

func TestRootlessMigrationSuite(t *testing.T) { suite.Run(t, new(rootlessMigrationSuite)) }

func (s *rootlessMigrationSuite) TestAJobCheckoutRefusesAndWritesNothing() {
	root := s.T().TempDir()
	s.T().Chdir(root)
	st, path, notices := checkoutStore(s.T(), "", "running", forkCheckout(root))
	raw, err := os.ReadFile(path)
	s.Require().NoError(err)

	_, err = st.Read(s.T().Context())

	s.Require().ErrorContains(err, "a job's checkout never migrates the store")
	after, err := os.ReadFile(path)
	s.Require().NoError(err)
	s.Equal(string(raw), string(after), "nothing was written")
	s.Empty(notices.String())
}

func (s *rootlessMigrationSuite) TestAnotherCheckoutMigrates() {
	s.T().Chdir(s.T().TempDir())
	st, path, _ := checkoutStore(s.T(), "", "running", forkCheckout(s.T().TempDir()))

	_, err := st.Read(s.T().Context())

	s.Require().NoError(err)
	s.InDelta(1, storedLines(s.T(), path)[0]["migration"], 0)
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
	e, err := entryOf([]byte(`{"apiVersion":"batch.magus/v1alpha1","kind":"Job","metadata":{"name":"a","annotations":{"batch.magus/requires":"claim-declarations","other/key":"v"}},"spec":{}}`))
	s.Require().NoError(err)

	got, err := regroup(e)

	s.Require().NoError(err)
	text, err := codec.Marshal(got)
	s.Require().NoError(err)
	s.JSONEq(fmt.Sprintf(`{"apiVersion":%q,"kind":"Job","metadata":{"name":"a","annotations":{%q:"claim-declarations","other/key":"v"}},"spec":{}}`,
		job.APIVersion, job.APIVersion.Group()+"/requires"), string(text))
}

func (s *regroupSuite) TestAMigratedLineIsKeptByteForByte() {
	line := []byte(fmt.Sprintf(`{"apiVersion":%q,"kind":"Job","metadata":{"annotations":{%q:"x"},"name":"a"},"spec":{}}`,
		job.APIVersion, job.APIVersion.Group()+"/requires"))
	once, err := regroupedText(line)
	s.Require().NoError(err)

	twice, err := regroupedText(once)

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
