//go:build linux

package mem

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type PSIPressureSuite struct {
	suite.Suite
}

func TestPSIPressureSuite(t *testing.T) {
	suite.Run(t, new(PSIPressureSuite))
}

func psiFile(someAvg10, fullAvg10 string) string {
	return "some avg10=" + someAvg10 + " avg60=0.00 avg300=0.00 total=1\n" +
		"full avg10=" + fullAvg10 + " avg60=0.00 avg300=0.00 total=1\n"
}

func (s *PSIPressureSuite) TestFullStallAtTheThresholdRefuses() {
	got, err := parsePSI(strings.NewReader(psiFile("90.00", "25.00")))
	s.Require().NoError(err)
	s.Contains(got, "25.0% of the last 10 s")
}

func (s *PSIPressureSuite) TestFullStallBelowTheThresholdAdmits() {
	got, err := parsePSI(strings.NewReader(psiFile("0.00", "24.99")))
	s.Require().NoError(err)
	s.Empty(got)
}

func (s *PSIPressureSuite) TestSomeStallAloneAdmits() {
	got, err := parsePSI(strings.NewReader(psiFile("99.00", "0.00")))
	s.Require().NoError(err)
	s.Empty(got)
}

func (s *PSIPressureSuite) TestAFileWithNoFullLineIsAnError() {
	_, err := parsePSI(strings.NewReader("some avg10=1.00 avg60=0.00 avg300=0.00 total=1\n"))
	s.Error(err)
}

func (s *PSIPressureSuite) TestAnUnparsableAverageIsAnError() {
	_, err := parsePSI(strings.NewReader("full avg10=high avg60=0.00 avg300=0.00 total=1\n"))
	s.Error(err)
}

func (s *PSIPressureSuite) TestAFullLineWithNoAverageIsAnError() {
	_, err := parsePSI(strings.NewReader("full total=1\n"))
	s.Error(err)
}
