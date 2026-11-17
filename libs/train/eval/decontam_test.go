package eval

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type decontamSuite struct{ suite.Suite }

func TestDecontamSuite(t *testing.T) { suite.Run(t, new(decontamSuite)) }

func (s *decontamSuite) TestDecontaminatorDropsRepeatedWindows() {
	evalText := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron"
	d := NewDecontaminator([]string{evalText})
	s.Equal("exact", d.Match(evalText))
	s.Equal("ngram", d.Match("prefix words here "+evalText+" and a tail"))
	s.Equal("", d.Match("one two three four five six seven eight nine ten eleven twelve thirteen fourteen"))
	s.Equal("", d.Match(""))
}

func (s *decontamSuite) TestDecontaminatorIgnoresBoilerplateGrams() {
	var texts []string
	for i := range Common + 2 {
		texts = append(texts, "if err != nil { return err } // shared boilerplate line number "+strings.Repeat("x", i))
	}
	d := NewDecontaminator(texts)
	s.Equal("", d.Match("a b c if err != nil { return err } // shared boilerplate line number zz more"))
}
