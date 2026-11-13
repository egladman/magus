package job

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/types/agent"
)

type modelTierSourceJobSuite struct {
	suite.Suite
}

func TestModelTierSourceJobSuite(t *testing.T) { suite.Run(t, new(modelTierSourceJobSuite)) }

func (s *modelTierSourceJobSuite) entry(text string) migrate.Entry {
	var e migrate.Entry
	s.Require().NoError(json.Unmarshal([]byte(text), &e))
	return e
}

func (s *modelTierSourceJobSuite) source(e migrate.Entry) string {
	var status struct {
		ModelTier struct {
			Source string `json:"source"`
		} `json:"modelTier"`
	}
	s.Require().NoError(json.Unmarshal(e["status"], &status))
	return status.ModelTier.Source
}

func (s *modelTierSourceJobSuite) TestAStoredRowBecomesTheJobSpelling() {
	got, err := modelTierSourceJob(s.entry(`{"spec":{},"status":{"modelTier":{"name":"bounded","source":"Row"}}}`))

	s.Require().NoError(err)
	s.Equal(wireTierSource.wire[agent.ModelTierSourceJob], s.source(got))
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
func (s *pathsNestedSuite) spec(spec string) map[string]json.RawMessage {
	var e migrate.Entry
	s.Require().NoError(json.Unmarshal([]byte(`{"metadata":{"name":"a"},"spec":`+spec+`}`), &e))
	got, err := pathsNested(e)
	s.Require().NoError(err)
	var out map[string]json.RawMessage
	s.Require().NoError(json.Unmarshal(got["spec"], &out))
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
	s.Require().NoError(json.Unmarshal([]byte(`{"apiVersion":"batch.magus/v1alpha1","kind":"JobList"}`), &e))

	got, err := pathsNested(e)

	s.Require().NoError(err)
	s.NotContains(got, "spec")
}
