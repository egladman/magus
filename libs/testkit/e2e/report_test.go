package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
)

type reportSuite struct {
	suite.Suite
	report *Report
}

func TestReport(t *testing.T) { suite.Run(t, new(reportSuite)) }

func (s *reportSuite) SetupTest() { s.report = NewReport() }

func (s *reportSuite) TestTrackRecordsAPass() {
	s.Run("subject", func() { s.report.Track(s.T(), "a", "passes") })

	s.Equal([]Case{{Subject: "a", Name: "passes", Status: OutcomePass}}, s.report.Cases())
}

func (s *reportSuite) TestTrackRecordsASkipWithItsDetail() {
	s.Run("subject", func() {
		c := s.report.Track(s.T(), "a", "skips")
		c.Detail = "not installed"
		s.T().Skip(c.Detail)
	})

	s.Equal([]Case{{Subject: "a", Name: "skips", Status: OutcomeSkip, Detail: "not installed"}}, s.report.Cases())
}

// failedTest stands in for a test that failed, since failing a real
// subtest would fail this one too.
type failedTest struct{ testing.TB }

func (failedTest) Skipped() bool { return false }
func (failedTest) Failed() bool  { return true }

func (s *reportSuite) TestOutcomeReadsAFailure() {
	s.Equal(OutcomeFail, outcome(failedTest{}))
}

func (s *reportSuite) TestCasesSortBySubjectThenName() {
	s.Run("subject", func() {
		s.report.Track(s.T(), "b", "one")
		s.report.Track(s.T(), "a", "two")
		s.report.Track(s.T(), "a", "one")
	})

	s.Equal([]Case{
		{Subject: "a", Name: "one", Status: OutcomePass},
		{Subject: "a", Name: "two", Status: OutcomePass},
		{Subject: "b", Name: "one", Status: OutcomePass},
	}, s.report.Cases())
}

func (s *reportSuite) TestWriteCreatesItsDirectoryAndCarriesAttachments() {
	s.Run("subject", func() { s.report.Track(s.T(), "a", "one") })
	s.report.Attach("build", "v1")
	s.report.Attach("cases", "overwritten by Write")
	path := filepath.Join(s.T().TempDir(), "deep", "report.json")

	s.Require().NoError(s.report.Write(path))

	body, err := os.ReadFile(path)
	s.Require().NoError(err)
	var got writtenReport
	s.Require().NoError(json.Unmarshal(body, &got))
	s.NotEmpty(got.GeneratedAt)
	got.GeneratedAt = ""
	s.Equal(writtenReport{Build: "v1", Cases: []Case{{Subject: "a", Name: "one", Status: OutcomePass}}}, got)
}

type writtenReport struct {
	GeneratedAt string `json:"generated_at"`
	Build       string `json:"build"`
	Cases       []Case `json:"cases"`
}
