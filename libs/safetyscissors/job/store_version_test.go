package job

import (
	"errors"
	"os"
	"path/filepath"
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

func (s *StoreVersionSuite) TestANewerStoreVersionNamesBoth() {
	err := &NewerWriterError{Path: "/s/jobs.jsonl", Found: "safetyscissors.job/v1alpha9", Known: "safetyscissors.job/v1alpha7"}

	s.Equal("job store /s/jobs.jsonl is at safetyscissors.job/v1alpha9, past this build's safetyscissors.job/v1alpha7", err.Error())
}

func (s *StoreVersionSuite) TestARecordOfAnotherVersionNamesBoth() {
	err := &RecordVersionError{Path: "/s/jobs.jsonl", Line: 2, Want: APIVersion, Got: apiversion.APIVersion("batch.magus/v1alpha1")}

	s.Equal("job: /s/jobs.jsonl:2 is batch.magus/v1alpha1, and this build reads safetyscissors.job/v1alpha1", err.Error())
}

func (s *StoreVersionSuite) TestARetiredNameNamesTheRowAndTheRespelling() {
	err := &RetiredNameError{Record: "a", Retired: "spec.goals", Now: "spec.postconditions"}

	s.Equal("job a spells spec.goals, a name this build retired for spec.postconditions: migrate the store, respelling spec.goals as spec.postconditions, before reading it", err.Error())
}

// A row spelling its postconditions goals would keep them as an unknown member, and every
// reader would see a row with none.
func (s *StoreVersionSuite) TestARowSpellingGoalsIsRefused() {
	_, _, err := readRecord([]byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},` +
		`"spec":{"goals":[{"id":"tests","kind":"check","expect":"passed","check":{"target":"test","project":"."}}]},"status":{"created":1,"updated":1}}`))

	retired, ok := errors.AsType[*RetiredNameError](err)
	s.Require().True(ok, err)
	s.Equal(RetiredNameError{Record: "a", Retired: "spec.goals", Now: "spec.postconditions"}, *retired)
}

func (s *StoreVersionSuite) TestARowRequiringSignatureGoalsIsRefused() {
	_, _, err := readRecord([]byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job",` +
		`"metadata":{"name":"a","annotations":{"safetyscissors.job/requires":"signature-goals"}},"spec":{},"status":{"created":1,"updated":1}}`))

	retired, ok := errors.AsType[*RetiredNameError](err)
	s.Require().True(ok, err)
	s.Equal(RetiredNameError{Record: "a", Retired: "signature-goals", Now: "signature-postconditions"}, *retired)
}

// The store refuses the file rather than read it as rows with no postconditions.
func (s *StoreSuite) TestAStoreHoldingARetiredNameIsRefused() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, jobsFileName), []byte(
		`{"api_version":"safetyscissors.job/v1alpha1","kind":"JobList","metadata":{"annotations":{"safetyscissors.job/requires":"signature-goals"}}}`+"\n"), 0o600))

	_, err := s.open().List(s.T().Context())

	var retired *RetiredNameError
	s.Require().ErrorAs(err, &retired)
	s.Equal(RetiredNameError{Retired: "signature-goals", Now: "signature-postconditions"}, *retired)
}

func (s *StoreSuite) TestAStoredRowSpellingGoalsIsRefused() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, jobsFileName), []byte(
		`{"api_version":"safetyscissors.job/v1alpha1","kind":"JobList"}`+"\n"+
			`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},"spec":{"goals":[]},"status":{"created":1,"updated":1}}`+"\n"), 0o600))

	_, err := s.open().List(s.T().Context())

	var retired *RetiredNameError
	s.Require().ErrorAs(err, &retired)
	s.Equal("spec.goals", retired.Retired)
}

func (s *StoreVersionSuite) TestARowSpellingPostconditionsReads() {
	row, ok, err := readRecord([]byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},"spec":{},"status":{"created":1,"updated":1}}`))

	s.Require().NoError(err)
	s.True(ok)
	s.Equal("a", row.ID)
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

func (s *StoreVersionSuite) TestAStoredCheckPostconditionComesToAcceptWitnessed() {
	line := s.line(`{"kind":"Job","spec":{"goals":[{"id":"tests","kind":"Check"},{"id":"docs","kind":"Paths"},{"id":"lint","kind":"Check","accept":["Reported"]}]}}`)

	got, err := AcceptWitnessed(line)

	s.Require().NoError(err)
	s.JSONEq(`{"goals":[{"id":"tests","kind":"Check","accept":["Witnessed"]},{"id":"docs","kind":"Paths"},{"id":"lint","kind":"Check","accept":["Reported"]}]}`, s.spec(got))
	again, err := AcceptWitnessed(got)
	s.Require().NoError(err)
	s.JSONEq(s.spec(got), s.spec(again), "the rewrite is the same applied twice")
}

func (s *StoreVersionSuite) TestALineWithNoPostconditionsComesBackAsItWas() {
	header := s.line(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"JobList","migration":7}`)

	got, err := AcceptWitnessed(header)

	s.Require().NoError(err)
	s.Equal(header, got)
}

// AcceptWitnessed rewrites a line of the CamelCase layout an older build stored, so it
// writes that layout's spelling, and the line it leaves is still one this build refuses:
// only the caller's later migration brings it forward.
func (s *StoreVersionSuite) TestAMigratedRowKeepsTheOlderLayout() {
	line := s.line(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"Job","metadata":{"name":"a"},` +
		`"spec":{"goals":[{"id":"tests","kind":"Check","expect":"Passed","check":{"target":"test","project":"."}}]},"status":{}}`)

	got, err := AcceptWitnessed(line)
	s.Require().NoError(err)
	text, err := codec.Marshal(got)
	s.Require().NoError(err)
	_, _, err = readRecord(text)

	s.Contains(s.spec(got), `"accept":["Witnessed"]`)
	refused, ok := errors.AsType[*apiversion.RefusedError](err)
	s.Require().True(ok, err)
	s.Empty(refused.Got.APIVersion, "the older layout keyed its version apiVersion")
}
