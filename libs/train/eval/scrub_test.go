package eval

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/train/dedup"
)

type scrubSuite struct{ suite.Suite }

func TestScrubSuite(t *testing.T) { suite.Run(t, new(scrubSuite)) }

const scrubSrc = `package p

func Keep(a int) int {
	return a + 1
}

func Leak(a, b int) int {
	first := a * b * 1001 + 7 * a + b * 3 - 12
	second := first * first * 2002 + 9 * first
	return second
}
`

func (s *scrubSuite) TestScrubRemovesOnlyTheConstructOverlappingAnEvalText() {
	d := NewDecontaminator([]string{"first := a * b * 1001 + 7 * a + b * 3 - 12 second := first * first * 2002 + 9 * first return second"})
	hits := d.Hits(scrubSrc)
	s.NotEmpty(hits)
	got := Scrub(d, dedup.Go, scrubSrc)
	s.Contains(got.Text, "func Keep")
	s.NotContains(got.Text, "1001")
	s.Positive(got.Removed)
	s.Equal(len(scrubSrc)-len(got.Text), got.Removed)
	s.Empty(d.Hits(got.Text))
	s.True(got.Reparsed)
}

func (s *scrubSuite) TestScrubLeavesACleanFileAlone() {
	d := NewDecontaminator([]string{"nothing here matches the source at all not a single window of it"})
	got := Scrub(d, dedup.Go, scrubSrc)
	s.Equal(scrubSrc, got.Text)
	s.Zero(got.Removed)
}

func (s *scrubSuite) TestScrubRemovesAFileEqualToAnEvalText() {
	d := NewDecontaminator([]string{scrubSrc})
	s.Empty(Scrub(d, dedup.Go, scrubSrc).Text)
}
