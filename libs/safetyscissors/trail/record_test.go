package trail

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type RecordSuite struct{ suite.Suite }

func TestRecord(t *testing.T) { suite.Run(t, new(RecordSuite)) }

func (s *RecordSuite) TestSectionLettersRunInLabelOrder() {
	var letters string
	for _, sec := range Sections() {
		s.Require().NoError(sec.Validate())
		letters += sec.Letter()
	}
	s.Equal("ABCD", letters, "labels are A refused, B advised, C unguarded, D next-not-taken")
}

func (s *RecordSuite) TestSectionOutsideTheSetNamesTheOnesThatExist() {
	s.Empty(Section("passed").Letter())
	s.ErrorContains(Section("passed").Validate(), "refused, advised, unguarded, next-not-taken")
}

func (s *RecordSuite) TestVerdictValidate() {
	for _, v := range Verdicts() {
		s.Require().NoError(v.Validate())
	}
	s.ErrorContains(Verdict("bad").Validate(), "should-deny, should-advise, wrong-deny, fine")
}
