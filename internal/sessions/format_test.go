package sessions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type FormatSuite struct{ suite.Suite }

func TestFormat(t *testing.T) { suite.Run(t, new(FormatSuite)) }

func (s *FormatSuite) TestFormatDurOfNothingIsEmpty() {
	s.Empty(FormatDur(0))
	s.Empty(FormatDur(-time.Second))
}

func (s *FormatSuite) TestFormatDurKeepsATenthUnderTenSeconds() {
	s.Equal("0.3s", FormatDur(300*time.Millisecond))
	s.Equal("3.4s", FormatDur(3*time.Second+400*time.Millisecond))
}

func (s *FormatSuite) TestFormatDurRoundsToTheUnitAboveTen() {
	s.Equal("10s", FormatDur(10*time.Second))
	s.Equal("47s", FormatDur(47*time.Second))
	s.Equal("1m0s", FormatDur(time.Minute))
	s.Equal("2m17s", FormatDur(2*time.Minute+17*time.Second))
	s.Equal("1h0m", FormatDur(time.Hour))
	s.Equal("3h5m", FormatDur(3*time.Hour+5*time.Minute))
}
