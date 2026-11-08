package progress

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/termkit"
)

type BarSuite struct{ suite.Suite }

func TestBar(t *testing.T) { suite.Run(t, new(BarSuite)) }

func (s *BarSuite) TestBarFillsInEighths() {
	s.Equal("█████░░░░░", text(Bar(1, 2, 10, termkit.SGRDim)))
	s.Equal("▌░", text(Bar(1, 4, 2, termkit.SGRDim)))
}

func (s *BarSuite) TestBarClampsDone() {
	s.Equal("████", text(Bar(9, 4, 4, termkit.SGRDim)))
	s.Equal("░░░░", text(Bar(-1, 4, 4, termkit.SGRDim)))
}

func (s *BarSuite) TestBarWithNoTotalDrawsNothing() {
	s.Empty(Bar(1, 0, 10, termkit.SGRDim).Spans)
}
