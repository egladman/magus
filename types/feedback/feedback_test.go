package feedback

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type feedbackSuite struct{ suite.Suite }

func TestFeedback(t *testing.T) { suite.Run(t, new(feedbackSuite)) }

func (s *feedbackSuite) TestSectionLettersRunInLabelOrder() {
	var letters string
	for _, sec := range Sections() {
		s.Require().NoError(sec.Validate())
		letters += sec.letter()
	}
	s.Equal("ABCD", letters, "labels are A refused, B advised, C unguarded, D next-not-taken")
}

func (s *feedbackSuite) TestSectionOutsideTheSetNamesTheOnesThatExist() {
	s.Empty(Section("passed").letter())
	err := Section("passed").Validate()
	var unknown *UnknownValueError
	s.Require().ErrorAs(err, &unknown)
	s.Equal("section", unknown.Field)
	s.ErrorContains(err, "refused, advised, unguarded, next-not-taken")
}

func (s *feedbackSuite) TestVerdictValidate() {
	for _, v := range Verdicts() {
		s.Require().NoError(v.Validate())
	}
	err := Verdict("bad").Validate()
	var unknown *UnknownValueError
	s.Require().ErrorAs(err, &unknown)
	s.Equal("verdict", unknown.Field)
	s.ErrorContains(err, "should-deny, should-advise, wrong-deny, fine")
}
