package job

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
)

type StoreVersionSuite struct {
	suite.Suite
}

func TestStoreVersionSuite(t *testing.T) { suite.Run(t, new(StoreVersionSuite)) }

func (s *StoreVersionSuite) TestANewerWriterNamesWhatItLacks() {
	err := &NewerWriterError{Path: "/s/jobs.jsonl", Lacks: []string{"jobs-v2"}}

	s.Contains(err.Error(), `/s/jobs.jsonl requires "jobs-v2"`)
}

func (s *StoreVersionSuite) TestANewerMigrationNamesBoth() {
	err := &NewerWriterError{Path: "/s/jobs.jsonl", Found: 9, Known: 7}

	s.Equal("job store /s/jobs.jsonl is at migration 9, past this build's 7", err.Error())
}

func (s *StoreVersionSuite) TestARecordOfAnotherVersionSaysHowToGetPastIt() {
	err := &RecordVersionError{Path: "/s/jobs.jsonl", Line: 2, Want: RecordAPIVersion, Got: apiversion.APIVersion("batch.magus/v1alpha1")}

	s.Contains(err.Error(), "move /s/jobs.jsonl aside, or delete it, and fork the jobs again")
}

func (s *StoreVersionSuite) TestAShellWordIsQuotedOnlyWhenItMustBe() {
	s.Equal("/a/b.json", shellQuote("/a/b.json"))
	s.Equal(`'it'\''s here'`, shellQuote("it's here"))
}

func (s *StoreVersionSuite) line(text string) map[string]codec.RawMessage {
	var line map[string]codec.RawMessage
	s.Require().NoError(codec.Unmarshal([]byte(text), &line))
	return line
}

func (s *StoreVersionSuite) spec(line map[string]codec.RawMessage) string {
	return string(line["spec"])
}

func (s *StoreVersionSuite) TestAStoredCheckGoalComesToAcceptWitnessed() {
	line := s.line(`{"kind":"Job","spec":{"goals":[{"id":"tests","kind":"Check"},{"id":"docs","kind":"Paths"},{"id":"lint","kind":"Check","accept":["Reported"]}]}}`)

	got, err := AcceptWitnessed(line)

	s.Require().NoError(err)
	s.JSONEq(`{"goals":[{"id":"tests","kind":"Check","accept":["Witnessed"]},{"id":"docs","kind":"Paths"},{"id":"lint","kind":"Check","accept":["Reported"]}]}`, s.spec(got))
	again, err := AcceptWitnessed(got)
	s.Require().NoError(err)
	s.JSONEq(s.spec(got), s.spec(again), "the rewrite is the same applied twice")
}

func (s *StoreVersionSuite) TestALineWithNoGoalsComesBackAsItWas() {
	header := s.line(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"JobList","migration":7}`)

	got, err := AcceptWitnessed(header)

	s.Require().NoError(err)
	s.Equal(header, got)
}

func (s *StoreVersionSuite) TestAMigratedRowReadsItsBasis() {
	line := s.line(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},` +
		`"spec":{"goals":[{"id":"tests","kind":"Check","expect":"Passed","check":{"target":"test","project":"."}}]},"status":{}}`)

	got, err := AcceptWitnessed(line)
	s.Require().NoError(err)
	text, err := codec.Marshal(got)
	s.Require().NoError(err)
	row, ok, err := ReadRecord(text)

	s.Require().NoError(err)
	s.Require().True(ok)
	s.Equal([]Basis{BasisWitnessed}, row.Goals[0].Accept)
}
