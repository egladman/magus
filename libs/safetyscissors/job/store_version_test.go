package job

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/apiversion"
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
