package enum

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type color string

var colors = Set[color]{"red", "green"}

type SetSuite struct{ suite.Suite }

func TestSet(t *testing.T) { suite.Run(t, new(SetSuite)) }

func (s *SetSuite) TestValidAcceptsACaseOrUnset() {
	assert.True(s.T(), colors.Valid("red"))
	assert.True(s.T(), colors.Valid(""), "the zero value means unset")
	assert.False(s.T(), colors.Valid("blue"))
}

func (s *SetSuite) TestStringsIsAFreshCopy() {
	got := colors.Strings()
	assert.Equal(s.T(), []string{"red", "green"}, got)
	got[0] = "mutated"
	assert.Equal(s.T(), []string{"red", "green"}, colors.Strings(), "a caller's copy cannot reach the set")
}

func (s *SetSuite) TestStringNamesUnset() {
	assert.Equal(s.T(), "unset", String(color("")))
	assert.Equal(s.T(), "red", String(color("red")))
}
